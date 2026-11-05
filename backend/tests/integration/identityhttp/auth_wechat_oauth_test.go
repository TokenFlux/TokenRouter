package identityhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// auth_wechat_oauth_test.go checks WeChat login, payment authorization, and identity
// persistence through HTTP fixtures. Login uses unionid when available and records
// each app/channel openid separately. Local provider replies and SQLite rows
// exercise current-user binding, legacy identity repair, ownership conflicts
// and pending signup choices. The open and mp table cases each build their own
// fixture and run sequentially. Payment OAuth separately checks the listed
// resume-token claims and signing-key selection. Endpoint overrides are
// package-global and are restored by testing cleanup after deferred fixture
// closes.

// TestWeChatAuthorizationCallbacksPreserveBrowserCookies completes login and
// payment callbacks with cookies selected by a browser jar. Site domains and
// path prefixes cannot divert callbacks from their host-only state, while GET
// and CAPTCHA POST login starts retain their pending browser/session proof.
func TestWeChatAuthorizationCallbacksPreserveBrowserCookies(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	// Restore the shared endpoint hooks after all sequential scenarios finish,
	// so later provider fixtures cannot inherit this test's closed local server.
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Supply fixed upstream identity data only after the callback accepts its
		// browser proof; response write errors are immaterial to these local assertions.
		if strings.Contains(r.URL.Path, "/access_token") {
			_, _ = w.Write([]byte(`{"access_token":"fixture-access","openid":"fixture-openid","unionid":"fixture-unionid","scope":"snsapi_userinfo"}`))
			return
		}
		_, _ = w.Write([]byte(`{"openid":"fixture-openid","unionid":"fixture-unionid","nickname":"Fixture User"}`))
	}))
	// Close the local provider after all callback requests return, before restoring
	// the shared endpoint hooks through the test's registered cleanup.
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/userinfo"

	for _, frontend := range []string{"https://console.example.com", "https://api.example.com/portal/", ""} {
		for _, flow := range []struct {
			name   string // named OAuth flow
			mode   string // WeChat channel mode
			method string // start HTTP method
		}{
			{name: "login GET", mode: "open", method: http.MethodGet},
			{name: "login POST", mode: "open", method: http.MethodPost},
			{name: "payment", mode: "mp", method: http.MethodGet},
		} {
			t.Run(flow.name+"/"+frontend, func(t *testing.T) {
				values := wechatOAuthTestSettings(flow.mode, "wx-app", "wx-secret", "https://console.example.com/auth/wechat/callback")
				values[identitycore.SettingKeyWeChatConnectRedirectURL] = ""
				values["api_base_url"] = "https://models.example.com/v1"
				values["frontend_url"] = frontend
				handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, values)
				// This flow leaves no asynchronous profile task. Close its SQLite client
				// before the next fixed-DSN scenario creates an independent pending session.
				defer func() { require.NoError(t, client.Close()) }()
				handler.cfg.Security.SecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				handler.cfg.Security.SecretEncryptionKeyConfigured = true
				path := "/api/v1/auth/oauth/wechat"
				if flow.mode == "mp" {
					path += "/payment"
				}
				start, err := url.Parse("https://api.example.com" + path + "/start?mode=open&payment_type=wxpay&amount=12.5")
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(flow.method, start.String(), strings.NewReader(`{}`))
				c.Request.Header.Set("Content-Type", "application/json")
				if flow.mode == "mp" {
					handler.WeChatPaymentOAuthStart(c)
				} else {
					handler.WeChatOAuthStart(c)
				}
				authorizeURL := recorder.Header().Get("Location")
				if flow.method == http.MethodPost {
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					var response struct {
						Data identityhttp.OAuthStartResponse `json:"data"` // authorization response payload
					}
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
					authorizeURL = response.Data.AuthorizeURL
				} else {
					require.Equal(t, http.StatusFound, recorder.Code, recorder.Body.String())
				}
				authorize, err := url.Parse(authorizeURL)
				require.NoError(t, err)
				callback, err := url.Parse(authorize.Query().Get("redirect_uri"))
				require.NoError(t, err)
				require.Equal(t, "https://api.example.com"+path+"/callback", callback.String())
				jar, err := cookiejar.New(nil)
				require.NoError(t, err)
				jar.SetCookies(start, recorder.Result().Cookies())
				query := callback.Query()
				query.Set("state", authorize.Query().Get("state"))
				query.Set("code", "fixture-code")
				callback.RawQuery = query.Encode()
				callbackRecorder := httptest.NewRecorder()
				callbackContext, _ := gin.CreateTestContext(callbackRecorder)
				callbackContext.Request = httptest.NewRequest(http.MethodGet, callback.String(), nil)
				for _, cookie := range jar.Cookies(callback) {
					callbackContext.Request.AddCookie(cookie)
				}
				if flow.mode == "mp" {
					handler.WeChatPaymentOAuthCallback(callbackContext)
				} else {
					handler.WeChatOAuthCallback(callbackContext)
				}
				require.Equal(t, http.StatusFound, callbackRecorder.Code, callbackRecorder.Body.String())
				if flow.mode == "mp" {
					location, err := url.Parse(callbackRecorder.Header().Get("Location"))
					require.NoError(t, err)
					fragment, err := url.ParseQuery(location.Fragment)
					require.NoError(t, err)
					claims, err := handler.paymentResume().ParseWeChatPaymentResumeToken(fragment.Get("wechat_resume_token"))
					require.NoError(t, err)
					require.Equal(t, "fixture-openid", claims.OpenID)
					require.Equal(t, "12.5", claims.Amount)
				} else {
					require.Equal(t, "https://console.example.com/auth/wechat/callback", callbackRecorder.Header().Get("Location"))
					jar.SetCookies(callback, callbackRecorder.Result().Cookies())
					exchange, err := url.Parse("https://api.example.com/api/v1/auth/oauth/pending/exchange")
					require.NoError(t, err)
					require.NotNil(t, findCookie(jar.Cookies(exchange), identityhttp.OauthPendingSessionCookieName))
					require.NotNil(t, findCookie(jar.Cookies(exchange), identityhttp.OauthPendingBrowserCookieName))
				}
			})
		}
	}
}

// TestWeChatOAuthStartRedirectsAndSetsPendingCookies starts open-mode login
// with a local billing redirect. It checks the WeChat authorize host, app and
// scope parameters plus state, redirect, mode and browser cookies.
func TestWeChatOAuthStartRedirectsAndSetsPendingCookies(t *testing.T) {
	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, map[string]string{
		identitycore.SettingKeyWeChatConnectEnabled:             "true",
		identitycore.SettingKeyWeChatConnectAppID:               "wx-open-app",
		identitycore.SettingKeyWeChatConnectAppSecret:           "wx-open-secret",
		identitycore.SettingKeyWeChatConnectMode:                "open",
		identitycore.SettingKeyWeChatConnectScopes:              "snsapi_login",
		identitycore.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identitycore.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/start?mode=open&redirect=/billing", nil)
	c.Request.Host = "api.example.com"

	handler.WeChatOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.NotEmpty(t, location)
	require.Contains(t, location, "open.weixin.qq.com")
	require.Contains(t, location, "appid=wx-open-app")
	require.Contains(t, location, "scope=snsapi_login")

	cookies := recorder.Result().Cookies()
	require.NotEmpty(t, findCookie(cookies, identityhttp.WechatOAuthStateCookieName))
	require.NotEmpty(t, findCookie(cookies, identityhttp.WechatOAuthRedirectCookieName))
	require.NotEmpty(t, findCookie(cookies, identityhttp.WechatOAuthModeCookieName))
	require.NotEmpty(t, findCookie(cookies, identityhttp.OauthPendingBrowserCookieName))
}

// TestWeChatOAuthStart_AllowsOpenModeWhenBothCapabilitiesEnabled requests open
// mode while the default mode is mp and both capabilities are enabled. Start
// selects the QR-connect authorize path and snsapi_login scope.
func TestWeChatOAuthStart_AllowsOpenModeWhenBothCapabilitiesEnabled(t *testing.T) {
	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, map[string]string{
		identitycore.SettingKeyWeChatConnectEnabled:             "true",
		identitycore.SettingKeyWeChatConnectAppID:               "wx-shared-app",
		identitycore.SettingKeyWeChatConnectAppSecret:           "wx-shared-secret",
		identitycore.SettingKeyWeChatConnectMode:                "mp",
		identitycore.SettingKeyWeChatConnectScopes:              "snsapi_base",
		identitycore.SettingKeyWeChatConnectOpenEnabled:         "true",
		identitycore.SettingKeyWeChatConnectMPEnabled:           "true",
		identitycore.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identitycore.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/start?mode=open&redirect=/billing", nil)
	c.Request.Host = "api.example.com"

	handler.WeChatOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.NotEmpty(t, location)
	require.Contains(t, location, "open.weixin.qq.com")
	require.Contains(t, location, "connect/qrconnect")
	require.Contains(t, location, "scope=snsapi_login")
}

// TestWeChatOAuthCallbackCreatesPendingSessionForUnifiedFlow returns matching
// unionid and openid through local provider endpoints. Callback stores the
// canonical union subject, synthetic email, both provider identifiers and
// suggested profile values in pending state.
func TestWeChatOAuthCallbackCreatesPendingSessionForUnifiedFlow(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"WeChat Nick","headimgurl":"https://cdn.example/avatar.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/wechat/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	ctx := context.Background()
	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "wechat", session.ProviderType)
	require.Equal(t, "wechat-main", session.ProviderKey)
	require.Equal(t, "union-456", session.ProviderSubject)
	require.Equal(t, "wechat-union-456@wechat-connect.invalid", session.ResolvedEmail)
	require.Equal(t, "WeChat Nick", session.UpstreamIdentityClaims["suggested_display_name"])
	require.Equal(t, "https://cdn.example/avatar.png", session.UpstreamIdentityClaims["suggested_avatar_url"])
	require.Equal(t, "union-456", session.UpstreamIdentityClaims["unionid"])
	require.Equal(t, "openid-123", session.UpstreamIdentityClaims["openid"])
}

// TestWeChatOAuthCallbackFallsBackToOpenIDWhenUnionIDMissingInSingleChannelMode
// returns a profile containing only openid in open mode. Pending login uses
// that subject and synthetic email, redirects to the configured frontend, and
// requires the third-party signup choice.
func TestWeChatOAuthCallbackFallsBackToOpenIDWhenUnionIDMissingInSingleChannelMode(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","nickname":"WeChat Nick","headimgurl":"https://cdn.example/avatar.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, wechatOAuthTestSettings("open", "wx-open-app", "wx-open-secret", "https://app.example.com/auth/wechat/callback"))
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "https://app.example.com/auth/wechat/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.Equal(t, "openid-123", session.ProviderSubject)
	require.Equal(t, identitycore.WeChatSyntheticEmail("openid-123"), session.ResolvedEmail)

	completion := testassert.MustType[map[string]any](session.LocalFlowState[identityhttp.OauthCompletionResponseKey])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, completion["step"])
	require.Equal(t, "third_party_signup", completion["choice_reason"])
}

// TestWeChatOAuthCallbackCreatesLoginPendingSessionForExistingIdentityUserWithoutStoredTokens
// returns the stored owner's union subject. Pending state targets that account
// and local redirect, while its completion map contains neither access-token
// nor refresh-token fields.
func TestWeChatOAuthCallbackCreatesLoginPendingSessionForExistingIdentityUserWithoutStoredTokens(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"WeChat Display","headimgurl":"https://cdn.example/wechat-login.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, wechatOAuthTestSettings("open", "wx-open-app", "wx-open-secret", "https://app.example.com/auth/wechat/callback"))
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.WeChatSyntheticEmail("union-456")).
		SetUsername("wechat-existing-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("union-456").
		SetMetadata(map[string]any{"username": "wechat-existing-user"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "https://app.example.com/auth/wechat/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, existingUser.ID, *session.TargetUserID)
	require.Equal(t, existingUser.Email, session.ResolvedEmail)

	completion := testassert.MustType[map[string]any](session.LocalFlowState[identityhttp.OauthCompletionResponseKey])
	require.Equal(t, "/dashboard", completion["redirect"])
	_, hasAccessToken := completion["access_token"]
	require.False(t, hasAccessToken)
	_, hasRefreshToken := completion["refresh_token"]
	require.False(t, hasRefreshToken)
}

// TestWeChatOAuthCallbackRejectsDisabledExistingIdentityUser matches a local
// provider profile to a disabled owner. Callback reports USER_NOT_ACTIVE,
// issues no pending-session cookie, and leaves the pending table empty.
func TestWeChatOAuthCallbackRejectsDisabledExistingIdentityUser(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-disabled","unionid":"union-disabled","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-disabled","unionid":"union-disabled","nickname":"Disabled WeChat","headimgurl":"https://cdn.example/disabled.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.WeChatSyntheticEmail("union-disabled")).
		SetUsername("disabled-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("union-disabled").
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-disabled", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-disabled"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-disabled"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "session_error", "USER_NOT_ACTIVE")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// TestWeChatPaymentOAuthCallbackRedirectsWithOpaqueResumeToken exchanges local
// payment OAuth and saved purchase context. The redirect carries a resume token
// and local destination, omits the listed payment fields, and parses back to
// the supplied purchase claims.
func TestWeChatPaymentOAuthCallbackRedirectsWithOpaqueResumeToken(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The fixed payment token reply supplies openid and scope for resume claims.
		// Write errors are discarded while the caller inspects the resulting
		// redirect.
		if strings.Contains(r.URL.Path, "/sns/oauth2/access_token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","scope":"snsapi_base"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"

	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, wechatOAuthTestSettings("mp", "wx-mp-app", "wx-mp-secret", "/auth/wechat/callback"))
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()
	handler.cfg.Security.SecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	handler.cfg.Security.SecretEncryptionKeyConfigured = true

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/payment/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthStateName, "state-123"))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthRedirect, "/purchase?from=wechat"))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthContextName, `{"payment_type":"wxpay","amount":"12.5","order_type":"subscription","plan_id":7}`))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthScope, "snsapi_base"))
	c.Request = req

	handler.WeChatPaymentOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	fragment, err := url.ParseQuery(parsed.Fragment)
	require.NoError(t, err)
	require.Equal(t, "/purchase?from=wechat", fragment.Get("redirect"))
	require.NotEmpty(t, fragment.Get("wechat_resume_token"))
	require.Empty(t, fragment.Get("openid"))
	require.Empty(t, fragment.Get("payment_type"))
	require.Empty(t, fragment.Get("amount"))
	require.Empty(t, fragment.Get("order_type"))
	require.Empty(t, fragment.Get("plan_id"))

	claims, err := handler.paymentResume().ParseWeChatPaymentResumeToken(fragment.Get("wechat_resume_token"))
	require.NoError(t, err)
	require.Equal(t, "openid-123", claims.OpenID)
	require.Equal(t, payment.TypeWxpay, claims.PaymentType)
	require.Equal(t, "12.5", claims.Amount)
	require.Equal(t, payment.OrderTypeSubscription, claims.OrderType)
	require.EqualValues(t, 7, claims.PlanID)
	require.Equal(t, "/purchase?from=wechat", claims.RedirectTo)
}

// TestWeChatPaymentOAuthCallbackUsesExplicitPaymentResumeSigningKeyWhenMixedKeysConfigured
// supplies environment signing text and hexadecimal legacy encryption material.
// The resume token parses under the explicit key with the expected purchase
// claims and fails under a separate ASCII comparison key. The rejection
// assertion uses the first thirty-two hex characters as UTF-8 bytes; it does
// not decode the configured legacy key for that comparison.
func TestWeChatPaymentOAuthCallbackUsesExplicitPaymentResumeSigningKeyWhenMixedKeysConfigured(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The fixed payment token reply supplies openid and scope for resume claims.
		// Write errors are discarded while the caller inspects the resulting
		// redirect.
		if strings.Contains(r.URL.Path, "/sns/oauth2/access_token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-mixed-key","scope":"snsapi_base"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"

	handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, wechatOAuthTestSettings("mp", "wx-mp-app", "wx-mp-secret", "/auth/wechat/callback"))
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	legacyKeyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	explicitSigningKey := "explicit-payment-resume-signing-key"
	handler.cfg.Payment.ResumeSigningKey = explicitSigningKey
	handler.cfg.Security.SecretEncryptionKey = legacyKeyHex
	handler.cfg.Security.SecretEncryptionKeyConfigured = true

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/payment/callback?code=wechat-code&state=state-mixed", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthStateName, "state-mixed"))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthRedirect, "/purchase?from=wechat"))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthContextName, `{"payment_type":"wxpay","amount":"18.8","order_type":"subscription","plan_id":9}`))
	req.AddCookie(encodedCookie(paymenthttp.WechatPaymentOAuthScope, "snsapi_base"))
	c.Request = req

	handler.WeChatPaymentOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	fragment, err := url.ParseQuery(parsed.Fragment)
	require.NoError(t, err)

	token := fragment.Get("wechat_resume_token")
	require.NotEmpty(t, token)

	claims, err := payment.NewPaymentResumeService([]byte(explicitSigningKey)).ParseWeChatPaymentResumeToken(token)
	require.NoError(t, err)
	require.Equal(t, "openid-mixed-key", claims.OpenID)
	require.Equal(t, payment.TypeWxpay, claims.PaymentType)
	require.Equal(t, "18.8", claims.Amount)
	require.Equal(t, payment.OrderTypeSubscription, claims.OrderType)
	require.EqualValues(t, 9, claims.PlanID)
	require.Equal(t, "/purchase?from=wechat", claims.RedirectTo)

	_, err = payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef")).ParseWeChatPaymentResumeToken(token)
	require.Error(t, err)
}

// TestWeChatOAuthCallbackBindUsesUnionCanonicalIdentityAcrossChannels runs
// independent open and mp binding fixtures sequentially. Each pending result
// targets the current user with the same union subject and retains that case's
// app, channel and openid values.
func TestWeChatOAuthCallbackBindUsesUnionCanonicalIdentityAcrossChannels(t *testing.T) {
	testCases := []struct {
		name string // channel scenario label
		// mode, appID, appSecret and openID form one independent channel fixture.
		// Each subtest pairs those app credentials with its own application-scoped
		// openid while sharing the supplied union subject across case definitions.
		mode      string // requested OAuth channel
		appID     string // selected channel application ID
		appSecret string // matching channel client secret
		openID    string // application-scoped provider subject
	}{
		{
			name:      "open",
			mode:      "open",
			appID:     "wx-open-app",
			appSecret: "wx-open-secret",
			openID:    "openid-open-123",
		},
		{
			name:      "mp",
			mode:      "mp",
			appID:     "wx-mp-app",
			appSecret: "wx-mp-secret",
			openID:    "openid-mp-123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Save the package-global endpoint values for cleanup restoration. Deferred
			// database and local-server closes run before this restoration callback.
			originalAccessTokenURL := wechatOAuthAccessTokenURL
			originalUserInfoURL := wechatOAuthUserInfoURL
			t.Cleanup(func() {
				wechatOAuthAccessTokenURL = originalAccessTokenURL
				wechatOAuthUserInfoURL = originalUserInfoURL
			})

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Token and userinfo branches return fixed profile data for this case.
				// Their write errors are discarded while the caller inspects callback
				// results.
				switch {
				case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"` + tc.openID + `","unionid":"union-456","scope":"snsapi_login"}`))
				case strings.Contains(r.URL.Path, "/sns/userinfo"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"openid":"` + tc.openID + `","unionid":"union-456","nickname":"Bind Nick","headimgurl":"https://cdn.example/bind.png"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
			wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

			handler, client := newWeChatOAuthTestHandlerWithSettings(t, false, wechatOAuthTestSettings(tc.mode, tc.appID, tc.appSecret, "/auth/wechat/callback"))
			// Close the client on test return; testing cleanups run afterward, including
			// task-stop callbacks registered by fixture construction.
			defer func() { require.NoError(t, client.Close()) }()

			currentUser, err := client.User.Create().
				SetEmail("current@example.com").
				SetUsername("current-user").
				SetPasswordHash("hash").
				SetRole(identitycore.RoleUser).
				SetStatus(billing.StatusActive).
				Save(context.Background())
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
			req.Host = "api.example.com"
			req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
			req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
			req.AddCookie(encodedCookie(identityhttp.WechatOAuthIntentCookieName, identityhttp.WechatOAuthIntentBind))
			req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, tc.mode))
			req.AddCookie(encodedCookie(identityhttp.WechatOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
			req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
			c.Request = req

			handler.WeChatOAuthCallback(c)

			require.Equal(t, http.StatusFound, recorder.Code)
			require.Equal(t, "/auth/wechat/callback", recorder.Header().Get("Location"))

			sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
			require.NotNil(t, sessionCookie)

			session, err := client.PendingAuthSession.Query().
				Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
				Only(context.Background())
			require.NoError(t, err)
			require.Equal(t, identityhttp.WechatOAuthIntentBind, session.Intent)
			require.NotNil(t, session.TargetUserID)
			require.Equal(t, currentUser.ID, *session.TargetUserID)
			require.Equal(t, currentUser.Email, session.ResolvedEmail)
			require.Equal(t, "union-456", session.ProviderSubject)
			require.Equal(t, "union-456", session.UpstreamIdentityClaims["subject"])
			require.Equal(t, "union-456", session.UpstreamIdentityClaims["unionid"])
			require.Equal(t, tc.openID, session.UpstreamIdentityClaims["openid"])
			require.Equal(t, tc.mode, session.UpstreamIdentityClaims["channel"])
			require.Equal(t, tc.appID, session.UpstreamIdentityClaims["channel_app_id"])
			require.Equal(t, tc.openID, session.UpstreamIdentityClaims["channel_subject"])

			completionResponse := testassert.MustType[map[string]any](session.LocalFlowState[identityhttp.OauthCompletionResponseKey])
			require.Equal(t, "/dashboard", completionResponse["redirect"])
			_, hasAccessToken := completionResponse["access_token"]
			require.False(t, hasAccessToken)
		})
	}
}

// TestWeChatOAuthCallbackBindRejectsCanonicalOwnershipConflict returns a union
// subject owned by another account during current-user binding. Callback
// reports canonical ownership conflict, sets no pending-session cookie, and
// creates no pending row.
func TestWeChatOAuthCallbackBindRejectsCanonicalOwnershipConflict(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"Conflict Nick","headimgurl":"https://cdn.example/conflict.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	owner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	currentUser, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("current").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(owner.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("union-456").
		SetMetadata(map[string]any{"unionid": "union-456"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthIntentCookieName, identityhttp.WechatOAuthIntentBind))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "ownership_conflict", "AUTH_IDENTITY_OWNERSHIP_CONFLICT")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// TestWeChatOAuthCallbackBindRejectsChannelOwnershipConflict supplies an openid
// whose app/channel relation belongs to another identity. Callback reports
// channel ownership conflict, sets no pending-session cookie, and leaves the
// pending table empty.
func TestWeChatOAuthCallbackBindRejectsChannelOwnershipConflict(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"Conflict Nick","headimgurl":"https://cdn.example/conflict.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	owner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	currentUser, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("current").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	ownerIdentity, err := client.AuthIdentity.Create().
		SetUserID(owner.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("union-owner").
		SetMetadata(map[string]any{"unionid": "union-owner"}).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentityChannel.Create().
		SetIdentityID(ownerIdentity.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetChannel("open").
		SetChannelAppID("wx-open-app").
		SetChannelSubject("openid-123").
		SetMetadata(map[string]any{"openid": "openid-123"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthIntentCookieName, identityhttp.WechatOAuthIntentBind))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "ownership_conflict", "AUTH_IDENTITY_CHANNEL_OWNERSHIP_CONFLICT")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// TestWeChatOAuthCallbackBindRejectsLegacyProviderKeyOwnershipConflict seeds
// the returned union subject under the legacy provider key on another account.
// Current-user callback reports ownership conflict, sets no pending-session
// cookie, and leaves the pending table empty.
func TestWeChatOAuthCallbackBindRejectsLegacyProviderKeyOwnershipConflict(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"Conflict Nick","headimgurl":"https://cdn.example/conflict.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	owner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	currentUser, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("current").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(owner.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthLegacyProviderKey).
		SetProviderSubject("union-456").
		SetMetadata(map[string]any{"unionid": "union-456"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthIntentCookieName, identityhttp.WechatOAuthIntentBind))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "ownership_conflict", "AUTH_IDENTITY_OWNERSHIP_CONFLICT")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// TestCompleteWeChatOAuthRegistrationAfterInvitationPendingSessionReturnsPendingSession
// submits an invitation and adoption choices after callback creates
// signup-choice state. Completion returns that pending choice, leaves the row
// unconsumed, and creates no user, identity, channel or adoption decision.
func TestCompleteWeChatOAuthRegistrationAfterInvitationPendingSessionReturnsPendingSession(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"WeChat Display","headimgurl":"https://cdn.example/wechat.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, true)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	redeemRepo := billingpostgres.NewRedeemCodeRepository(client)
	require.NoError(t, redeemRepo.Create(ctx, &billing.RedeemCode{
		Code:   "invite-1",
		Type:   billing.RedeemTypeInvitation,
		Status: billing.StatusUnused,
	}))

	callbackRecorder := httptest.NewRecorder()
	callbackCtx, _ := gin.CreateTestContext(callbackRecorder)
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	callbackReq.Host = "api.example.com"
	callbackReq.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	callbackReq.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	callbackReq.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	callbackReq.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	callbackCtx.Request = callbackReq

	handler.WeChatOAuthCallback(callbackCtx)

	require.Equal(t, http.StatusFound, callbackRecorder.Code)
	require.Equal(t, "/auth/wechat/callback", callbackRecorder.Header().Get("Location"))

	sessionCookie := findCookie(callbackRecorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)
	sessionToken := decodeCookieValueForTest(t, sessionCookie.Value)

	pendingSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(sessionToken)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthPendingChoiceStep, testassert.MustType[map[string]any](pendingSession.LocalFlowState[identityhttp.OauthCompletionResponseKey])["step"])

	body := bytes.NewBufferString(`{"invitation_code":"invite-1","adopt_display_name":true,"adopt_avatar":true}`)
	completeRecorder := httptest.NewRecorder()
	completeCtx, _ := gin.CreateTestContext(completeRecorder)
	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/wechat/complete-registration", body)
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(sessionToken)})
	completeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("browser-123")})
	completeCtx.Request = completeReq

	handler.CompleteWeChatOAuthRegistration(completeCtx)

	require.Equal(t, http.StatusOK, completeRecorder.Code)
	responseData := decodeJSONBody(t, completeRecorder)
	require.Equal(t, "pending_session", responseData["auth_result"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, responseData["step"])
	require.Equal(t, true, responseData["adoption_required"])
	require.Empty(t, responseData["access_token"])

	consumed, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(pendingSession.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, consumed.ConsumedAt)

	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ("wechat-main"),
			authidentity.ProviderSubjectEQ("union-456"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	channelCount, err := client.AuthIdentityChannel.Query().
		Where(
			authidentitychannel.ProviderTypeEQ("wechat"),
			authidentitychannel.ProviderKeyEQ("wechat-main"),
			authidentitychannel.ChannelEQ("open"),
			authidentitychannel.ChannelAppIDEQ("wx-open-app"),
			authidentitychannel.ChannelSubjectEQ("openid-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, channelCount)

	decisionCount, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(pendingSession.ID)).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, decisionCount)
}

// TestCompleteWeChatOAuthRegistrationBindsIdentityWithoutAdoptionFlags
// completes a seeded WeChat registration without profile choices. It issues
// both tokens, creates the supplied username, binds the subject, and links an
// identity decision with both adoption flags false.
func TestCompleteWeChatOAuthRegistrationBindsIdentityWithoutAdoptionFlags(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("wechat-complete-no-adoption-session").
		SetIntent("login").
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("wechat-subject-no-adoption").
		SetResolvedEmail("wechat-subject-no-adoption@wechat-connect.invalid").
		SetBrowserSessionKey("wechat-browser-no-adoption").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "wechat_user",
			"suggested_display_name": "WeChat Legacy",
			"suggested_avatar_url":   "https://cdn.example/wechat-legacy.png",
			"mode":                   "open",
			"channel":                "open",
			"channel_app_id":         "wx-open-app",
			"channel_subject":        "openid-legacy",
		}).
		// Seed a future expiry for the following completion-state request.
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	completeCtx, _ := gin.CreateTestContext(recorder)
	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/wechat/complete-registration", body)
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	completeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("wechat-browser-no-adoption")})
	completeCtx.Request = completeReq

	handler.CompleteWeChatOAuthRegistration(completeCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.NotEmpty(t, responseData["access_token"])
	require.NotEmpty(t, responseData["refresh_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ(session.ResolvedEmail)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "wechat_user", userEntity.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentity.ProviderSubjectEQ("wechat-subject-no-adoption"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)

	decision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, decision.IdentityID)
	require.Equal(t, identity.ID, *decision.IdentityID)
	require.False(t, decision.AdoptDisplayName)
	require.False(t, decision.AdoptAvatar)
}

// TestWeChatOAuthCallbackRepairsLegacyOpenIDOnlyIdentity seeds an existing
// identity under openid before unionid becomes available. Callback targets the
// same owner, changes the same identity to the union subject, removes the old
// subject lookup, and links the app/channel openid.
func TestWeChatOAuthCallbackRepairsLegacyOpenIDOnlyIdentity(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"Legacy WeChat","headimgurl":"https://cdn.example/legacy.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	legacyUser, err := client.User.Create().
		SetEmail("legacy@example.com").
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	legacyIdentity, err := client.AuthIdentity.Create().
		SetUserID(legacyUser.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthProviderKey).
		SetProviderSubject("openid-123").
		SetMetadata(map[string]any{"openid": "openid-123"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/wechat/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, legacyUser.ID, *session.TargetUserID)
	require.Equal(t, legacyUser.Email, session.ResolvedEmail)

	repairedIdentity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentity.ProviderSubjectEQ("union-456"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, legacyIdentity.ID, repairedIdentity.ID)
	require.Equal(t, legacyUser.ID, repairedIdentity.UserID)

	openIDIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentity.ProviderSubjectEQ("openid-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, openIDIdentityCount)

	channel, err := client.AuthIdentityChannel.Query().
		Where(
			authidentitychannel.ProviderTypeEQ("wechat"),
			authidentitychannel.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentitychannel.ChannelEQ("open"),
			authidentitychannel.ChannelAppIDEQ("wx-open-app"),
			authidentitychannel.ChannelSubjectEQ("openid-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, repairedIdentity.ID, channel.IdentityID)
}

// TestCompleteWeChatOAuthRegistrationRejectsAdoptExistingUserSession sends
// registration completion for an existing-account adoption intent. The handler
// returns HTTP 400 and leaves its pending row unconsumed for the required
// ownership-proof flow.
func TestCompleteWeChatOAuthRegistrationRejectsAdoptExistingUserSession(t *testing.T) {
	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("wechat-complete-invalid-session").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("wechat").
		SetProviderKey("wechat-main").
		SetProviderSubject("union-invalid-1").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("wechat-invalid-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "wechat_user",
		}).
		SetLocalFlowState(map[string]any{
			identityhttp.OauthCompletionResponseKey: map[string]any{
				"step": "bind_login_required",
			},
		}).
		// Seed a future expiry for the following completion-state request.
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	completeCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/wechat/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("wechat-invalid-browser")})
	completeCtx.Request = req

	handler.CompleteWeChatOAuthRegistration(completeCtx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

// TestCompleteWeChatOAuthRegistrationReturnsPendingSessionWhenChoiceStillRequired
// submits completion against a forced-email signup choice. It returns
// pending_session and the choice step, creates no user, and leaves the session
// unconsumed.
func TestCompleteWeChatOAuthRegistrationReturnsPendingSessionWhenChoiceStillRequired(t *testing.T) {
	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	session, err := client.PendingAuthSession.Create().
		SetSessionToken("wechat-complete-choice-session").
		SetIntent("login").
		SetProviderType("wechat").
		SetProviderKey("wechat-main").
		SetProviderSubject("wechat-choice-subject-1").
		SetResolvedEmail("wechat-choice-subject-1@wechat-connect.invalid").
		SetBrowserSessionKey("wechat-choice-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "wechat_user",
		}).
		SetLocalFlowState(map[string]any{
			identityhttp.OauthCompletionResponseKey: map[string]any{
				"step":                  identityhttp.OauthPendingChoiceStep,
				"redirect":              "/dashboard",
				"email":                 "fresh@example.com",
				"resolved_email":        "fresh@example.com",
				"force_email_on_signup": true,
			},
		}).
		// Seed a future expiry for the following completion-state request.
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	completeCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/wechat/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("wechat-choice-browser")})
	completeCtx.Request = req

	handler.CompleteWeChatOAuthRegistration(completeCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.Equal(t, "pending_session", responseData["auth_result"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, responseData["step"])
	require.Equal(t, true, responseData["force_email_on_signup"])
	require.Empty(t, responseData["access_token"])

	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

// TestWeChatOAuthCallbackRepairsLegacyProviderKeyCanonicalIdentity seeds a
// union subject under the legacy provider key. Callback retains its identity ID
// and owner while moving lookup to the canonical key and recording the matching
// app/channel relation.
func TestWeChatOAuthCallbackRepairsLegacyProviderKeyCanonicalIdentity(t *testing.T) {
	// Save the package-global endpoint values for cleanup restoration. Deferred
	// database and local-server closes run before this restoration callback.
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token and userinfo branches return fixed profile data for this case. Their
		// write errors are discarded while the caller inspects callback results.
		switch {
		case strings.Contains(r.URL.Path, "/sns/oauth2/access_token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wechat-access","openid":"openid-123","unionid":"union-456","scope":"snsapi_login"}`))
		case strings.Contains(r.URL.Path, "/sns/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openid":"openid-123","unionid":"union-456","nickname":"Legacy Canonical","headimgurl":"https://cdn.example/legacy-canonical.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	wechatOAuthAccessTokenURL = upstream.URL + "/sns/oauth2/access_token"
	wechatOAuthUserInfoURL = upstream.URL + "/sns/userinfo"

	handler, client := newWeChatOAuthTestHandler(t, false)
	// Close the client on test return; testing cleanups run afterward, including
	// task-stop callbacks registered by fixture construction.
	defer func() { require.NoError(t, client.Close()) }()

	ctx := context.Background()
	legacyUser, err := client.User.Create().
		SetEmail("legacy@example.com").
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	legacyIdentity, err := client.AuthIdentity.Create().
		SetUserID(legacyUser.ID).
		SetProviderType("wechat").
		SetProviderKey(identityhttp.WechatOAuthLegacyProviderKey).
		SetProviderSubject("union-456").
		SetMetadata(map[string]any{"unionid": "union-456"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/wechat/callback?code=wechat-code&state=state-123", nil)
	req.Host = "api.example.com"
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthRedirectCookieName, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.WechatOAuthModeCookieName, "open"))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.WeChatOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/wechat/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, legacyUser.ID, *session.TargetUserID)
	require.Equal(t, legacyUser.Email, session.ResolvedEmail)

	repairedIdentity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentity.ProviderSubjectEQ("union-456"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, legacyIdentity.ID, repairedIdentity.ID)
	require.Equal(t, legacyUser.ID, repairedIdentity.UserID)

	legacyIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("wechat"),
			authidentity.ProviderKeyEQ(identityhttp.WechatOAuthLegacyProviderKey),
			authidentity.ProviderSubjectEQ("union-456"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, legacyIdentityCount)

	channel, err := client.AuthIdentityChannel.Query().
		Where(
			authidentitychannel.ProviderTypeEQ("wechat"),
			authidentitychannel.ProviderKeyEQ(identityhttp.WechatOAuthProviderKey),
			authidentitychannel.ChannelEQ("open"),
			authidentitychannel.ChannelAppIDEQ("wx-open-app"),
			authidentitychannel.ChannelSubjectEQ("openid-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, repairedIdentity.ID, channel.IdentityID)
}
