//go:build unit

package identityhttp_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/promotion"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"

	"entgo.io/ent/dialect"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"

	dbuser "github.com/TokenFlux/TokenRouter/ent/user"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	_ "modernc.org/sqlite"
)

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

func TestWeChatOAuthCallbackCreatesPendingSessionForUnifiedFlow(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackFallsBackToOpenIDWhenUnionIDMissingInSingleChannelMode(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackCreatesLoginPendingSessionForExistingIdentityUserWithoutStoredTokens(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackRejectsDisabledExistingIdentityUser(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatPaymentOAuthCallbackRedirectsWithOpaqueResumeToken(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer func() { require.NoError(t, client.Close()) }()
	handler.cfg.Totp.EncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	handler.cfg.Totp.EncryptionKeyConfigured = true

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

func TestWeChatPaymentOAuthCallbackUsesExplicitPaymentResumeSigningKeyWhenMixedKeysConfigured(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer func() { require.NoError(t, client.Close()) }()

	legacyKeyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	explicitSigningKey := "explicit-payment-resume-signing-key"
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", explicitSigningKey)
	handler.cfg.Totp.EncryptionKey = legacyKeyHex
	handler.cfg.Totp.EncryptionKeyConfigured = true

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

func TestWeChatOAuthCallbackBindUsesUnionCanonicalIdentityAcrossChannels(t *testing.T) {
	testCases := []struct {
		name      string
		mode      string
		appID     string
		appSecret string
		openID    string
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
			originalAccessTokenURL := wechatOAuthAccessTokenURL
			originalUserInfoURL := wechatOAuthUserInfoURL
			t.Cleanup(func() {
				wechatOAuthAccessTokenURL = originalAccessTokenURL
				wechatOAuthUserInfoURL = originalUserInfoURL
			})

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackBindRejectsCanonicalOwnershipConflict(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackBindRejectsChannelOwnershipConflict(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackBindRejectsLegacyProviderKeyOwnershipConflict(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestCompleteWeChatOAuthRegistrationAfterInvitationPendingSessionReturnsPendingSession(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWeChatOAuthCallbackRepairsLegacyOpenIDOnlyIdentity(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestCompleteWeChatOAuthRegistrationRejectsAdoptExistingUserSession(t *testing.T) {
	handler, client := newWeChatOAuthTestHandler(t, false)
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
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"step": "bind_login_required",
		},
		}).
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

func TestCompleteWeChatOAuthRegistrationReturnsPendingSessionWhenChoiceStillRequired(t *testing.T) {
	handler, client := newWeChatOAuthTestHandler(t, false)
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
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"step":                  identityhttp.OauthPendingChoiceStep,
			"redirect":              "/dashboard",
			"email":                 "fresh@example.com",
			"resolved_email":        "fresh@example.com",
			"force_email_on_signup": true,
		},
		}).
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

func TestWeChatOAuthCallbackRepairsLegacyProviderKeyCanonicalIdentity(t *testing.T) {
	originalAccessTokenURL := wechatOAuthAccessTokenURL
	originalUserInfoURL := wechatOAuthUserInfoURL
	t.Cleanup(func() {
		wechatOAuthAccessTokenURL = originalAccessTokenURL
		wechatOAuthUserInfoURL = originalUserInfoURL
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func newWeChatOAuthTestHandler(t *testing.T, invitationEnabled bool) (*authHTTPFixture, *dbent.Client) {
	return newWeChatOAuthTestHandlerWithSettings(t, invitationEnabled, nil)
}

func wechatOAuthTestSettings(mode, appID, secret, frontendRedirect string) map[string]string {
	return map[string]string{
		identitycore.SettingKeyWeChatConnectEnabled:             "true",
		identitycore.SettingKeyWeChatConnectAppID:               appID,
		identitycore.SettingKeyWeChatConnectAppSecret:           secret,
		identitycore.SettingKeyWeChatConnectMode:                mode,
		identitycore.SettingKeyWeChatConnectScopes:              identitycore.SettingsDefaultWeChatConnectScopesForMode(mode),
		identitycore.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identitycore.SettingKeyWeChatConnectFrontendRedirectURL: frontendRedirect,
	}
}

func newWeChatOAuthTestHandlerWithSettings(t *testing.T, invitationEnabled bool, extraSettings map[string]string) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:auth_wechat_oauth?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))

	userRepo := &oauthPendingFlowUserRepo{client: client}
	redeemRepo := billingpostgres.NewRedeemCodeRepository(client)
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		Default: config.DefaultConfig{
			UserBalance:     0,
			UserConcurrency: 1,
		},
	}
	values := map[string]string{
		identitycore.SettingKeyRegistrationEnabled: "true",
		promotion.SettingKeyInvitationCodeEnabled:  boolSettingValue(invitationEnabled),
	}
	for key, value := range wechatOAuthTestSettings("open", "wx-open-app", "wx-open-secret", "/auth/wechat/callback") {
		values[key] = value
	}
	for key, value := range extraSettings {
		values[key] = value
	}
	settingSvc := newAuthSettingsFixture(&wechatOAuthSettingRepoStub{values: values}, cfg)

	authSvc := identitytestkit.Auth(
		client, &identitycore.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: &wechatOAuthRefreshTokenCacheStub{}, Options: identitytestkit.AuthOptions(cfg), Settings: authContractSettings(settingSvc)},
	)

	return newAuthHTTPFixture(t, &authHTTPFixture{
		authDB:      client,
		authService: authSvc,
		settingSvc:  settingSvc,
		cfg:         cfg,
	}), client
}

type wechatOAuthSettingRepoStub struct {
	values map[string]string
}

func (s *wechatOAuthSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

func (s *wechatOAuthSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

func (s *wechatOAuthSettingRepoStub) Set(context.Context, string, string) error {
	return nil
}

func (s *wechatOAuthSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *wechatOAuthSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (s *wechatOAuthSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	for key, value := range s.values {
		result[key] = value
	}
	return result, nil
}

func (s *wechatOAuthSettingRepoStub) Delete(context.Context, string) error {
	return nil
}

type wechatOAuthRefreshTokenCacheStub struct{}

func (s *wechatOAuthRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

func (s *wechatOAuthRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *wechatOAuthRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

func (s *wechatOAuthRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *wechatOAuthRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

// ConsumeRefreshToken 与此桩始终未找到凭据的读取行为一致。
func (s *wechatOAuthRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}
