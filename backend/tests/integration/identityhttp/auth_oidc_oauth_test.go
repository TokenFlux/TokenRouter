package identityhttp_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/provider"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"

	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func buildRSAJWK(kid string, pub *rsa.PublicKey) provider.OidcJWK {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return provider.OidcJWK{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   n,
		E:   e,
	}
}

func TestOIDCOAuthBindStartRedirectsAndSetsBindCookies(t *testing.T) {
	handler := newOIDCOAuthTestHandler(t, false, config.OIDCConnectConfig{
		Enabled:              true,
		ClientID:             "oidc-client",
		ClientSecret:         "oidc-secret",
		IssuerURL:            "https://issuer.example.com",
		AuthorizeURL:         "https://issuer.example.com/oauth/authorize",
		TokenURL:             "https://issuer.example.com/oauth/token",
		UserInfoURL:          "https://issuer.example.com/oauth/userinfo",
		JWKSURL:              "https://issuer.example.com/oauth/jwks",
		Scopes:               "openid profile email",
		RedirectURL:          "https://api.example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL:  "/auth/oidc/callback",
		TokenAuthMethod:      "client_secret_post",
		UsePKCE:              true,
		ValidateIDToken:      true,
		AllowedSigningAlgs:   "RS256",
		ClockSkewSeconds:     120,
		RequireEmailVerified: false,
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/bind/start?intent=bind_current_user&redirect=/settings/connections", nil)
	c.Request = req
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 84})

	handler.OIDCOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "issuer.example.com/oauth/authorize")
	require.Contains(t, location, "client_id=oidc-client")
	require.Contains(t, location, "nonce=")

	cookies := recorder.Result().Cookies()
	require.NotNil(t, findCookie(cookies, identityhttp.OidcOAuthStateCookieName))
	require.NotNil(t, findCookie(cookies, identityhttp.OidcOAuthRedirectCookie))
	require.NotNil(t, findCookie(cookies, identityhttp.OidcOAuthVerifierCookie))
	require.NotNil(t, findCookie(cookies, identityhttp.OidcOAuthNonceCookie))
	require.NotNil(t, findCookie(cookies, identityhttp.OauthPendingBrowserCookieName))

	intentCookie := findCookie(cookies, identityhttp.OidcOAuthIntentCookieName)
	require.NotNil(t, intentCookie)
	require.Equal(t, identityhttp.OauthIntentBindCurrentUser, decodeCookieValueForTest(t, intentCookie.Value))

	bindCookie := findCookie(cookies, identityhttp.OidcOAuthBindUserCookieName)
	require.NotNil(t, bindCookie)
	userID, err := identitycore.ParseOAuthBindUserCookieValue(decodeCookieValueForTest(t, bindCookie.Value), "test-secret")
	require.NoError(t, err)
	require.Equal(t, int64(84), userID)
}

func TestOIDCOAuthStartOmitsPKCEAndNonceWhenDisabled(t *testing.T) {
	handler := newOIDCOAuthTestHandler(t, false, config.OIDCConnectConfig{
		Enabled:              true,
		ClientID:             "oidc-client",
		ClientSecret:         "oidc-secret",
		IssuerURL:            "https://issuer.example.com",
		AuthorizeURL:         "https://issuer.example.com/oauth/authorize",
		TokenURL:             "https://issuer.example.com/oauth/token",
		UserInfoURL:          "https://issuer.example.com/oauth/userinfo",
		Scopes:               "openid profile email",
		RedirectURL:          "https://api.example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL:  "/auth/oidc/callback",
		TokenAuthMethod:      "client_secret_post",
		UsePKCE:              false,
		ValidateIDToken:      false,
		RequireEmailVerified: false,
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/start?redirect=/dashboard", nil)

	handler.OIDCOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.NotContains(t, location, "code_challenge=")
	require.NotContains(t, location, "nonce=")
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OidcOAuthVerifierCookie))
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OidcOAuthNonceCookie))
}

func TestOIDCOAuthCallbackAllowsOptionalPKCEAndIDTokenValidation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.NoError(t, r.ParseForm())
			require.Empty(t, r.PostForm.Get("code_verifier"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oidc-access","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sub":"oidc-subject-compat","preferred_username":"oidc_user","name":"OIDC Display","email":"oidc@example.com"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, config.OIDCConnectConfig{
		Enabled:              true,
		ClientID:             "oidc-client",
		ClientSecret:         "oidc-secret",
		IssuerURL:            "https://issuer.example.com",
		AuthorizeURL:         upstream.URL + "/authorize",
		TokenURL:             upstream.URL + "/token",
		UserInfoURL:          upstream.URL + "/userinfo",
		Scopes:               "openid profile email",
		RedirectURL:          "https://api.example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL:  "/auth/oidc/callback",
		TokenAuthMethod:      "client_secret_post",
		UsePKCE:              false,
		ValidateIDToken:      false,
		RequireEmailVerified: false,
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-123", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback", recorder.Header().Get("Location"))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
}

func TestOIDCOAuthCallbackCreatesLoginPendingSessionForExistingIdentityUser(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-subject-login",
		PreferredUsername: "oidc_login",
		DisplayName:       "OIDC Login Display",
		AvatarURL:         "https://cdn.example/oidc-login.png",
		Email:             "oidc-login@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, cfg)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.OIDCSyntheticEmailFromIdentityKey(identitycore.OIDCIdentityKey(cfg.IssuerURL, "oidc-subject-login"))).
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("oidc").
		SetProviderKey(cfg.IssuerURL).
		SetProviderSubject("oidc-subject-login").
		SetMetadata(map[string]any{"username": "legacy-user"}).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-123", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-123"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-123"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-subject-login"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-123"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, existingUser.ID, *session.TargetUserID)
	require.Equal(t, cfg.IssuerURL, session.ProviderKey)
	require.Equal(t, "OIDC Login Display", session.UpstreamIdentityClaims["suggested_display_name"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/dashboard", completion["redirect"])
	_, hasAccessToken := completion["access_token"]
	require.False(t, hasAccessToken)
	_, hasRefreshToken := completion["refresh_token"]
	require.False(t, hasRefreshToken)
	require.Nil(t, completion["error"])
}

func TestOIDCOAuthCallbackRejectsDisabledExistingIdentityUser(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-disabled-subject",
		PreferredUsername: "oidc_disabled",
		DisplayName:       "OIDC Disabled",
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, cfg)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail(identitycore.OIDCSyntheticEmailFromIdentityKey(identitycore.OIDCIdentityKey(cfg.IssuerURL, "oidc-disabled-subject"))).
		SetUsername("disabled-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(existingUser.ID).
		SetProviderType("oidc").
		SetProviderKey(cfg.IssuerURL).
		SetProviderSubject("oidc-disabled-subject").
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-disabled", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-disabled"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-disabled"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-disabled-subject"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-disabled"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "session_error", "USER_NOT_ACTIVE")

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestOIDCOAuthCallbackCreatesBindPendingSessionForCompatEmailUser(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-subject-compat",
		PreferredUsername: "oidc_compat",
		DisplayName:       "OIDC Compat Display",
		AvatarURL:         "https://cdn.example/oidc-compat.png",
		Email:             "legacy@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, cfg)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	existingUser, err := client.User.Create().
		SetEmail("legacy@example.com").
		SetUsername("legacy-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-compat", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-subject-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-compat"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback", recorder.Header().Get("Location"))

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
	require.Equal(t, "legacy@example.com", session.UpstreamIdentityClaims["compat_email"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/dashboard", completion["redirect"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, completion["step"])
	require.Equal(t, existingUser.Email, completion["email"])
	require.Equal(t, existingUser.Email, completion["existing_account_email"])
	require.Equal(t, true, completion["existing_account_bindable"])
	require.Equal(t, "compat_email_match", completion["choice_reason"])
	_, hasAccessToken := completion["access_token"]
	require.False(t, hasAccessToken)
}

func TestOIDCOAuthCallbackAllowsCompatEmailBindWhenUpstreamEmailIsUnverified(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-subject-unverified-compat",
		PreferredUsername: "oidc_unverified",
		DisplayName:       "OIDC Unverified Compat Display",
		AvatarURL:         "https://cdn.example/oidc-unverified.png",
		Email:             "owner@example.com",
		EmailVerified:     false,
	})
	defer cleanup()
	cfg.RequireEmailVerified = true

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, cfg)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-unverified-compat", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-unverified-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/settings/connections"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-unverified-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-subject-unverified-compat"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-unverified-compat"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback#error=email_not_verified&error_message=email+is+not+verified", recorder.Header().Get("Location"))
	require.Nil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))

	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestOIDCOAuthCallbackCreatesChoicePendingSessionWhenSignupRequiresInvite(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-subject-invite",
		PreferredUsername: "oidc_invite",
		DisplayName:       "OIDC Invite Display",
		AvatarURL:         "https://cdn.example/oidc-invite.png",
		Email:             "oidc-invite@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, true, cfg)
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-456", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-456"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-456"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-subject-invite"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-456"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback", recorder.Header().Get("Location"))

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

func TestOIDCOAuthCallbackCreatesBindPendingSessionForCurrentUser(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-subject-bind",
		PreferredUsername: "oidc_bind",
		DisplayName:       "OIDC Bind Display",
		AvatarURL:         "https://cdn.example/oidc-bind.png",
		Email:             "oidc-bind@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClient(t, false, cfg)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-bind", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-bind"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/settings/connections"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-bind"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-subject-bind"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentBindCurrentUser))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthBindUserCookieName, buildEncodedOAuthBindUserCookie(t, currentUser.ID, "test-secret")))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-bind"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/auth/oidc/callback", recorder.Header().Get("Location"))

	sessionCookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, sessionCookie)

	session, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(decodeCookieValueForTest(t, sessionCookie.Value))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentBindCurrentUser, session.Intent)
	require.NotNil(t, session.TargetUserID)
	require.Equal(t, currentUser.ID, *session.TargetUserID)
	require.Equal(t, cfg.IssuerURL, session.ProviderKey)
	require.Equal(t, "OIDC Bind Display", session.UpstreamIdentityClaims["suggested_display_name"])

	completion, ok := session.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/settings/connections", completion["redirect"])
	require.Empty(t, completion["access_token"])

	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
}

func TestCompleteOIDCOAuthRegistrationAppliesPendingAdoptionDecision(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("oidc-complete-session").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-subject-1").
		SetResolvedEmail("93a310f4c1944c5bbd2e246df1f76485@oidc-connect.invalid").
		SetBrowserSessionKey("oidc-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"issuer":                 "https://issuer.example.com",
			"suggested_display_name": "OIDC Display",
			"suggested_avatar_url":   "https://cdn.example/oidc.png",
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("oidc-browser")})
	c.Request = req

	handler.CompleteOIDCOAuthRegistration(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.NotEmpty(t, responseData["access_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ(session.ResolvedEmail)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "OIDC Display", userEntity.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example.com"),
			authidentity.ProviderSubjectEQ("oidc-subject-1"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)
	require.Equal(t, "OIDC Display", identity.Metadata["display_name"])
	require.Equal(t, "https://cdn.example/oidc.png", identity.Metadata["avatar_url"])

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

func TestCompleteOIDCOAuthRegistrationRejectsAdoptExistingUserSession(t *testing.T) {
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
		SetSessionToken("oidc-complete-invalid-session").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-invalid-subject-1").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("oidc-invalid-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("oidc-invalid-browser")})
	c.Request = req

	handler.CompleteOIDCOAuthRegistration(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestCompleteOIDCOAuthRegistrationReturnsPendingSessionWhenChoiceStillRequired(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("oidc-complete-choice-session").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-choice-subject-1").
		SetResolvedEmail("oidc-choice-subject-1@oidc-connect.invalid").
		SetBrowserSessionKey("oidc-choice-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
			"issuer":   "https://issuer.example.com",
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("oidc-choice-browser")})
	c.Request = req

	handler.CompleteOIDCOAuthRegistration(c)

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

func TestCompleteOIDCOAuthRegistrationBindsIdentityWithoutAdoptionFlags(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("oidc-complete-no-adoption-session").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-subject-no-adoption").
		SetResolvedEmail("8c9f12b2a2e14b1db9efc08b27e0ef5c@oidc-connect.invalid").
		SetBrowserSessionKey("oidc-browser-no-adoption").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"issuer":                 "https://issuer.example.com",
			"suggested_display_name": "OIDC Legacy",
			"suggested_avatar_url":   "https://cdn.example/oidc-legacy.png",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("oidc-browser-no-adoption")})
	c.Request = req

	handler.CompleteOIDCOAuthRegistration(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	responseData := decodeJSONBody(t, recorder)
	require.NotEmpty(t, responseData["access_token"])
	require.NotEmpty(t, responseData["refresh_token"])

	userEntity, err := client.User.Query().
		Where(dbuser.EmailEQ(session.ResolvedEmail)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "oidc_user", userEntity.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example.com"),
			authidentity.ProviderSubjectEQ("oidc-subject-no-adoption"),
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

func TestCompleteOIDCOAuthRegistrationRejectsIdentityOwnershipConflictBeforeUserCreation(t *testing.T) {
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
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-conflict-subject").
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("oidc-complete-conflict-session").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example.com").
		SetProviderSubject("oidc-conflict-subject").
		SetResolvedEmail("f6f5f1f16f9248ccb11e0d633963b290@oidc-connect.invalid").
		SetBrowserSessionKey("oidc-conflict-browser").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
			"issuer":   "https://issuer.example.com",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"invitation_code":"invite-1"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/complete-registration", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("oidc-conflict-browser")})
	c.Request = req

	handler.CompleteOIDCOAuthRegistration(c)

	require.Equal(t, http.StatusConflict, recorder.Code)
	payload := decodeJSONBody(t, recorder)
	require.Equal(t, "AUTH_IDENTITY_OWNERSHIP_CONFLICT", payload["reason"])

	userCount, err := client.User.Query().
		Where(dbuser.EmailEQ("f6f5f1f16f9248ccb11e0d633963b290@oidc-connect.invalid")).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestTryOIDCVerifiedEmailFastPathCreatesUserAndIdentity(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	identity := identitycore.PendingAuthIdentityKey{
		ProviderType:    "oidc",
		ProviderKey:     "https://issuer.example.com",
		ProviderSubject: "fast-path-subject",
	}
	completed := handler.OIDC.TryOIDCVerifiedEmailFastPath(
		c,
		"/auth/oidc/callback",
		"/dashboard",
		identity,
		"fastpath@example.com",
		"fastpath_user",
		map[string]any{
			"suggested_display_name": "Fast Path",
			"suggested_avatar_url":   "",
		},
	)
	require.True(t, completed)
	require.Equal(t, http.StatusFound, recorder.Code)

	location := recorder.Header().Get("Location")
	require.Contains(t, location, "/auth/oidc/callback")
	require.Contains(t, location, "access_token=")
	require.Contains(t, location, "refresh_token=")
	require.Contains(t, location, "token_type=Bearer")

	user, err := client.User.Query().Where(dbuser.EmailEQ("fastpath@example.com")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "fastpath_user", user.Username)
	require.Equal(t, "oidc", user.SignupSource)

	identityRecord, err := client.AuthIdentity.Query().Where(
		authidentity.ProviderTypeEQ("oidc"),
		authidentity.ProviderKeyEQ("https://issuer.example.com"),
		authidentity.ProviderSubjectEQ("fast-path-subject"),
		authidentity.UserIDEQ(user.ID),
	).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "fastpath@example.com", identityRecord.Metadata["email"])
	require.Equal(t, true, identityRecord.Metadata["email_verified"])

	pendingCount, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, pendingCount)
}

func TestOIDCOAuthCallbackVerifiedEmailFastPathIssuesTokenWithoutPendingSession(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-fast-callback-subject",
		PreferredUsername: "oidc_fast_callback",
		DisplayName:       "OIDC Fast Callback",
		AvatarURL:         "https://cdn.example/oidc-fast.png",
		Email:             "oidc-fast-callback@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClientWithSettings(t, false, cfg, nil)
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-fast-callback", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-fast-callback"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-fast-callback"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-fast-callback-subject"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-fast-callback"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "/auth/oidc/callback#")
	require.Contains(t, location, "access_token=")
	require.Contains(t, location, "refresh_token=")
	require.Contains(t, location, "token_type=Bearer")
	fragmentValues := parseOAuthRedirectFragment(t, location)
	require.Equal(t, "/dashboard", fragmentValues.Get("redirect"))
	requireCookieCleared(t, recorder, identityhttp.OauthPendingSessionCookieName)
	requireCookieCleared(t, recorder, identityhttp.OauthPendingBrowserCookieName)

	ctx := context.Background()
	user, err := client.User.Query().Where(dbuser.EmailEQ("oidc-fast-callback@example.com")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "oidc_fast_callback", user.Username)
	require.Equal(t, "oidc", user.SignupSource)

	identity, err := client.AuthIdentity.Query().Where(
		authidentity.ProviderTypeEQ("oidc"),
		authidentity.ProviderKeyEQ(cfg.IssuerURL),
		authidentity.ProviderSubjectEQ("oidc-fast-callback-subject"),
		authidentity.UserIDEQ(user.ID),
	).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "oidc-fast-callback@example.com", identity.Metadata["email"])
	require.Equal(t, true, identity.Metadata["email_verified"])
	require.Equal(t, "OIDC Fast Callback", identity.Metadata["suggested_display_name"])
	require.NotEqual(t, identity.Metadata["email"], identity.Metadata["synthetic_email"])

	pendingCount, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, pendingCount)
}

func TestOIDCOAuthCallbackVerifiedEmailFastPathBackendModeBlocksBeforeUserCreation(t *testing.T) {
	cfg, cleanup := newOIDCTestProvider(t, oidcProviderFixture{
		Subject:           "oidc-fast-backend-mode-subject",
		PreferredUsername: "oidc_backend_mode",
		DisplayName:       "OIDC Backend Mode",
		Email:             "oidc-backend-mode@example.com",
		EmailVerified:     true,
	})
	defer cleanup()

	handler, client := newOIDCOauthHTTPFixtureAndClientWithSettings(t, false, cfg, map[string]string{
		gateway.SettingKeyBackendModeEnabled: "true",
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback?code=oidc-code&state=state-backend-mode", nil)
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthStateCookieName, "state-backend-mode"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthRedirectCookie, "/dashboard"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthVerifierCookie, "verifier-backend-mode"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthNonceCookie, "nonce-oidc-fast-backend-mode-subject"))
	req.AddCookie(encodedCookie(identityhttp.OidcOAuthIntentCookieName, identityhttp.OauthIntentLogin))
	req.AddCookie(encodedCookie(identityhttp.OauthPendingBrowserCookieName, "browser-backend-mode"))
	c.Request = req

	handler.OIDCOAuthCallback(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	assertOAuthRedirectError(t, recorder.Header().Get("Location"), "login_blocked", "BACKEND_MODE_ADMIN_ONLY")
	requireCookieCleared(t, recorder, identityhttp.OauthPendingSessionCookieName)
	requireCookieCleared(t, recorder, identityhttp.OauthPendingBrowserCookieName)

	ctx := context.Background()
	userCount, err := client.User.Query().Where(dbuser.EmailEQ("oidc-backend-mode@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)
	identityCount, err := client.AuthIdentity.Query().
		Where(authidentity.ProviderSubjectEQ("oidc-fast-backend-mode-subject")).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)
	pendingCount, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, pendingCount)
}

func TestTryOIDCVerifiedEmailFastPathSkippedWhenInvitationCodeRequired(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, true)
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	identity := identitycore.PendingAuthIdentityKey{
		ProviderType:    "oidc",
		ProviderKey:     "https://issuer.example.com",
		ProviderSubject: "fast-path-skipped-invitation",
	}
	completed := handler.OIDC.TryOIDCVerifiedEmailFastPath(
		c,
		"/auth/oidc/callback",
		"/dashboard",
		identity,
		"invite-only@example.com",
		"invite_only_user",
		map[string]any{},
	)
	require.False(t, completed)
	require.NotEqual(t, http.StatusFound, recorder.Code)

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("invite-only@example.com")).Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, userCount)
}

func TestTryOIDCVerifiedEmailFastPathSkippedWhenForceEmailEnabled(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			identitycore.SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	})
	t.Cleanup(func() { _ = client.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	identity := identitycore.PendingAuthIdentityKey{
		ProviderType:    "oidc",
		ProviderKey:     "https://issuer.example.com",
		ProviderSubject: "fast-path-skipped-force-email",
	}
	completed := handler.OIDC.TryOIDCVerifiedEmailFastPath(
		c,
		"/auth/oidc/callback",
		"/dashboard",
		identity,
		"force-email@example.com",
		"force_email_user",
		map[string]any{},
	)
	require.False(t, completed)

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("force-email@example.com")).Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, userCount)
}

type oidcProviderFixture struct {
	Subject           string
	PreferredUsername string
	DisplayName       string
	AvatarURL         string
	Email             string
	EmailVerified     bool
}

func newOIDCOAuthTestHandler(t *testing.T, invitationEnabled bool, oauthCfg config.OIDCConnectConfig) *authHTTPFixture {
	t.Helper()
	handler, _ := newOIDCOauthHTTPFixtureAndClient(t, invitationEnabled, oauthCfg)
	return handler
}

func newOIDCOauthHTTPFixtureAndClient(t *testing.T, invitationEnabled bool, oauthCfg config.OIDCConnectConfig) (*authHTTPFixture, *dbent.Client) {
	t.Helper()
	handler, client := newOAuthPendingFlowTestHandler(t, invitationEnabled)
	handler.settingSvc = nil
	handler.cfg = &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		OIDC: oauthCfg,
	}
	bindAuthHTTPFixture(t, handler)
	return handler, client
}

func newOIDCOauthHTTPFixtureAndClientWithSettings(
	t *testing.T,
	invitationEnabled bool,
	oauthCfg config.OIDCConnectConfig,
	settingValues map[string]string,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	values := map[string]string{
		identitycore.SettingKeyOIDCConnectEnabled:              "true",
		identitycore.SettingKeyOIDCConnectProviderName:         strings.TrimSpace(identitycore.OAuthFirstNonEmpty(oauthCfg.ProviderName, "OIDC")),
		identitycore.SettingKeyOIDCConnectClientID:             oauthCfg.ClientID,
		identitycore.SettingKeyOIDCConnectClientSecret:         oauthCfg.ClientSecret,
		identitycore.SettingKeyOIDCConnectIssuerURL:            oauthCfg.IssuerURL,
		identitycore.SettingKeyOIDCConnectAuthorizeURL:         oauthCfg.AuthorizeURL,
		identitycore.SettingKeyOIDCConnectTokenURL:             oauthCfg.TokenURL,
		identitycore.SettingKeyOIDCConnectUserInfoURL:          oauthCfg.UserInfoURL,
		identitycore.SettingKeyOIDCConnectJWKSURL:              oauthCfg.JWKSURL,
		identitycore.SettingKeyOIDCConnectScopes:               oauthCfg.Scopes,
		identitycore.SettingKeyOIDCConnectRedirectURL:          oauthCfg.RedirectURL,
		identitycore.SettingKeyOIDCConnectFrontendRedirectURL:  oauthCfg.FrontendRedirectURL,
		identitycore.SettingKeyOIDCConnectTokenAuthMethod:      oauthCfg.TokenAuthMethod,
		identitycore.SettingKeyOIDCConnectUsePKCE:              boolSettingValue(oauthCfg.UsePKCE),
		identitycore.SettingKeyOIDCConnectValidateIDToken:      boolSettingValue(oauthCfg.ValidateIDToken),
		identitycore.SettingKeyOIDCConnectAllowedSigningAlgs:   oauthCfg.AllowedSigningAlgs,
		identitycore.SettingKeyOIDCConnectClockSkewSeconds:     "120",
		identitycore.SettingKeyOIDCConnectRequireEmailVerified: boolSettingValue(oauthCfg.RequireEmailVerified),
	}
	for key, value := range settingValues {
		values[key] = value
	}

	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		invitationEnabled: invitationEnabled,
		settingValues:     values,
	})
	if handler.cfg == nil {
		handler.cfg = &config.Config{}
	}
	handler.cfg.OIDC = oauthCfg
	bindAuthHTTPFixture(t, handler)
	return handler, client
}

func newOIDCTestProvider(t *testing.T, fixture oidcProviderFixture) (config.OIDCConnectConfig, func()) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	kid := "test-kid"
	jwks := provider.OidcJWKSet{Keys: []provider.OidcJWK{buildRSAJWK(kid, &privateKey.PublicKey)}}
	tokenResponse := provider.OidcTokenResponse{
		AccessToken: "oidc-access-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
	}

	userInfoPayload := map[string]any{
		"sub":                fixture.Subject,
		"preferred_username": fixture.PreferredUsername,
		"name":               fixture.DisplayName,
		"picture":            fixture.AvatarURL,
		"email":              fixture.Email,
		"email_verified":     fixture.EmailVerified,
	}

	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.NoError(t, json.NewEncoder(w).Encode(tokenResponse))
		case "/userinfo":
			require.NoError(t, json.NewEncoder(w).Encode(userInfoPayload))
		case "/jwks":
			require.NoError(t, json.NewEncoder(w).Encode(jwks))
		default:
			http.NotFound(w, r)
		}
	}))

	issuer = server.URL
	now := time.Now()
	claims := provider.OidcIDTokenClaims{
		Email:             fixture.Email,
		EmailVerified:     boolPtr(fixture.EmailVerified),
		PreferredUsername: fixture.PreferredUsername,
		Name:              fixture.DisplayName,
		Nonce:             "nonce-" + fixture.Subject,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   fixture.Subject,
			Audience:  jwt.ClaimStrings{"oidc-client"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	tokenResponse.IDToken, err = token.SignedString(privateKey)
	require.NoError(t, err)

	cfg := config.OIDCConnectConfig{
		Enabled:              true,
		ProviderName:         "Test OIDC",
		ClientID:             "oidc-client",
		ClientSecret:         "oidc-secret",
		IssuerURL:            issuer,
		AuthorizeURL:         issuer + "/authorize",
		TokenURL:             issuer + "/token",
		UserInfoURL:          issuer + "/userinfo",
		JWKSURL:              issuer + "/jwks",
		Scopes:               "openid profile email",
		RedirectURL:          "https://api.example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL:  "/auth/oidc/callback",
		TokenAuthMethod:      "client_secret_post",
		UsePKCE:              true,
		ValidateIDToken:      true,
		AllowedSigningAlgs:   "RS256",
		ClockSkewSeconds:     120,
		RequireEmailVerified: false,
	}
	return cfg, server.Close
}
