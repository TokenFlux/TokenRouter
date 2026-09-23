package identityhttp_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"

	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLinuxDoOAuthBindStartRedirectsAndSetsBindCookies(t *testing.T) {
	handler := newLinuxDoOAuthTestHandler(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        "https://connect.linux.do/oauth/authorize",
		TokenURL:            "https://connect.linux.do/oauth/token",
		UserInfoURL:         "https://connect.linux.do/api/user",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/bind/start?intent=bind_current_user&redirect=/settings/connections", nil)
	c.Request = req
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 42})

	handler.LinuxDoOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "connect.linux.do/oauth/authorize")
	require.Contains(t, location, "client_id=linuxdo-client")
	require.Contains(t, location, "code_challenge=")

	cookies := recorder.Result().Cookies()
	require.NotNil(t, findCookie(cookies, identityhttp.LinuxDoOAuthStateCookieName))
	require.NotNil(t, findCookie(cookies, identityhttp.LinuxDoOAuthRedirectCookie))
	require.NotNil(t, findCookie(cookies, identityhttp.LinuxDoOAuthVerifierCookie))
	require.NotNil(t, findCookie(cookies, identityhttp.OauthPendingBrowserCookieName))

	intentCookie := findCookie(cookies, identityhttp.LinuxDoOAuthIntentCookieName)
	require.NotNil(t, intentCookie)
	require.Equal(t, identityhttp.OauthIntentBindCurrentUser, decodeCookieValueForTest(t, intentCookie.Value))

	bindCookie := findCookie(cookies, identityhttp.LinuxDoOAuthBindUserCookieName)
	require.NotNil(t, bindCookie)
	userID, err := identitycore.ParseOAuthBindUserCookieValue(decodeCookieValueForTest(t, bindCookie.Value), "test-secret")
	require.NoError(t, err)
	require.Equal(t, int64(42), userID)
}

func TestLinuxDoOAuthStartOmitsPKCEWhenDisabled(t *testing.T) {
	handler := newLinuxDoOAuthTestHandler(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        "https://connect.linux.do/oauth/authorize",
		TokenURL:            "https://connect.linux.do/oauth/token",
		UserInfoURL:         "https://connect.linux.do/api/user",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             false,
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/start?redirect=/dashboard", nil)

	handler.LinuxDoOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.NotContains(t, recorder.Header().Get("Location"), "code_challenge=")
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.LinuxDoOAuthVerifierCookie))
}

func TestLinuxDoOAuthCallbackAllowsMissingVerifierWhenPKCEDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.NoError(t, r.ParseForm())
			require.Empty(t, r.PostForm.Get("code_verifier"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"compat-subject","username":"linuxdo_user","name":"LinuxDo Display"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             false,
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=linuxdo-code&state=state-123", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "/auth/linuxdo/callback#")
	require.Contains(t, location, "access_token=")
	requireCookieCleared(t, recorder, identityhttp.OauthPendingSessionCookieName)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("compat-subject"),
		).
		Only(context.Background())
	require.NoError(t, err)
	require.Positive(t, identity.UserID)
}

func TestLinuxDoOAuthBindStartAcceptsAccessTokenCookie(t *testing.T) {
	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        "https://connect.linux.do/oauth/authorize",
		TokenURL:            "https://connect.linux.do/oauth/token",
		UserInfoURL:         "https://connect.linux.do/api/user",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("bind-cookie@example.com").
		SetUsername("bind-cookie-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(context.Background())
	require.NoError(t, err)

	token, err := handler.authService.GenerateToken(context.Background(), &identitycore.User{
		ID:           user.ID,
		Email:        user.Email,
		Username:     user.Username,
		PasswordHash: user.PasswordHash,
		Role:         user.Role,
		Status:       user.Status,
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/start?intent=bind_current_user&redirect=/settings/connections", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthBindAccessTokenCookieName, Value: token, Path: identityhttp.OauthBindAccessTokenCookiePath})
	c.Request = req

	handler.LinuxDoOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)

	bindCookie := findCookie(recorder.Result().Cookies(), identityhttp.LinuxDoOAuthBindUserCookieName)
	require.NotNil(t, bindCookie)
	userID, err := identitycore.ParseOAuthBindUserCookieValue(decodeCookieValueForTest(t, bindCookie.Value), "test-secret")
	require.NoError(t, err)
	require.Equal(t, user.ID, userID)

	accessTokenCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthBindAccessTokenCookieName)
	require.NotNil(t, accessTokenCookie)
	require.Equal(t, -1, accessTokenCookie.MaxAge)
}

func TestPrepareOAuthBindAccessTokenCookieSetsHttpOnlyCookie(t *testing.T) {
	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/bind-token", nil)
	req.Header.Set("Authorization", "Bearer access-token-value")
	c.Request = req

	handler.PrepareOAuthBindAccessTokenCookie(c)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	accessTokenCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthBindAccessTokenCookieName)
	require.NotNil(t, accessTokenCookie)
	require.Equal(t, identityhttp.OauthBindAccessTokenCookiePath, accessTokenCookie.Path)
	require.Equal(t, identityhttp.LinuxDoOAuthCookieMaxAgeSec, accessTokenCookie.MaxAge)
	require.True(t, accessTokenCookie.HttpOnly)
	require.Equal(t, url.QueryEscape("access-token-value"), accessTokenCookie.Value)
}

func TestLinuxDoOAuthCallbackCreatesLoginPendingSessionForExistingIdentityUser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"321","username":"linuxdo_user","name":"LinuxDo Display","avatar_url":"https://cdn.example/linuxdo.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.OAuthLinuxDoSyntheticEmail("321")).
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("321").
		SetMetadata(map[string]any{"username": "legacy-user"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-123&state=state-123", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-123"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/linuxdo/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, existingUser.ID, *session.TargetUserID)
	require.Equal(t, identitycore.OAuthLinuxDoSyntheticEmail("321"), session.ResolvedEmail)
	require.Equal(t, "LinuxDo Display", session.UpstreamIdentityClaims["suggested_display_name"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/dashboard", completion["redirect"])
	_, hasAccessToken := completion["access_token"]
	require.False(t, hasAccessToken)
	_, hasRefreshToken := completion["refresh_token"]
	require.False(t, hasRefreshToken)
	require.Nil(t, completion["error"])
}

func TestLinuxDoOAuthCallbackRejectsDisabledExistingIdentityUser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"654","username":"linuxdo_disabled","name":"LinuxDo Disabled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.OAuthLinuxDoSyntheticEmail("654")).
		SetUsername("disabled-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("654").
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-disabled&state=state-disabled", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-disabled"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-disabled"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-disabled"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "session_error", "USER_NOT_ACTIVE")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestLinuxDoOAuthCallbackCreatesBindPendingSessionForCompatEmailUser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"321","email":"legacy@example.com","username":"linuxdo_user","name":"LinuxDo Display","avatar_url":"https://cdn.example/linuxdo.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(" Legacy@Example.com ").
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-compat&state=state-compat", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-compat"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-compat"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-compat"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/linuxdo/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, existingUser.ID, *session.TargetUserID)
	require.Equal(t, strings.TrimSpace(existingUser.Email), session.ResolvedEmail)
	require.Equal(t, "legacy@example.com", session.UpstreamIdentityClaims["compat_email"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/dashboard", completion["redirect"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, completion["step"])
	require.Equal(t, strings.TrimSpace(existingUser.Email), completion["email"])
	require.Equal(t, strings.TrimSpace(existingUser.Email), completion["existing_account_email"])
	require.Equal(t, true, completion["existing_account_bindable"])
	require.Equal(t, "compat_email_match", completion["choice_reason"])
	_, hasAccessToken := completion["access_token"]
	require.False(t, hasAccessToken)
}

func TestLinuxDoOAuthCallbackCreatesChoicePendingSessionWhenSignupRequiresInvite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"654","username":"linuxdo_invite","name":"Need Invite","avatar_url":"https://cdn.example/invite.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, true, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-456&state=state-456", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-456"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-456"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-456"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/linuxdo/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	ctx := context.Background()
	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.Nil(t, session.TargetUserID)

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, identityhttp.OauthPendingChoiceStep, completion["step"])
	require.Equal(t, "/dashboard", completion["redirect"])
	require.Equal(t, "third_party_signup", completion["choice_reason"])
}

func TestLinuxDoOAuthCallbackEmailVerificationCompletesWithBoundEmail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"email-verify-123","username":"linuxdo_email","name":"Email Verify","avatar_url":"https://cdn.example/email.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClientWithEmailVerification(t, false, "fresh@example.com", "246810", config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-email&state=state-email", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-email"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-email"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-email"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/linuxdo/callback", recorder.Header().Get("Location"))
	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	ctx := context.Background()
	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.Nil(t, session.TargetUserID)
	require.Empty(t, session.ResolvedEmail)
	require.Equal(t, "linuxdo-email-verify-123@linuxdo-connect.invalid", session.UpstreamIdentityClaims["email"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "create_account_required", completion["step"])
	require.Equal(t, true, completion["email_binding_required"])
	require.Equal(t, true, completion["force_email_on_signup"])
	require.Equal(t, "email_verification_required", completion["choice_reason"])
	require.NotContains(t, completion, "email")
	require.NotContains(t, completion, "resolved_email")

	createRecorder := httptest.NewRecorder()
	createCtx, _ := gin.CreateTestContext(createRecorder)
	body := bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/create-account", body)
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(sessionCookie)
	createReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("browser-email")})
	createCtx.Request = createReq

	handler.CreatePendingOAuthAccount(createCtx)

	require.Equal(t, http.StatusOK, createRecorder.Code)
	responseData := decodeJSONBody(t, createRecorder)
	require.NotEmpty(t, responseData["access_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ("fresh@example.com")).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "linuxdo", userEntity.SignupSource)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("email-verify-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestLinuxDoOAuthCallbackDirectlyLogsInNewUserWhenEmailVerificationDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"direct-123","username":"linuxdo_direct","name":"Direct Login","avatar_url":"https://cdn.example/direct.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-direct&state=state-direct", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-direct"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-direct"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-direct"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "/auth/linuxdo/callback#")
	require.Contains(t, location, "access_token=")
	require.Contains(t, location, "refresh_token=")
	fragmentValues := parseOAuthRedirectFragment(t, location)
	require.Equal(t, "/dashboard", fragmentValues.Get("redirect"))
	requireCookieCleared(t, recorder, identityhttp.OauthPendingSessionCookieName)
	requireCookieCleared(t, recorder, identityhttp.OauthPendingBrowserCookieName)

	ctx := context.Background()
	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ("linuxdo-direct-123@linuxdo-connect.invalid")).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "linuxdo_direct", userEntity.Username)
	require.Equal(t, "linuxdo", userEntity.SignupSource)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("direct-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)
	require.Equal(t, "https://cdn.example/direct.png", identity.Metadata["suggested_avatar_url"])

	sessionCount, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, sessionCount)
}

func TestLinuxDoOAuthCallbackCreatesBindPendingSessionForCurrentUser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"linuxdo-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"999","username":"bind_user","name":"Bind Display","avatar_url":"https://cdn.example/bind.png"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newLinuxDoOauthHTTPFixtureAndClient(t, false, config.LinuxDoConnectConfig{
		Enabled:             true,
		ClientID:            "linuxdo-client",
		ClientSecret:        "linuxdo-secret",
		AuthorizeURL:        upstream.URL + "/authorize",
		TokenURL:            upstream.URL + "/token",
		UserInfoURL:         upstream.URL + "/userinfo",
		Scopes:              "read",
		RedirectURL:         "https://api.example.com/api/v1/auth/oauth/linuxdo/callback",
		FrontendRedirectURL: "/auth/linuxdo/callback",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	currentUser, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("current-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/linuxdo/callback?code=code-bind&state=state-bind", nil)
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthStateCookieName, "state-bind"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthRedirectCookie, "/settings/connections"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthVerifierCookie, "verifier-bind"))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthIntentCookieName, identityhttp.OauthIntentBindCurrentUser))
	req.AddCookie(encodedCookie(identityhttp.LinuxDoOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-bind"))
	c.Request = req

	handler.LinuxDoOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/linuxdo/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentBindCurrentUser, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, currentUser.ID, *session.TargetUserID)
	require.Equal(t, identitycore.OAuthLinuxDoSyntheticEmail("999"), session.ResolvedEmail)

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/settings/connections", completion["redirect"])
	require.Empty(t, completion["access_token"])
	require.Equal(t, "Bind Display", session.UpstreamIdentityClaims["suggested_display_name"])

	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
}

func TestCompleteLinuxDoOAuthRegistrationAppliesPendingAdoptionDecision(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("linuxdo-complete-session").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-subject-1").
		SetResolvedEmail("linuxdo-subject-1@linuxdo-connect.invalid").
		SetBrowserSessionKey("linuxdo-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "linuxdo_user",
			"suggested_display_name": "LinuxDo Display",
			"suggested_avatar_url":   "https://cdn.example/linuxdo.png",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	_, err = identitypostgres.NewAuthPendingIdentityService(client).UpsertAdoptionDecision(ctx, identitycore.PendingIdentityAdoptionDecisionInput{
		PendingAuthSessionID: session.ID,
		AdoptAvatar:          true,
	})
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1","adopt_display_name":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/linuxdo/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-browser")})
	c.Request = req

	handler.CompleteLinuxDoOAuthRegistration(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.NotEmpty(t, responseData["access_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ(session.ResolvedEmail)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "LinuxDo Display", userEntity.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("linuxdo-subject-1"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)
	require.Equal(t, "LinuxDo Display", identity.Metadata["display_name"])
	require.Equal(t, "https://cdn.example/linuxdo.png", identity.Metadata["avatar_url"])

	decision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, decision.IdentityID)
	require.Equal(t, identity.ID, *decision.IdentityID)
	require.True(t, decision.AdoptDisplayName)
	require.True(t, decision.AdoptAvatar)

	consumed, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
}

func TestCompleteLinuxDoOAuthRegistrationRejectsAdoptExistingUserSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
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
		SetSessionToken("linuxdo-complete-invalid-session").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-invalid-subject-1").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("linuxdo-invalid-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "linuxdo_user",
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
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/linuxdo/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-invalid-browser")})
	c.Request = req

	handler.CompleteLinuxDoOAuthRegistration(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestCompleteLinuxDoOAuthRegistrationReturnsPendingSessionWhenChoiceStillRequired(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("linuxdo-complete-choice-session").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-choice-subject-1").
		SetResolvedEmail("linuxdo-choice-subject-1@linuxdo-connect.invalid").
		SetBrowserSessionKey("linuxdo-choice-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "linuxdo_user",
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
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/linuxdo/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-choice-browser")})
	c.Request = req

	handler.CompleteLinuxDoOAuthRegistration(c)

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

func TestCompleteLinuxDoOAuthRegistrationBindsIdentityWithoutAdoptionFlags(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("linuxdo-complete-no-adoption-session").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-subject-no-adoption").
		SetResolvedEmail("linuxdo-subject-no-adoption@linuxdo-connect.invalid").
		SetBrowserSessionKey("linuxdo-browser-no-adoption").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "linuxdo_user",
			"suggested_display_name": "LinuxDo Legacy",
			"suggested_avatar_url":   "https://cdn.example/linuxdo-legacy.png",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/linuxdo/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-browser-no-adoption")})
	c.Request = req

	handler.CompleteLinuxDoOAuthRegistration(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.NotEmpty(t, responseData["access_token"])
	require.NotEmpty(t, responseData["refresh_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ(session.ResolvedEmail)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "linuxdo_user", userEntity.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("linuxdo-subject-no-adoption"),
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

func TestCompleteLinuxDoOAuthRegistrationRejectsIdentityOwnershipConflictBeforeUserCreation(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	existingOwner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(existingOwner.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-conflict-subject").
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("linuxdo-complete-conflict-session").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("linuxdo-conflict-subject").
		SetResolvedEmail("linuxdo-conflict-subject@linuxdo-connect.invalid").
		SetBrowserSessionKey("linuxdo-conflict-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "linuxdo_user",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/linuxdo/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-conflict-browser")})
	c.Request = req

	handler.CompleteLinuxDoOAuthRegistration(c)

	require.Equal(t, http.StatusConflict, recorder.Code)
	payload := decodeJSONBody(t, recorder)
	require.Equal(t, "AUTH_IDENTITY_OWNERSHIP_CONFLICT", payload["reason"])

	userCount, err := client.User.Query().
		Where(dbuser.EmailEQ("linuxdo-conflict-subject@linuxdo-connect.invalid")).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func newLinuxDoOAuthTestHandler(t *testing.T, invitationEnabled bool, oauthCfg config.LinuxDoConnectConfig) *authHTTPFixture {
	t.Helper()
	handler, _ := newLinuxDoOauthHTTPFixtureAndClient(t, invitationEnabled, oauthCfg)
	return handler
}

func newLinuxDoOauthHTTPFixtureAndClient(t *testing.T, invitationEnabled bool, oauthCfg config.LinuxDoConnectConfig) (*authHTTPFixture, *dbent.Client) {
	t.Helper()
	handler, client := newOAuthPendingFlowTestHandler(t, invitationEnabled)
	configureLinuxDoOAuthTestHandler(t, handler, oauthCfg)
	return handler, client
}

func newLinuxDoOauthHTTPFixtureAndClientWithEmailVerification(
	t *testing.T,
	invitationEnabled bool,
	email string,
	code string,
	oauthCfg config.LinuxDoConnectConfig,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()
	handler, client := newOAuthPendingFlowTestHandlerWithEmailVerification(t, invitationEnabled, email, code)
	configureLinuxDoOAuthTestHandler(t, handler, oauthCfg)
	return handler, client
}

func configureLinuxDoOAuthTestHandler(t *testing.T, handler *authHTTPFixture, oauthCfg config.LinuxDoConnectConfig) {
	handler.settingSvc = nil
	handler.cfg = &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		LinuxDo: oauthCfg,
	}
	bindAuthHTTPFixture(t, handler)
}
