package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/ent/authidentity"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/idtoken"
)

type googleIDTokenVerifierStub struct {
	claims     *googleIDTokenClaims
	err        error
	calls      int
	credential string
	audience   string
}

func (s *googleIDTokenVerifierStub) Verify(_ context.Context, credential string, audience string) (*googleIDTokenClaims, error) {
	s.calls++
	s.credential = credential
	s.audience = audience
	return s.claims, s.err
}

func googleOneTapTestSettings() map[string]string {
	return map[string]string{
		service.SettingKeyGoogleOneTapEnabled:            "true",
		service.SettingKeyGoogleOAuthEnabled:             "true",
		service.SettingKeyGoogleOAuthClientID:            "google-web-client",
		service.SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		service.SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		service.SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
	}
}

func performGoogleOneTapRequest(t *testing.T, handler *AuthHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/google/one-tap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	handler.GoogleOneTap(c)
	return recorder
}

func TestValidateGoogleIDTokenPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	validPayload := func() *idtoken.Payload {
		return &idtoken.Payload{
			Issuer:   "https://accounts.google.com",
			Audience: "google-web-client",
			Expires:  now.Add(time.Minute).Unix(),
			Subject:  "google-subject",
			Claims: map[string]any{
				"email":          "user@example.com",
				"email_verified": true,
				"name":           "Example User",
			},
		}
	}

	claims, err := validateGoogleIDTokenPayload(validPayload(), "google-web-client", now)
	require.NoError(t, err)
	require.Equal(t, "google-subject", claims.Subject)
	require.Equal(t, "user@example.com", claims.Email)
	require.True(t, claims.EmailVerified)

	tests := []struct {
		name   string
		mutate func(*idtoken.Payload)
	}{
		{name: "错误 audience", mutate: func(payload *idtoken.Payload) { payload.Audience = "other-client" }},
		{name: "错误 issuer", mutate: func(payload *idtoken.Payload) { payload.Issuer = "https://issuer.example" }},
		{name: "token 已过期", mutate: func(payload *idtoken.Payload) { payload.Expires = now.Unix() }},
		{name: "邮箱未验证", mutate: func(payload *idtoken.Payload) { payload.Claims["email_verified"] = false }},
		{name: "缺少主体", mutate: func(payload *idtoken.Payload) { payload.Subject = "" }},
		{name: "缺少邮箱", mutate: func(payload *idtoken.Payload) { payload.Claims["email"] = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := validPayload()
			tt.mutate(payload)
			_, err := validateGoogleIDTokenPayload(payload, "google-web-client", now)
			require.Error(t, err)
		})
	}
}

func TestGoogleOneTapCreatesPendingRegistrationForNewUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: googleOneTapTestSettings(),
	})
	verifier := &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{
		Subject:       "google-new-user",
		Email:         "new-user@example.com",
		EmailVerified: true,
		Name:          "New User",
	}}
	handler.googleIDTokenVerifier = verifier

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token","redirect":"/dashboard","aff_code":"AFF123","promo_code":"PROMO123"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := decodeJSONResponseData(t, recorder)
	require.Equal(t, googleOneTapStatusRegistration, payload["status"])
	require.Equal(t, "/dashboard", payload["redirect"])
	require.Equal(t, "valid-token", verifier.credential)
	require.Equal(t, "google-web-client", verifier.audience)

	session, err := client.PendingAuthSession.Query().Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "google", session.ProviderType)
	require.Equal(t, "google-new-user", session.ProviderSubject)
	require.Equal(t, "new-user@example.com", session.ResolvedEmail)
	require.Equal(t, "AFF123", pendingSessionStringValue(session.UpstreamIdentityClaims, "aff_code"))
	require.Equal(t, "PROMO123", pendingSessionStringValue(session.LocalFlowState, oauthPromoCodeStateKey))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), oauthPendingSessionCookieName))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), oauthPendingBrowserCookieName))
}

func TestGoogleOneTapLogsInExistingUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: googleOneTapTestSettings(),
	})
	ctx := context.Background()
	user, err := client.User.Create().
		SetEmail("existing@example.com").
		SetUsername("existing").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{
		Subject:       "google-existing-user",
		Email:         "existing@example.com",
		EmailVerified: true,
	}}

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token","redirect":"/dashboard"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := decodeJSONResponseData(t, recorder)
	require.Equal(t, googleOneTapStatusAuthenticated, payload["status"])
	require.NotEmpty(t, payload["access_token"])
	require.NotEmpty(t, payload["refresh_token"])
	require.Equal(t, "Bearer", payload["token_type"])
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	identityCount, err := client.AuthIdentity.Query().Where(
		authidentity.ProviderTypeEQ("google"),
		authidentity.ProviderSubjectEQ("google-existing-user"),
		authidentity.UserIDEQ(user.ID),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, identityCount)
}

func TestGoogleOneTapRejectsDisabledExistingUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: googleOneTapTestSettings(),
	})
	ctx := context.Background()
	_, err := client.User.Create().
		SetEmail("disabled@example.com").
		SetUsername("disabled").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{
		Subject:       "google-disabled-user",
		Email:         "disabled@example.com",
		EmailVerified: true,
	}}

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token"}`)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "access_token")
	count, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestGoogleOneTapFailsClosedBeforeIdentityLookup(t *testing.T) {
	tests := []struct {
		name           string
		settingsMutate func(map[string]string)
		verifier       *googleIDTokenVerifierStub
	}{
		{
			name: "One Tap 已关闭",
			settingsMutate: func(settings map[string]string) {
				settings[service.SettingKeyGoogleOneTapEnabled] = "false"
			},
			verifier: &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
		},
		{
			name: "Google OAuth 配置不完整",
			settingsMutate: func(settings map[string]string) {
				delete(settings, service.SettingKeyGoogleOAuthClientSecret)
			},
			verifier: &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
		},
		{
			name:           "凭据验证失败",
			settingsMutate: func(map[string]string) {},
			verifier:       &googleIDTokenVerifierStub{err: errors.New("signature verification failed")},
		},
		{
			name:           "邮箱未验证",
			settingsMutate: func(map[string]string) {},
			verifier:       &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{Subject: "sub", Email: "user@example.com"}},
		},
		{
			name: "动作验证码已开启",
			settingsMutate: func(settings map[string]string) {
				settings[service.SettingKeyTencentCaptchaEnabled] = "true"
			},
			verifier: &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := googleOneTapTestSettings()
			tt.settingsMutate(settings)
			handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
				settingValues: settings,
			})
			handler.googleIDTokenVerifier = tt.verifier

			recorder := performGoogleOneTapRequest(t, handler, `{"credential":"untrusted-token"}`)

			require.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest)
			require.NotContains(t, recorder.Body.String(), "untrusted-token")
			require.NotContains(t, recorder.Body.String(), "signature verification failed")
			count, err := client.PendingAuthSession.Query().Count(context.Background())
			require.NoError(t, err)
			require.Zero(t, count)
			if tt.name != "凭据验证失败" && tt.name != "邮箱未验证" {
				require.Zero(t, tt.verifier.calls)
			}
		})
	}
}

func TestGoogleOneTapRejectsNewUserWhenRegistrationDisabled(t *testing.T) {
	settings := googleOneTapTestSettings()
	settings[service.SettingKeyRegistrationEnabled] = "false"
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: settings,
	})
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &googleIDTokenClaims{
		Subject:       "google-disabled-registration",
		Email:         "disabled-registration@example.com",
		EmailVerified: true,
	}}

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token"}`)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	count, err := client.PendingAuthSession.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestGoogleOneTapRejectsOversizedCredential(t *testing.T) {
	handler := &AuthHandler{}
	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"`+strings.Repeat("x", googleOneTapCredentialMaxBytes+1)+`"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Google credential is invalid")
}

func TestGoogleOneTapRejectsOversizedRequestBeforeBinding(t *testing.T) {
	handler := &AuthHandler{}
	body := `{"credential":"valid-token","padding":"` + strings.Repeat("x", googleOneTapRequestMaxBytes) + `"}`

	recorder := performGoogleOneTapRequest(t, handler, body)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Invalid request")
	require.NotContains(t, recorder.Body.String(), "AUTH_SERVICE_NOT_READY")
}
