package identityhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type googleIDTokenVerifierStub struct {
	claims     *identityprovider.GoogleIDTokenClaims
	err        error
	calls      int
	credential string
	audience   string
}

func (s *googleIDTokenVerifierStub) Verify(_ context.Context, credential string, audience string) (*identityprovider.GoogleIDTokenClaims, error) {
	s.calls++
	s.credential = credential
	s.audience = audience
	return s.claims, s.err
}

func googleOneTapTestSettings() map[string]string {
	return map[string]string{
		identity.SettingKeyGoogleOneTapEnabled:            "true",
		identity.SettingKeyGoogleOAuthEnabled:             "true",
		identity.SettingKeyGoogleOAuthClientID:            "google-web-client",
		identity.SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		identity.SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		identity.SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
	}
}

func performGoogleOneTapRequest(t *testing.T, handler *authHTTPFixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/google/one-tap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	handler.GoogleOneTap(c)
	return recorder
}

func TestGoogleOneTapCreatesPendingRegistrationForNewUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: googleOneTapTestSettings(),
	})
	verifier := &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{
		Subject:       "google-new-user",
		Email:         "new-user@example.com",
		EmailVerified: true,
		Name:          "New User",
	}}
	handler.googleIDTokenVerifier = verifier

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token","redirect":"/dashboard","aff_code":"AFF123","promo_code":"PROMO123"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := decodeJSONResponseData(t, recorder)
	require.Equal(t, identityhttp.GoogleOneTapStatusRegistration, payload["status"])
	require.Equal(t, "/dashboard", payload["redirect"])
	require.Equal(t, "valid-token", verifier.credential)
	require.Equal(t, "google-web-client", verifier.audience)

	session, err := client.PendingAuthSession.Query().Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "google", session.ProviderType)
	require.Equal(t, "google-new-user", session.ProviderSubject)
	require.Equal(t, "new-user@example.com", session.ResolvedEmail)
	require.Equal(t, "AFF123", identityhttp.PendingSessionStringValue(session.UpstreamIdentityClaims, "aff_code"))
	require.Equal(t, "PROMO123", identityhttp.PendingSessionStringValue(session.LocalFlowState, identityhttp.OauthPromoCodeStateKey))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingBrowserCookieName))
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
		SetRole(identity.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{
		Subject:       "google-existing-user",
		Email:         "existing@example.com",
		EmailVerified: true,
	}}

	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"valid-token","redirect":"/dashboard"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := decodeJSONResponseData(t, recorder)
	require.Equal(t, identityhttp.GoogleOneTapStatusAuthenticated, payload["status"])
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
		SetRole(identity.RoleUser).
		SetStatus(billing.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{
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
				settings[identity.SettingKeyGoogleOneTapEnabled] = "false"
			},
			verifier: &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
		},
		{
			name: "Google OAuth 配置不完整",
			settingsMutate: func(settings map[string]string) {
				delete(settings, identity.SettingKeyGoogleOAuthClientSecret)
			},
			verifier: &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
		},
		{
			name:           "凭据验证失败",
			settingsMutate: func(map[string]string) {},
			verifier:       &googleIDTokenVerifierStub{err: errors.New("signature verification failed")},
		},
		{
			name:           "邮箱未验证",
			settingsMutate: func(map[string]string) {},
			verifier:       &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{Subject: "sub", Email: "user@example.com"}},
		},
		{
			name: "动作验证码已开启",
			settingsMutate: func(settings map[string]string) {
				settings[identity.SettingKeyTencentCaptchaEnabled] = "true"
			},
			verifier: &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{Subject: "sub", Email: "user@example.com", EmailVerified: true}},
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
	settings[identity.SettingKeyRegistrationEnabled] = "false"
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: settings,
	})
	handler.googleIDTokenVerifier = &googleIDTokenVerifierStub{claims: &identityprovider.GoogleIDTokenClaims{
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
	handler := newAuthHTTPFixture(t, &authHTTPFixture{})
	recorder := performGoogleOneTapRequest(t, handler, `{"credential":"`+strings.Repeat("x", identityhttp.GoogleOneTapCredentialMaxBytes+1)+`"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Google credential is invalid")
}

func TestGoogleOneTapRejectsOversizedRequestBeforeBinding(t *testing.T) {
	handler := newAuthHTTPFixture(t, &authHTTPFixture{})
	body := `{"credential":"valid-token","padding":"` + strings.Repeat("x", identityhttp.GoogleOneTapRequestMaxBytes) + `"}`

	recorder := performGoogleOneTapRequest(t, handler, body)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Invalid request")
	require.NotContains(t, recorder.Body.String(), "AUTH_SERVICE_NOT_READY")
}
