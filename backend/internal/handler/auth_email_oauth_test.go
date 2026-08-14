package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/ent/authidentity"
	"github.com/BrandonVee/TokenRouter/ent/redeemcode"
	dbuser "github.com/BrandonVee/TokenRouter/ent/user"
	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmailOAuthCallbackRequiresPendingRegistrationWhenInvitationEnabled(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, true)
	ctx := context.Background()

	state := "github-oauth-state"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback?code=code-1&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: emailOAuthStateCookieName, Value: encodeCookieValue(state)})
	req.AddCookie(&http.Cookie{Name: emailOAuthRedirectCookie, Value: encodeCookieValue("/dashboard")})
	req.AddCookie(&http.Cookie{Name: emailOAuthProviderCookie, Value: encodeCookieValue("github")})
	c.Request = req

	profile := &emailOAuthProfile{
		Subject:       "github-123",
		Email:         "fresh@example.com",
		EmailVerified: true,
		Username:      "fresh",
		DisplayName:   "Fresh User",
		AvatarURL:     "https://cdn.example/fresh.png",
		Metadata: map[string]any{
			"login": "fresh",
		},
	}
	handler.emailOAuthCallbackWithProfile(c, "github", config.EmailOAuthProviderConfig{
		Enabled:             true,
		ClientID:            "github-client",
		ClientSecret:        "github-secret",
		RedirectURL:         "https://app.example/api/v1/auth/oauth/github/callback",
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", profile)

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "/auth/oauth/callback")
	require.NotContains(t, location, "access_token=")

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("fresh@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	session, err := client.PendingAuthSession.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "github", session.ProviderType)
	require.Equal(t, "github", session.ProviderKey)
	require.Equal(t, "github-123", session.ProviderSubject)
	require.Equal(t, "fresh@example.com", session.ResolvedEmail)
	require.Equal(t, "/dashboard", session.RedirectTo)
	require.Nil(t, session.TargetUserID)

	completion, ok := readCompletionResponse(session.LocalFlowState)
	require.True(t, ok)
	require.Equal(t, oauthPendingChoiceStep, completion["step"])
	require.Equal(t, "invitation_required", completion["error"])
	require.Equal(t, true, completion["invitation_required"])
	require.Equal(t, "fresh@example.com", completion["email"])
	require.Equal(t, "fresh@example.com", completion["resolved_email"])
	require.Equal(t, true, completion["create_account_allowed"])

	require.NotNil(t, findCookie(recorder.Result().Cookies(), oauthPendingSessionCookieName))
	require.NotNil(t, findCookie(recorder.Result().Cookies(), oauthPendingBrowserCookieName))
}

func TestEmailOAuthCallbackExistingEmailLogsInWhenInvitationEnabled(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, true)
	ctx := context.Background()

	user, err := client.User.Create().
		SetEmail("existing@example.com").
		SetUsername("existing").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/google/callback", nil)

	handler.emailOAuthCallbackWithProfile(c, "google", config.EmailOAuthProviderConfig{
		Enabled:             true,
		ClientID:            "google-client",
		ClientSecret:        "google-secret",
		RedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", &emailOAuthProfile{
		Subject:       "google-123",
		Email:         "existing@example.com",
		EmailVerified: true,
		Username:      "existing",
	})

	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "access_token=")
	require.Contains(t, location, "redirect=%252Fdashboard")

	sessionCount, err := client.PendingAuthSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, sessionCount)

	identityCount, err := client.AuthIdentity.Query().Where(
		authidentity.ProviderTypeEQ("google"),
		authidentity.ProviderSubjectEQ("google-123"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, identityCount)
	_ = user
}

func TestEmailOAuthCallbackCreatesPasswordRegistrationSessionWithAffiliateCode(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback", nil)
	req.AddCookie(&http.Cookie{Name: emailOAuthAffCookie, Value: encodeCookieValue(" AFF123 ")})
	c.Request = req

	handler.emailOAuthCallbackWithProfile(c, "github", config.EmailOAuthProviderConfig{
		Enabled:             true,
		ClientID:            "github-client",
		ClientSecret:        "github-secret",
		RedirectURL:         "https://app.example/api/v1/auth/oauth/github/callback",
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", &emailOAuthProfile{
		Subject:       "github-ref-user",
		Email:         "ref-user@example.com",
		EmailVerified: true,
		Username:      "ref-user",
	})

	require.Equal(t, http.StatusFound, recorder.Code)
	require.NotContains(t, recorder.Header().Get("Location"), "access_token=")

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("ref-user@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	session, err := client.PendingAuthSession.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "ref-user@example.com", session.ResolvedEmail)
	require.Equal(t, "AFF123", pendingSessionStringValue(session.UpstreamIdentityClaims, "aff_code"))

	completion, ok := readCompletionResponse(session.LocalFlowState)
	require.True(t, ok)
	require.Equal(t, oauthPendingChoiceStep, completion["step"])
	require.Equal(t, "registration_completion_required", completion["error"])
	require.Equal(t, false, completion["invitation_required"])
	require.Equal(t, true, completion["create_account_allowed"])
	require.Equal(t, true, completion["force_email_on_signup"])
	require.Equal(t, "ref-user@example.com", completion["resolved_email"])
}

func TestEmailOAuthStartAcceptsAffCodeQueryAlias(t *testing.T) {
	handler, _ := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			service.SettingKeyGitHubOAuthEnabled:             "true",
			service.SettingKeyGitHubOAuthClientID:            "github-client",
			service.SettingKeyGitHubOAuthClientSecret:        "github-secret",
			service.SettingKeyGitHubOAuthRedirectURL:         "https://api.example/api/v1/auth/oauth/github/callback",
			service.SettingKeyGitHubOAuthFrontendRedirectURL: "/auth/oauth/callback",
		},
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?redirect=/dashboard&aff_code=AFFALIAS", nil)

	handler.GitHubOAuthStart(c)

	require.Equal(t, http.StatusFound, recorder.Code)
	affCookie := findCookie(recorder.Result().Cookies(), emailOAuthAffCookie)
	require.NotNil(t, affCookie)
	require.Equal(t, encodeCookieValue("AFFALIAS"), affCookie.Value)
}

func TestEmailOAuthStartPreservesPromoCodeInPendingSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			service.SettingKeyGitHubOAuthEnabled:             "true",
			service.SettingKeyGitHubOAuthClientID:            "github-client",
			service.SettingKeyGitHubOAuthClientSecret:        "github-secret",
			service.SettingKeyGitHubOAuthRedirectURL:         "https://api.example/api/v1/auth/oauth/github/callback",
			service.SettingKeyGitHubOAuthFrontendRedirectURL: "/auth/oauth/callback",
		},
	})
	ctx := context.Background()

	startRecorder := httptest.NewRecorder()
	startCtx, _ := gin.CreateTestContext(startRecorder)
	startCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?promo_code=WELCOME2024", nil)

	handler.GitHubOAuthStart(startCtx)

	require.Equal(t, http.StatusFound, startRecorder.Code)
	promoCookie := findCookie(startRecorder.Result().Cookies(), oauthPromoCodeCookieName)
	require.NotNil(t, promoCookie)
	require.Equal(t, "WELCOME2024", decodeCookieValueForTest(t, promoCookie.Value))

	callbackRecorder := httptest.NewRecorder()
	callbackCtx, _ := gin.CreateTestContext(callbackRecorder)
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback", nil)
	callbackReq.AddCookie(promoCookie)
	callbackCtx.Request = callbackReq

	handler.emailOAuthCallbackWithProfile(callbackCtx, "github", config.EmailOAuthProviderConfig{
		Enabled:             true,
		ClientID:            "github-client",
		ClientSecret:        "github-secret",
		RedirectURL:         "https://api.example/api/v1/auth/oauth/github/callback",
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", &emailOAuthProfile{
		Subject:       "github-promo-user",
		Email:         "promo-user@example.com",
		EmailVerified: true,
		Username:      "promo-user",
	})

	require.Equal(t, http.StatusFound, callbackRecorder.Code)
	session, err := client.PendingAuthSession.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "WELCOME2024", pendingOAuthPromoCode(session))
}

func TestCompleteEmailOAuthRegistrationUsesAffiliateCodeFromPendingSession(t *testing.T) {
	affiliateRepo := newOAuthPendingFlowAffiliateRepo()
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		invitationEnabled: true,
		settingValues: map[string]string{
			service.SettingKeyAffiliateEnabled: "true",
		},
		affiliateRepo: affiliateRepo,
	})
	ctx := context.Background()
	inviter, err := client.User.Create().
		SetEmail("inviter@example.com").
		SetUsername("inviter").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	affiliateRepo.setCode(t, inviter.ID, "AFF456")
	invitation, err := client.RedeemCode.Create().
		SetCode("INVITE456").
		SetType(service.RedeemTypeInvitation).
		SetStatus(service.StatusUnused).
		SetValue(0).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("email-oauth-ref-session-token").
		SetIntent(oauthIntentLogin).
		SetProviderType("google").
		SetProviderKey("google").
		SetProviderSubject("google-ref-user").
		SetResolvedEmail("pending-ref@example.com").
		SetRedirectTo("/dashboard").
		SetBrowserSessionKey("browser-ref-key").
		SetUpstreamIdentityClaims(map[string]any{
			"email":            "pending-ref@example.com",
			"email_verified":   true,
			"username":         "pending-ref",
			"provider":         "google",
			"provider_key":     "google",
			"provider_subject": "google-ref-user",
			"aff_code":         "AFF456",
		}).
		SetLocalFlowState(map[string]any{
			"step":  oauthPendingChoiceStep,
			"error": "invitation_required",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/google/complete-registration", strings.NewReader(`{"password":"secret-123","invitation_code":"INVITE456","email":"tampered@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("browser-ref-key")})
	c.Request = req

	handler.completeEmailOAuthRegistration(c, "google")

	require.Equal(t, http.StatusOK, recorder.Code)
	user, err := client.User.Query().Where(dbuser.EmailEQ("pending-ref@example.com")).Only(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, user.PasswordHash)
	require.NotEqual(t, "secret-123", user.PasswordHash)
	require.Equal(t, inviter.ID, affiliateRepo.inviterIDFor(t, user.ID))
	require.Equal(t, 1, affiliateRepo.affCountFor(t, inviter.ID))

	tamperedCount, err := client.User.Query().Where(dbuser.EmailEQ("tampered@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, tamperedCount)

	storedInvitation, err := client.RedeemCode.Query().Where(redeemcode.IDEQ(invitation.ID)).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedInvitation.UsedBy)
	require.Equal(t, user.ID, *storedInvitation.UsedBy)
}

func TestCompleteEmailOAuthRegistrationRequiresPassword(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("email-oauth-password-session-token").
		SetIntent(oauthIntentLogin).
		SetProviderType("github").
		SetProviderKey("github").
		SetProviderSubject("github-password-user").
		SetResolvedEmail("password-user@example.com").
		SetBrowserSessionKey("browser-password-key").
		SetUpstreamIdentityClaims(map[string]any{
			"email":            "password-user@example.com",
			"email_verified":   true,
			"provider":         "github",
			"provider_key":     "github",
			"provider_subject": "github-password-user",
		}).
		SetLocalFlowState(map[string]any{
			"step":  oauthPendingChoiceStep,
			"error": "registration_completion_required",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/github/complete-registration", strings.NewReader(`{"password":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("browser-password-key")})
	c.Request = req

	handler.completeEmailOAuthRegistration(c, "github")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	userCount, err := client.User.Query().Where(dbuser.EmailEQ("password-user@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)
}
