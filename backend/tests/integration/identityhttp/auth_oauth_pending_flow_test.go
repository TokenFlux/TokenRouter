package identityhttp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/promotion"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"entgo.io/ent/dialect"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"

	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	_ "modernc.org/sqlite"
)

func TestApplySuggestedProfileToCompletionResponse(t *testing.T) {
	payload := map[string]any{
		"access_token": "token",
	}
	upstream := map[string]any{
		"suggested_display_name": "Alice",
		"suggested_avatar_url":   "https://cdn.example/avatar.png",
	}

	identityhttp.ApplySuggestedProfileToCompletionResponse(payload, upstream)

	require.Equal(t, "Alice", payload["suggested_display_name"])
	require.Equal(t, "https://cdn.example/avatar.png", payload["suggested_avatar_url"])
	require.Equal(t, true, payload["adoption_required"])
}

func TestApplySuggestedProfileToCompletionResponseKeepsExistingPayloadValues(t *testing.T) {
	payload := map[string]any{
		"suggested_display_name": "Existing",
		"adoption_required":      false,
	}
	upstream := map[string]any{
		"suggested_display_name": "Alice",
		"suggested_avatar_url":   "https://cdn.example/avatar.png",
	}

	identityhttp.ApplySuggestedProfileToCompletionResponse(payload, upstream)

	require.Equal(t, "Existing", payload["suggested_display_name"])
	require.Equal(t, "https://cdn.example/avatar.png", payload["suggested_avatar_url"])
	require.Equal(t, true, payload["adoption_required"])
}

func TestSetOAuthPendingSessionCookieUsesProviderCompletionPathPrefix(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	identityhttp.SetOAuthPendingSessionCookie(ginCtx, "pending-session-token", false)

	cookie := findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName)
	require.NotNil(t, cookie)
	require.Equal(t, "/api/v1/auth/oauth", cookie.Path)
}

func TestExchangePendingOAuthCompletionPreviewThenFinalizeAppliesAdoptionDecision(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("linuxdo-123@linuxdo-connect.invalid").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("pending-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "linuxdo_user",
			"suggested_display_name": "Alice Example",
			"suggested_avatar_url":   "https://cdn.example/alice.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
			"redirect":     "/dashboard",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	previewRecorder := httptest.NewRecorder()
	previewCtx, _ := gin.CreateTestContext(previewRecorder)
	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	previewReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	previewReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("browser-session-key")})
	previewCtx.Request = previewReq

	handler.ExchangePendingOAuthCompletion(previewCtx)

	require.Equal(t, http.StatusOK, previewRecorder.Code)
	previewData := decodeJSONResponseData(t, previewRecorder)
	require.Equal(t, "Alice Example", previewData["suggested_display_name"])
	require.Equal(t, "https://cdn.example/alice.png", previewData["suggested_avatar_url"])
	require.Equal(t, true, previewData["adoption_required"])

	storedUser, err := client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-name", storedUser.Username)

	previewSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, previewSession.ConsumedAt)

	body := bytes.NewBufferString(`{"adopt_display_name":true,"adopt_avatar":true}`)
	finalizeRecorder := httptest.NewRecorder()
	finalizeCtx, _ := gin.CreateTestContext(finalizeRecorder)
	finalizeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	finalizeReq.Header.Set("Content-Type", "application/json")
	finalizeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	finalizeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("browser-session-key")})
	finalizeCtx.Request = finalizeReq

	handler.ExchangePendingOAuthCompletion(finalizeCtx)

	require.Equal(t, http.StatusOK, finalizeRecorder.Code)

	storedUser, err = client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Equal(t, "Alice Example", storedUser.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)
	require.Equal(t, "Alice Example", identity.Metadata["display_name"])
	require.Equal(t, "https://cdn.example/alice.png", identity.Metadata["avatar_url"])

	avatar := loadUserAvatarRecord(t, client, userEntity.ID)
	require.NotNil(t, avatar)
	require.Equal(t, "remote_url", avatar.StorageProvider)
	require.Equal(t, "https://cdn.example/alice.png", avatar.URL)

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

func TestExchangePendingOAuthCompletionSkipsInvalidAvatarAdoptionWithoutBlockingCompletion(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("invalid-avatar@example.com").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("pending-invalid-avatar-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("invalid-avatar-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("browser-invalid-avatar-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "linuxdo_user",
			"suggested_display_name": "Alice Example",
			"suggested_avatar_url":   "/avatars/alice.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
			"redirect":     "/dashboard",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":true,"adopt_avatar":true}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("browser-invalid-avatar-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("invalid-avatar-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Alice Example", identity.Metadata["display_name"])
	_, hasAdoptedAvatar := identity.Metadata["avatar_url"]
	require.False(t, hasAdoptedAvatar)

	avatar := loadUserAvatarRecord(t, client, userEntity.ID)
	require.Nil(t, avatar)

	consumed, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
}

func TestExchangePendingOAuthCompletionBindCurrentUserPreviewThenFinalizeBindsIdentityWithoutAdoption(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("bind-target@example.com").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-pending-session-token").
		SetIntent("bind_current_user").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("bind-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("bind-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "linuxdo_user",
			"suggested_display_name": "Bound Example",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
			"redirect":     "/settings/profile",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	previewRecorder := httptest.NewRecorder()
	previewCtx, _ := gin.CreateTestContext(previewRecorder)
	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	previewReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	previewReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-browser-session-key")})
	previewCtx.Request = previewReq

	handler.ExchangePendingOAuthCompletion(previewCtx)

	require.Equal(t, http.StatusOK, previewRecorder.Code)
	previewData := decodeJSONResponseData(t, previewRecorder)
	require.Equal(t, "Bound Example", previewData["suggested_display_name"])
	require.Equal(t, "https://cdn.example/bound.png", previewData["suggested_avatar_url"])
	require.Equal(t, true, previewData["adoption_required"])

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("bind-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	previewSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, previewSession.ConsumedAt)

	body := bytes.NewBufferString(`{"adopt_display_name":false,"adopt_avatar":false}`)
	finalizeRecorder := httptest.NewRecorder()
	finalizeCtx, _ := gin.CreateTestContext(finalizeRecorder)
	finalizeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	finalizeReq.Header.Set("Content-Type", "application/json")
	finalizeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	finalizeReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-browser-session-key")})
	finalizeCtx.Request = finalizeReq

	handler.ExchangePendingOAuthCompletion(finalizeCtx)

	require.Equal(t, http.StatusOK, finalizeRecorder.Code)

	storedUser, err := client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-name", storedUser.Username)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("bind-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)
	require.Equal(t, "Bound Example", identity.Metadata["suggested_display_name"])
	require.Equal(t, "https://cdn.example/bound.png", identity.Metadata["suggested_avatar_url"])
	_, hasDisplayName := identity.Metadata["display_name"]
	require.False(t, hasDisplayName)
	_, hasAvatarURL := identity.Metadata["avatar_url"]
	require.False(t, hasAvatarURL)

	decision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, decision.IdentityID)
	require.Equal(t, identity.ID, *decision.IdentityID)
	require.False(t, decision.AdoptDisplayName)
	require.False(t, decision.AdoptAvatar)

	consumed, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
}

func TestExchangePendingOAuthCompletionBindCurrentUserOwnershipConflict(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	targetUser, err := client.User.Create().
		SetEmail("bind-conflict-target@example.com").
		SetUsername("target-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	ownerUser, err := client.User.Create().
		SetEmail("bind-conflict-owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	existingIdentity, err := client.AuthIdentity.Create().
		SetUserID(ownerUser.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("conflict-123").
		SetMetadata(map[string]any{"username": "owner-user"}).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-conflict-session-token").
		SetIntent("bind_current_user").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("conflict-123").
		SetTargetUserID(targetUser.ID).
		SetResolvedEmail(targetUser.Email).
		SetBrowserSessionKey("bind-conflict-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Conflict Example",
			"suggested_avatar_url":   "https://cdn.example/conflict.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-conflict-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	payload := decodeJSONBody(t, recorder)
	require.Equal(t, "PENDING_AUTH_ADOPTION_APPLY_FAILED", payload["reason"])

	identity, err := client.AuthIdentity.Get(ctx, existingIdentity.ID)
	require.NoError(t, err)
	require.Equal(t, ownerUser.ID, identity.UserID)

	decision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, decision.IdentityID)
	require.False(t, decision.AdoptDisplayName)
	require.False(t, decision.AdoptAvatar)

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionLoginFalseFalseBindsIdentityWithoutAdoption(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("login-false@example.com").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("login-false-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("login-false-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("login-false-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Login Example",
			"suggested_avatar_url":   "https://cdn.example/login.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("login-false-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("login-false-123"),
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

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionLoginReassignsExistingDecisionIdentityReference(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("login-reassign@example.com").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	existingIdentity, err := client.AuthIdentity.Create().
		SetUserID(userEntity.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("login-reassign-123").
		SetMetadata(map[string]any{}).
		Save(ctx)
	require.NoError(t, err)

	previousSession, err := client.PendingAuthSession.Create().
		SetSessionToken("login-reassign-previous-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("login-reassign-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("login-reassign-previous-browser-session-key").
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "previous-access-token",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	previousDecision, err := client.IdentityAdoptionDecision.Create().
		SetPendingAuthSessionID(previousSession.ID).
		SetIdentityID(existingIdentity.ID).
		SetAdoptDisplayName(true).
		SetAdoptAvatar(true).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("login-reassign-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("login-reassign-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("login-reassign-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Login Reassign",
			"suggested_avatar_url":   "https://cdn.example/login-reassign.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.IdentityAdoptionDecision.Create().
		SetPendingAuthSessionID(session.ID).
		SetAdoptDisplayName(false).
		SetAdoptAvatar(false).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("login-reassign-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	reloadedPrevious, err := client.IdentityAdoptionDecision.Get(ctx, previousDecision.ID)
	require.NoError(t, err)
	require.Nil(t, reloadedPrevious.IdentityID)

	currentDecision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, currentDecision.IdentityID)
	require.Equal(t, existingIdentity.ID, *currentDecision.IdentityID)

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionLoginWithoutDecisionStillBindsIdentity(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("login-nodecision@example.com").
		SetUsername("legacy-name").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("login-nodecision-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("login-nodecision-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("login-nodecision-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "login-nodecision-user",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token": "access-token",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("login-nodecision-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("login-nodecision-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, identity.UserID)

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionExistingLoginWithSuggestedProfileSkipsAdoptionPrompt(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("existing-login@example.com").
		SetUsername("existing-login-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(userEntity.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("existing-login-123").
		SetMetadata(map[string]any{
			"username": "existing-login-user",
		}).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("existing-login-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("existing-login-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("existing-login-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Existing Login Example",
			"suggested_avatar_url":   "https://cdn.example/existing-login.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token":  "legacy-access-token",
			"refresh_token": "legacy-refresh-token",
			"expires_in":    float64(3600),
			"token_type":    "Bearer",
			"redirect":      "/dashboard",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("existing-login-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	payload := decodeJSONResponseData(t, recorder)
	require.NotEmpty(t, payload["access_token"])
	require.NotEmpty(t, payload["refresh_token"])
	require.NotEqual(t, "legacy-access-token", payload["access_token"])
	require.NotEqual(t, "legacy-refresh-token", payload["refresh_token"])
	require.Equal(t, "/dashboard", payload["redirect"])
	require.Equal(t, "Existing Login Example", payload["suggested_display_name"])
	require.Equal(t, "https://cdn.example/existing-login.png", payload["suggested_avatar_url"])
	require.NotContains(t, payload, "adoption_required")

	accessToken, ok := payload["access_token"].(string)
	require.True(t, ok)
	claims, err := handler.authService.ValidateToken(accessToken)
	require.NoError(t, err)
	reloadedUser, err := handler.userService.GetByID(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Equal(t, reloadedUser.TokenVersion, claims.TokenVersion)

	decisionCount, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, decisionCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)

	completion, ok := storedSession.LocalFlowState[identityhttp.OauthCompletionResponseKey].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, completion, "access_token")
	require.NotContains(t, completion, "refresh_token")
	require.NotContains(t, completion, "expires_in")
	require.NotContains(t, completion, "token_type")
	require.Equal(t, "/dashboard", completion["redirect"])
}

func TestExchangePendingOAuthCompletionBlocksBackendModeBeforeReturningTokenPayload(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			gateway.SettingKeyBackendModeEnabled: "true",
		},
	})
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("blocked@example.com").
		SetUsername("blocked-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("blocked-backend-mode-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("blocked-subject-123").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("blocked-backend-mode-browser-session-key").
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_in":    float64(3600),
			"token_type":    "Bearer",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("blocked-backend-mode-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusForbidden, recorder.Code)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionRejectsDisabledTargetUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	userEntity, err := client.User.Create().
		SetEmail("disabled-linked@example.com").
		SetUsername("disabled-linked-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusDisabled).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("disabled-linked-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("disabled-linked-subject").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("disabled-linked-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Disabled Linked User",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"redirect": "/dashboard",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("disabled-linked-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusForbidden, recorder.Code)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestExchangePendingOAuthCompletionChoiceStateDoesNotBindIdentity(t *testing.T) {
	// 回归测试：复刻"补邮箱/创建账户"路径的账号接管 0day。
	// 攻击者用自己的 OAuth 账号登录后，在 create-account 步骤提交受害者邮箱，
	// 后端发现邮箱已存在会把 pending session 转入 choice 状态并指向受害者
	// （TargetUserID=受害者、无密码/验证码证明）。此时带 adoption decision 调
	// exchange 绝不能把 OAuth identity 绑定到受害者账号。
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	victim, err := client.User.Create().
		SetEmail("victim@example.com").
		SetUsername("victim-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("choice-state-attack-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("attacker-subject-123").
		SetTargetUserID(victim.ID).
		SetResolvedEmail(victim.Email).
		SetBrowserSessionKey("choice-state-attack-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "attacker_linuxdo_user",
			"suggested_display_name": "Attacker Display Name",
			"suggested_avatar_url":   "https://cdn.example/attacker.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"step":                      identityhttp.OauthPendingChoiceStep,
			"adoption_required":         true,
			"force_email_on_signup":     true,
			"email_binding_required":    true,
			"existing_account_bindable": true,
			"email":                     victim.Email,
			"resolved_email":            victim.Email,
			"redirect":                  "/dashboard",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":true,"adopt_avatar":true}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("choice-state-attack-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	data := decodeJSONResponseData(t, recorder)
	require.NotContains(t, data, "access_token")
	require.Equal(t, identityhttp.OauthPendingChoiceStep, data["step"])

	// 攻击者的 OAuth identity 绝不能绑定到受害者账号
	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("attacker-subject-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	// 受害者资料不得被 adoption 篡改
	storedVictim, err := client.User.Get(ctx, victim.ID)
	require.NoError(t, err)
	require.Equal(t, "victim-user", storedVictim.Username)

	// session 不得被消费（攻击者无法进入下一环）
	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestNormalizePendingOAuthCompletionResponseScrubsLegacyTokenPayload(t *testing.T) {
	payload := identityhttp.NormalizePendingOAuthCompletionResponse(map[string]any{
		"access_token":  "legacy-access-token",
		"refresh_token": "legacy-refresh-token",
		"expires_in":    float64(3600),
		"token_type":    "Bearer",
		"redirect":      "/dashboard",
	})

	require.NotContains(t, payload, "access_token")
	require.NotContains(t, payload, "refresh_token")
	require.NotContains(t, payload, "expires_in")
	require.NotContains(t, payload, "token_type")
	require.Equal(t, "/dashboard", payload["redirect"])
}

func TestExchangePendingOAuthCompletionInvitationRequiredFalseFalsePersistsDecisionWithoutBinding(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, true)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("invitation-required-session-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("invitation-123").
		SetBrowserSessionKey("invitation-required-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Invite Example",
			"suggested_avatar_url":   "https://cdn.example/invite.png",
		}).
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"error": "invitation_required",
		},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("invitation-required-browser-session-key")})
	ginCtx.Request = req

	handler.ExchangePendingOAuthCompletion(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	data := decodeJSONResponseData(t, recorder)
	require.Equal(t, "invitation_required", data["error"])
	require.Equal(t, true, data["adoption_required"])

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
			authidentity.ProviderSubjectEQ("invitation-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	decision, err := client.IdentityAdoptionDecision.Query().
		Where(identityadoptiondecision.PendingAuthSessionIDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, decision.IdentityID)
	require.False(t, decision.AdoptDisplayName)
	require.False(t, decision.AdoptAvatar)

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestCreateOIDCOAuthAccountCreatesUserBindsIdentityAndConsumesSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithEmailVerification(t, false, "fresh@example.com", "246810")
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("create-account-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-create-123").
		SetBrowserSessionKey("create-account-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Fresh OIDC User",
			"suggested_avatar_url":   "https://cdn.example/fresh.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("create-account-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.NotEmpty(t, payload["access_token"])
	require.NotEmpty(t, payload["refresh_token"])
	require.Equal(t, "Bearer", payload["token_type"])

	createdUser, err := client.User.Query().Where(dbuser.EmailEQ("fresh@example.com")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, billing.StatusActive, createdUser.Status)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-create-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, createdUser.ID, identity.UserID)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestCreateOIDCOAuthAccountExistingEmailReturnsChoicePendingSessionState(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithEmailVerification(t, false, "owner@example.com", "135790")
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
		SetSessionToken("existing-email-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-existing-123").
		SetBrowserSessionKey("existing-email-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Existing OIDC User",
			"suggested_avatar_url":   "https://cdn.example/existing.png",
		}).
		SetRedirectTo("/dashboard").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","verify_code":"135790","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("existing-email-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "pending_session", payload["auth_result"])
	require.Equal(t, identityhttp.OauthIntentLogin, payload["intent"])
	require.Equal(t, "oidc", payload["provider"])
	require.Equal(t, "/dashboard", payload["redirect"])
	require.Equal(t, true, payload["adoption_required"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, payload["step"])
	require.Equal(t, "owner@example.com", payload["email"])
	require.Equal(t, "Existing OIDC User", payload["suggested_display_name"])
	require.Equal(t, "https://cdn.example/existing.png", payload["suggested_avatar_url"])

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, storedSession.Intent)
	require.NotNil(t, storedSession.TargetUserID)
	require.Equal(t, existingUser.ID, *storedSession.TargetUserID)
	require.Equal(t, "owner@example.com", storedSession.ResolvedEmail)
	require.Nil(t, storedSession.ConsumedAt)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-existing-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)
}

func TestCreateOIDCOAuthAccountExistingEmailNormalizesLegacySpacingAndCase(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithEmailVerification(t, false, "owner@example.com", "135790")
	ctx := context.Background()

	existingUser, err := client.User.Create().
		SetEmail(" Owner@Example.com ").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("existing-email-normalized-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-existing-normalized-123").
		SetBrowserSessionKey("existing-email-normalized-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Existing OIDC User",
			"suggested_avatar_url":   "https://cdn.example/existing.png",
		}).
		SetRedirectTo("/dashboard").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","verify_code":"135790","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("existing-email-normalized-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, identityhttp.OauthIntentLogin, payload["intent"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, payload["step"])

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.TargetUserID)
	require.Equal(t, existingUser.ID, *storedSession.TargetUserID)
	require.Equal(t, "owner@example.com", storedSession.ResolvedEmail)
}

func TestCreateOIDCOAuthAccountRejectsEmailOutsideRegistrationSuffixWhitelist(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		emailVerifyEnabled: true,
		emailCache: &oauthPendingFlowEmailCacheStub{
			verificationCodes: map[string]*identitycore.VerificationCodeData{
				"foo@gmail.com": {
					Code:      "135790",
					CreatedAt: time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
				},
			},
		},
		settingValues: map[string]string{
			identitycore.SettingKeyRegistrationEmailSuffixWhitelist: `["@qq.com"]`,
		},
	})
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("suffix-whitelist-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-suffix-whitelist-123").
		SetBrowserSessionKey("suffix-whitelist-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"foo@gmail.com","verify_code":"135790","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("suffix-whitelist-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	payload := decodeJSONBody(t, recorder)
	require.Equal(t, "EMAIL_SUFFIX_NOT_ALLOWED", payload["reason"])

	count, err := client.User.Query().Where(dbuser.EmailEQ("foo@gmail.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestSendPendingOAuthVerifyCodeExistingEmailReturnsBindLoginState(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithEmailVerification(t, false, "owner@example.com", "135790")
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
		SetSessionToken("existing-email-send-code-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-existing-send-code-123").
		SetBrowserSessionKey("existing-email-send-code-browser-session-key").
		SetLocalFlowState(map[string]any{identityhttp.OauthCompletionResponseKey: map[string]any{
			"step": "email_required",
		},
		}).
		SetRedirectTo("/dashboard").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/send-verify-code", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("existing-email-send-code-browser-session-key")})
	ginCtx.Request = req

	handler.SendPendingOAuthVerifyCode(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "pending_session", payload["auth_result"])
	require.Equal(t, identityhttp.OauthPendingChoiceStep, payload["step"])
	require.Equal(t, "owner@example.com", payload["email"])

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, identityhttp.OauthIntentLogin, storedSession.Intent)
	require.NotNil(t, storedSession.TargetUserID)
	require.Equal(t, existingUser.ID, *storedSession.TargetUserID)
	require.Equal(t, "owner@example.com", storedSession.ResolvedEmail)
}

func TestCreateOIDCOAuthAccountBlocksBackendModeBeforeCreatingUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		emailVerifyEnabled: true,
		emailCache: &oauthPendingFlowEmailCacheStub{
			verificationCodes: map[string]*identitycore.VerificationCodeData{
				"fresh@example.com": {
					Code:      "246810",
					CreatedAt: time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
				},
			},
		},
		settingValues: map[string]string{
			gateway.SettingKeyBackendModeEnabled: "true",
		},
	})
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("create-account-backend-mode-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-create-backend-mode-123").
		SetBrowserSessionKey("create-account-backend-mode-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("create-account-backend-mode-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusForbidden, recorder.Code)

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("fresh@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestLogoutClearsPendingOAuthAndBindCookies(t *testing.T) {
	handler, _ := newOAuthPendingFlowTestHandler(t, false)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue("pending-session-token")})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("pending-browser-key")})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthBindAccessTokenCookieName, Value: "bind-token"})
	ginCtx.Request = req

	handler.Logout(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, -1, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingSessionCookieName).MaxAge)
	require.Equal(t, -1, findCookie(recorder.Result().Cookies(), identityhttp.OauthPendingBrowserCookieName).MaxAge)
	require.Equal(t, -1, findCookie(recorder.Result().Cookies(), identityhttp.OauthBindAccessTokenCookieName).MaxAge)
}

func TestCreateOIDCOAuthAccountRollsBackCreatedUserWhenBindingFails(t *testing.T) {
	affiliateRepo := newOAuthPendingFlowAffiliateRepo()
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		invitationEnabled:  true,
		emailVerifyEnabled: true,
		emailCache: &oauthPendingFlowEmailCacheStub{
			verificationCodes: map[string]*identitycore.VerificationCodeData{
				"fresh@example.com": {
					Code:      "246810",
					CreatedAt: time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
				},
			},
		},
		settingValues: map[string]string{
			promotion.SettingKeyAffiliateEnabled: "true",
		},
		affiliateRepo: affiliateRepo,
	})
	ctx := context.Background()

	inviter, err := client.User.Create().
		SetEmail("inviter@example.com").
		SetUsername("inviter").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	affiliateRepo.setCode(t, inviter.ID, "AFFROLL")

	conflictOwner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentity.Create().
		SetUserID(conflictOwner.ID).
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-conflict-123").
		SetMetadata(map[string]any{
			"username": "owner-user",
		}).
		Save(ctx)
	require.NoError(t, err)

	invitation, err := client.RedeemCode.Create().
		SetCode("INVITE123").
		SetType(billing.RedeemTypeInvitation).
		SetStatus(billing.StatusUnused).
		SetValue(0).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("create-account-conflict-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-conflict-123").
		SetBrowserSessionKey("create-account-conflict-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123","invitation_code":"INVITE123","aff_code":"AFFROLL"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("create-account-conflict-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusConflict, recorder.Code)

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("fresh@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)
	require.Equal(t, 0, affiliateRepo.affCountFor(t, inviter.ID))

	storedInvitation, err := client.RedeemCode.Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, billing.StatusUnused, storedInvitation.Status)
	require.Nil(t, storedInvitation.UsedBy)
	require.Nil(t, storedInvitation.UsedAt)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestCreateOIDCOAuthAccountRollsBackPostBindFailureBeforeIdentityCanCommit(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		emailVerifyEnabled: true,
		emailCache: &oauthPendingFlowEmailCacheStub{
			verificationCodes: map[string]*identitycore.VerificationCodeData{
				"fresh@example.com": {
					Code:      "246810",
					CreatedAt: time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
				},
			},
		},
		userRepoOptions: oauthPendingFlowUserRepoOptions{
			rejectDeleteWhileAuthIdentityExists: true,
		},
	})
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("create-account-finalize-failure-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-finalize-failure-123").
		SetBrowserSessionKey("create-account-finalize-failure-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	pendingOAuthCreateAccountPreCommitHook = func(context.Context, *dbent.PendingAuthSession) error {
		return errors.New("forced post-bind failure")
	}
	t.Cleanup(func() {
		pendingOAuthCreateAccountPreCommitHook = nil
	})

	body := bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("create-account-finalize-failure-browser-session-key")})
	ginCtx.Request = req

	handler.CreateOIDCOAuthAccount(ginCtx)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)

	userCount, err := client.User.Query().Where(dbuser.EmailEQ("fresh@example.com")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, userCount)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-finalize-failure-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestBindOIDCOAuthLoginBindsExistingUserAndConsumesSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-login-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("bind-login-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Bound OIDC User",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-login-browser-session-key")})
	ginCtx.Request = req

	handler.BindOIDCOAuthLogin(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.NotEmpty(t, payload["access_token"])
	require.NotEmpty(t, payload["refresh_token"])
	require.Equal(t, "Bearer", payload["token_type"])

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-bind-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, existingUser.ID, identity.UserID)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}

func TestBindOIDCOAuthLoginBlocksBackendModeBeforeTokenIssue(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			gateway.SettingKeyBackendModeEnabled: "true",
		},
	})
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-login-backend-mode-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-backend-mode-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("bind-login-backend-mode-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username": "oidc_user",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-login-backend-mode-browser-session-key")})
	ginCtx.Request = req

	handler.BindOIDCOAuthLogin(ginCtx)

	require.Equal(t, http.StatusForbidden, recorder.Code)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-bind-backend-mode-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestBindOIDCOAuthLoginRejectsInvalidPasswordWithoutConsumingSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-login-invalid-password-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-invalid-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("bind-login-invalid-password-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Bound OIDC User",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","password":"wrong-password"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-login-invalid-password-browser-session-key")})
	ginCtx.Request = req

	handler.BindOIDCOAuthLogin(ginCtx)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	payload := decodeJSONBody(t, recorder)
	require.Equal(t, "INVALID_CREDENTIALS", payload["reason"])

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-bind-invalid-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestBindOIDCOAuthLoginReclaimsIdentityOwnedBySoftDeletedUser(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	oldOwnerHash, err := handler.authService.HashPassword("old-secret")
	require.NoError(t, err)
	oldOwner, err := client.User.Create().
		SetEmail("old-owner@example.com").
		SetUsername("old-owner").
		SetPasswordHash(oldOwnerHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	identity, err := client.AuthIdentity.Create().
		SetUserID(oldOwner.ID).
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-soft-deleted-123").
		SetMetadata(map[string]any{"username": "old-owner"}).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Delete().Where(dbuser.IDEQ(oldOwner.ID)).Exec(ctx)
	require.NoError(t, err)

	newOwnerHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)
	newOwner, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(newOwnerHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-login-soft-deleted-owner-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-soft-deleted-123").
		SetTargetUserID(newOwner.ID).
		SetResolvedEmail(newOwner.Email).
		SetBrowserSessionKey("bind-login-soft-deleted-owner-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"username":               "oidc_user",
			"suggested_display_name": "Recovered OIDC User",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-login-soft-deleted-owner-browser-session-key")})
	ginCtx.Request = req

	handler.BindOIDCOAuthLogin(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	identity, err = client.AuthIdentity.Get(ctx, identity.ID)
	require.NoError(t, err)
	require.Equal(t, newOwner.ID, identity.UserID)
}

func TestBindOIDCOAuthLoginAppliesFirstBindGrantOnce(t *testing.T) {
	defaultSubAssigner := &oauthPendingFlowDefaultSubAssignerStub{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			identitycore.SettingKeyAuthSourceDefaultOIDCBalance:          "12.5",
			identitycore.SettingKeyAuthSourceDefaultOIDCConcurrency:      "3",
			identitycore.SettingKeyAuthSourceDefaultOIDCSubscriptions:    `[{"plan_id":101}]`,
			identitycore.SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind: "true",
		},
		defaultSubAssigner: defaultSubAssigner,
	})
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetBalance(5).
		SetConcurrency(2).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	firstSession, err := client.PendingAuthSession.Create().
		SetSessionToken("first-bind-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-first-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("first-bind-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Bound OIDC User",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	firstBody := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	firstRecorder := httptest.NewRecorder()
	firstGinCtx, _ := gin.CreateTestContext(firstRecorder)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", firstBody)
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(firstSession.SessionToken)})
	firstReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("first-bind-browser-session-key")})
	firstGinCtx.Request = firstReq

	handler.BindOIDCOAuthLogin(firstGinCtx)

	require.Equal(t, http.StatusOK, firstRecorder.Code)

	storedUser, err := client.User.Get(ctx, existingUser.ID)
	require.NoError(t, err)
	require.Equal(t, 17.5, storedUser.Balance)
	require.Equal(t, 5, storedUser.Concurrency)
	require.Zero(t, storedUser.TotalRecharged)
	require.Len(t, defaultSubAssigner.calls, 1)
	require.Equal(t, int64(existingUser.ID), defaultSubAssigner.calls[0].UserID)
	require.Equal(t, int64(101), defaultSubAssigner.calls[0].PlanID)
	require.Equal(t, 1, countProviderGrantRecords(t, client, existingUser.ID, "oidc", "first_bind"))

	secondSession, err := client.PendingAuthSession.Create().
		SetSessionToken("second-bind-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-second-456").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("second-bind-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Second OIDC User",
			"suggested_avatar_url":   "https://cdn.example/second.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	secondBody := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	secondRecorder := httptest.NewRecorder()
	secondGinCtx, _ := gin.CreateTestContext(secondRecorder)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", secondBody)
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(secondSession.SessionToken)})
	secondReq.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("second-bind-browser-session-key")})
	secondGinCtx.Request = secondReq

	handler.BindOIDCOAuthLogin(secondGinCtx)

	require.Equal(t, http.StatusOK, secondRecorder.Code)

	storedUser, err = client.User.Get(ctx, existingUser.ID)
	require.NoError(t, err)
	require.Equal(t, 17.5, storedUser.Balance)
	require.Equal(t, 5, storedUser.Concurrency)
	require.Zero(t, storedUser.TotalRecharged)
	require.Len(t, defaultSubAssigner.calls, 1)
	require.Equal(t, 1, countProviderGrantRecords(t, client, existingUser.ID, "oidc", "first_bind"))
}

func TestResolvePendingOAuthTargetUserIDNormalizesLegacySpacingAndCase(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	_ = handler
	ctx := context.Background()

	existingUser, err := client.User.Create().
		SetEmail(" Owner@Example.com ").
		SetUsername("owner-user").
		SetPasswordHash("hash").
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("resolve-target-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-target-123").
		SetResolvedEmail("owner@example.com").
		SetBrowserSessionKey("resolve-target-browser-session-key").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	resolvedUserID, err := identitypostgres.ResolvePendingOAuthTargetUserID(ctx, client, session)
	require.NoError(t, err)
	require.Equal(t, existingUser.ID, resolvedUserID)
}

func TestBindOIDCOAuthLoginReturns2FAChallengeWhenUserHasTotp(t *testing.T) {
	totpCache := &oauthPendingFlowTotpCacheStub{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			identitycore.SettingKeyTotpEnabled: "true",
		},
		totpCache:     totpCache,
		totpEncryptor: oauthPendingFlowTotpEncryptorStub{},
	})
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)
	totpEnabledAt := time.Now().UTC().Add(-time.Hour)
	secret := "JBSWY3DPEHPK3PXP"

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		SetTotpEnabled(true).
		SetTotpSecretEncrypted(secret).
		SetTotpEnabledAt(totpEnabledAt).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("bind-login-2fa-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-bind-2fa-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("bind-login-2fa-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Bound OIDC User",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret-123","adopt_display_name":false,"adopt_avatar":false}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("bind-login-2fa-browser-session-key")})
	ginCtx.Request = req

	handler.BindOIDCOAuthLogin(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	data := decodeJSONResponseData(t, recorder)
	require.Equal(t, true, data["requires_2fa"])
	require.Equal(t, "o***r@example.com", data["user_email_masked"])
	tempToken, ok := data["temp_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, tempToken)

	loginSession, err := totpCache.GetLoginSession(ctx, tempToken)
	require.NoError(t, err)
	require.NotNil(t, loginSession)
	require.NotNil(t, loginSession.PendingOAuthBind)
	require.Equal(t, session.SessionToken, loginSession.PendingOAuthBind.PendingSessionToken)
	require.Equal(t, session.BrowserSessionKey, loginSession.PendingOAuthBind.BrowserSessionKey)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-bind-2fa-123"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identityCount)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, storedSession.ConsumedAt)
}

func TestLogin2FACompletesPendingOAuthBindAndConsumesSession(t *testing.T) {
	totpCache := &oauthPendingFlowTotpCacheStub{}
	defaultSubAssigner := &oauthPendingFlowDefaultSubAssignerStub{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			identitycore.SettingKeyTotpEnabled:                           "true",
			identitycore.SettingKeyAuthSourceDefaultOIDCBalance:          "8",
			identitycore.SettingKeyAuthSourceDefaultOIDCConcurrency:      "2",
			identitycore.SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind: "true",
		},
		defaultSubAssigner: defaultSubAssigner,
		totpCache:          totpCache,
		totpEncryptor:      oauthPendingFlowTotpEncryptorStub{},
	})
	ctx := context.Background()

	passwordHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)
	totpEnabledAt := time.Now().UTC().Add(-time.Hour)
	secret := "JBSWY3DPEHPK3PXP"

	existingUser, err := client.User.Create().
		SetEmail("owner@example.com").
		SetUsername("owner-user").
		SetPasswordHash(passwordHash).
		SetBalance(1.5).
		SetConcurrency(4).
		SetRole(identitycore.RoleUser).
		SetStatus(billing.StatusActive).
		SetTotpEnabled(true).
		SetTotpSecretEncrypted(secret).
		SetTotpEnabledAt(totpEnabledAt).
		Save(ctx)
	require.NoError(t, err)

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("login-2fa-pending-session-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-login-2fa-123").
		SetTargetUserID(existingUser.ID).
		SetResolvedEmail(existingUser.Email).
		SetBrowserSessionKey("login-2fa-browser-session-key").
		SetUpstreamIdentityClaims(map[string]any{
			"suggested_display_name": "Bound OIDC User",
			"suggested_avatar_url":   "https://cdn.example/bound.png",
		}).
		SetRedirectTo("/profile").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.IdentityAdoptionDecision.Create().
		SetPendingAuthSessionID(session.ID).
		SetAdoptDisplayName(false).
		SetAdoptAvatar(false).
		Save(ctx)
	require.NoError(t, err)

	tempToken, err := handler.totpService.CreatePendingOAuthBindLoginSession(
		ctx,
		existingUser.ID,
		existingUser.Email,
		session.SessionToken,
		session.BrowserSessionKey,
	)
	require.NoError(t, err)

	code, err := totp.GenerateCode(secret, time.Now().UTC())
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"temp_token":"` + tempToken + `","totp_code":"` + code + `"}`)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue(session.BrowserSessionKey)})
	ginCtx.Request = req

	handler.Login2FA(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := decodeJSONResponseData(t, recorder)
	require.NotEmpty(t, payload["access_token"])
	require.NotEmpty(t, payload["refresh_token"])
	accessToken, ok := payload["access_token"].(string)
	require.True(t, ok)
	claims, err := handler.authService.ValidateToken(accessToken)
	require.NoError(t, err)
	reloadedUser, err := handler.userService.GetByID(ctx, existingUser.ID)
	require.NoError(t, err)
	require.Equal(t, reloadedUser.TokenVersion, claims.TokenVersion)

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("oidc"),
			authidentity.ProviderKeyEQ("https://issuer.example"),
			authidentity.ProviderSubjectEQ("oidc-login-2fa-123"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, existingUser.ID, identity.UserID)

	storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)

	loginSession, err := totpCache.GetLoginSession(ctx, tempToken)
	require.NoError(t, err)
	require.Nil(t, loginSession)

	storedUser, err := client.User.Get(ctx, existingUser.ID)
	require.NoError(t, err)
	require.Equal(t, 9.5, storedUser.Balance)
	require.Equal(t, 6, storedUser.Concurrency)
	require.Equal(t, 1, countProviderGrantRecords(t, client, existingUser.ID, "oidc", "first_bind"))
	require.Empty(t, defaultSubAssigner.calls)
}

func newOAuthPendingFlowTestHandler(t *testing.T, invitationEnabled bool) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	return newOAuthPendingFlowTestHandlerWithOptions(t, invitationEnabled, false, nil)
}

func newOAuthPendingFlowTestHandlerWithEmailVerification(
	t *testing.T,
	invitationEnabled bool,
	email string,
	code string,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	cache := &oauthPendingFlowEmailCacheStub{
		verificationCodes: map[string]*identitycore.VerificationCodeData{
			email: {
				Code:      code,
				Attempts:  0,
				CreatedAt: time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
			},
		},
	}
	return newOAuthPendingFlowTestHandlerWithOptions(t, invitationEnabled, true, cache)
}

func newOAuthPendingFlowTestHandlerWithOptions(
	t *testing.T,
	invitationEnabled bool,
	emailVerifyEnabled bool,
	emailCache identitycore.EmailCache,
) (*authHTTPFixture, *dbent.Client) {
	return newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		invitationEnabled:  invitationEnabled,
		emailVerifyEnabled: emailVerifyEnabled,
		emailCache:         emailCache,
	})
}

type oauthPendingFlowTestHandlerOptions struct {
	invitationEnabled  bool
	emailVerifyEnabled bool
	emailCache         identitycore.EmailCache
	settingValues      map[string]string
	defaultSubAssigner identitycore.DefaultSubscriptionAssigner
	affiliateRepo      promotion.AffiliateRepository
	totpCache          identitycore.TotpCache
	totpEncryptor      identitycore.SecretEncryptor
	userRepoOptions    oauthPendingFlowUserRepoOptions
}

func newOAuthPendingFlowTestHandlerWithDependencies(
	t *testing.T,
	options oauthPendingFlowTestHandlerOptions,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:auth_oauth_pending_flow_handler?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS user_provider_default_grants (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	provider_type TEXT NOT NULL,
	grant_reason TEXT NOT NULL DEFAULT 'first_bind',
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(user_id, provider_type, grant_reason)
)`)
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS user_avatars (
	user_id INTEGER PRIMARY KEY,
	storage_provider TEXT NOT NULL,
	storage_key TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))

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
	settingValues := map[string]string{
		identitycore.SettingKeyRegistrationEnabled: "true",
		promotion.SettingKeyInvitationCodeEnabled:  boolSettingValue(options.invitationEnabled),
		identitycore.SettingKeyEmailVerifyEnabled:  boolSettingValue(options.emailVerifyEnabled),
	}
	for key, value := range options.settingValues {
		settingValues[key] = value
	}
	settingSvc := newAuthSettingsFixture(&oauthPendingFlowSettingRepoStub{values: settingValues}, cfg)
	userRepo := &oauthPendingFlowUserRepo{
		client:  client,
		options: options.userRepoOptions,
	}
	redeemRepo := &oauthPendingFlowRedeemCodeRepo{client: client}
	var emailService *identitycore.EmailChallenges
	if options.emailCache != nil {
		emailService = identitycore.NewEmailChallenges(options.emailCache, notification.NewMailer(&oauthPendingFlowSettingRepoStub{
			values: map[string]string{
				identitycore.SettingKeyEmailVerifyEnabled: boolSettingValue(options.emailVerifyEnabled),
			},
		}, smtp.New()))
	}
	affiliateRepo := options.affiliateRepo
	if affiliateRepo == nil {
		affiliateRepo = newOAuthPendingFlowAffiliateRepo()
	}
	affiliateSvc := promotion.NewAffiliateService(affiliateRepo, settingSvc.promotion, nil, nil, promotion.Runtime{})
	authSvc := identitytestkit.Auth(
		client, &identitycore.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: &oauthPendingFlowRefreshTokenCacheStub{}, Options: identitytestkit.AuthOptions(cfg), Settings: authContractSettings(settingSvc), Email: identitytestkit.Email(emailService), DefaultSubscriptions: options.defaultSubAssigner, Affiliate: identitytestkit.Affiliate(affiliateSvc)},
	)
	userSvc := identitycore.NewUserService(userRepo, nil, nil, nil, authBackgroundFixture(t))
	var totpSvc *identitycore.TotpService
	if options.totpCache != nil || options.totpEncryptor != nil {
		totpCache := options.totpCache
		if totpCache == nil {
			totpCache = &oauthPendingFlowTotpCacheStub{}
		}
		totpEncryptor := options.totpEncryptor
		if totpEncryptor == nil {
			totpEncryptor = oauthPendingFlowTotpEncryptorStub{}
		}
		totpSvc = identitycore.NewTotpService(userRepo, totpEncryptor, totpCache, settingSvc, nil, nil)
	}

	return newAuthHTTPFixture(t, &authHTTPFixture{
		authDB:      client,
		authService: authSvc,
		userService: userSvc,
		settingSvc:  settingSvc,
		totpService: totpSvc,
	}), client
}

func boolSettingValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func boolPtr(v bool) *bool {
	return &v
}

type oauthPendingFlowSettingRepoStub struct {
	values map[string]string
}

func (s *oauthPendingFlowSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

func (s *oauthPendingFlowSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

func (s *oauthPendingFlowSettingRepoStub) Set(context.Context, string, string) error {
	return nil
}

func (s *oauthPendingFlowSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *oauthPendingFlowSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (s *oauthPendingFlowSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	for key, value := range s.values {
		result[key] = value
	}
	return result, nil
}

func (s *oauthPendingFlowSettingRepoStub) Delete(context.Context, string) error {
	return nil
}

type oauthPendingFlowRefreshTokenCacheStub struct{}

type oauthPendingFlowEmailCacheStub struct {
	verificationCodes map[string]*identitycore.VerificationCodeData
}

func (s *oauthPendingFlowEmailCacheStub) GetVerificationCode(_ context.Context, email string) (*identitycore.VerificationCodeData, error) {
	if s == nil || s.verificationCodes == nil {
		return nil, nil
	}
	return s.verificationCodes[email], nil
}

func (s *oauthPendingFlowEmailCacheStub) SetVerificationCode(_ context.Context, email string, data *identitycore.VerificationCodeData, _ time.Duration) error {
	if s.verificationCodes == nil {
		s.verificationCodes = map[string]*identitycore.VerificationCodeData{}
	}
	s.verificationCodes[email] = data
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) DeleteVerificationCode(_ context.Context, email string) error {
	delete(s.verificationCodes, email)
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return nil, nil
}

func (s *oauthPendingFlowEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error {
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) GetPasswordResetToken(context.Context, string) (*identitycore.PasswordResetTokenData, error) {
	return nil, nil
}

func (s *oauthPendingFlowEmailCacheStub) SetPasswordResetToken(context.Context, string, *identitycore.PasswordResetTokenData, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

func (s *oauthPendingFlowEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

func (s *oauthPendingFlowEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *oauthPendingFlowRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

type oauthPendingFlowRedeemCodeRepo struct {
	client *dbent.Client
}

func (r *oauthPendingFlowRedeemCodeRepo) Create(context.Context, *billing.RedeemCode) error {
	panic("unexpected Create call")
}

func (r *oauthPendingFlowRedeemCodeRepo) CreateBatch(context.Context, []billing.RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (r *oauthPendingFlowRedeemCodeRepo) GetByID(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByID call")
}

func (r *oauthPendingFlowRedeemCodeRepo) GetByIDForUpdate(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByIDForUpdate call")
}

func (r *oauthPendingFlowRedeemCodeRepo) GetByCode(ctx context.Context, code string) (*billing.RedeemCode, error) {
	entity, err := r.client.RedeemCode.Query().Where(redeemcode.CodeEQ(code)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	notes := ""
	if entity.Notes != nil {
		notes = *entity.Notes
	}
	return &billing.RedeemCode{
		ID:        entity.ID,
		Code:      entity.Code,
		Type:      entity.Type,
		Value:     entity.Value,
		Status:    entity.Status,
		MaxUses:   entity.MaxUses,
		UsedCount: entity.UsedCount,
		ExpiresAt: entity.ExpiresAt,
		UsedBy:    entity.UsedBy,
		UsedAt:    entity.UsedAt,
		Notes:     notes,
		CreatedAt: entity.CreatedAt,
		PlanID:    entity.PlanID,
	}, nil
}

func (r *oauthPendingFlowRedeemCodeRepo) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return r.GetByCode(ctx, code)
}

func (r *oauthPendingFlowRedeemCodeRepo) Update(ctx context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	update := r.client.RedeemCode.UpdateOneID(code.ID).
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes)
	if code.ExpiresAt != nil {
		update = update.SetExpiresAt(*code.ExpiresAt)
	} else {
		update = update.ClearExpiresAt()
	}
	if code.UsedBy != nil {
		update = update.SetUsedBy(*code.UsedBy)
	} else {
		update = update.ClearUsedBy()
	}
	if code.UsedAt != nil {
		update = update.SetUsedAt(*code.UsedAt)
	} else {
		update = update.ClearUsedAt()
	}
	if code.PlanID != nil {
		update = update.SetPlanID(*code.PlanID)
	} else {
		update = update.ClearPlanID()
	}
	_, err := update.Save(ctx)
	return err
}

func (r *oauthPendingFlowRedeemCodeRepo) BatchUpdate(context.Context, []int64, billing.RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected BatchUpdate call")
}

func (r *oauthPendingFlowRedeemCodeRepo) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (r *oauthPendingFlowRedeemCodeRepo) Use(ctx context.Context, id, userID int64) error {
	affected, err := r.client.RedeemCode.Update().
		Where(redeemcode.IDEQ(id), redeemcode.StatusEQ(billing.StatusUnused)).
		SetStatus(billing.StatusUsed).
		SetUsedCount(1).
		SetUsedBy(userID).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrRedeemCodeUsed
	}
	return nil
}

func (r *oauthPendingFlowRedeemCodeRepo) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	if usage.UsedAt.IsZero() {
		usage.UsedAt = time.Now().UTC()
	}
	entity, err := r.client.RedeemCodeUsage.Create().
		SetRedeemCodeID(usage.RedeemCodeID).
		SetUserID(usage.UserID).
		SetUsedAt(usage.UsedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	usage.ID = entity.ID
	return nil
}

func (r *oauthPendingFlowRedeemCodeRepo) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	entity, err := r.client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.RedeemCodeIDEQ(redeemCodeID),
			redeemcodeusage.UserIDEQ(userID),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &billing.RedeemCodeUsage{
		ID:           entity.ID,
		RedeemCodeID: entity.RedeemCodeID,
		UserID:       entity.UserID,
		UsedAt:       entity.UsedAt,
	}, nil
}

func (r *oauthPendingFlowRedeemCodeRepo) List(context.Context, pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *oauthPendingFlowRedeemCodeRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (r *oauthPendingFlowRedeemCodeRepo) ListByUser(context.Context, int64, int) ([]billing.RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (r *oauthPendingFlowRedeemCodeRepo) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (r *oauthPendingFlowRedeemCodeRepo) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func decodeJSONResponseData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

func decodeJSONBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

type oauthPendingFlowAvatarRecord struct {
	StorageProvider string
	URL             string
}

func loadUserAvatarRecord(t *testing.T, client *dbent.Client, userID int64) *oauthPendingFlowAvatarRecord {
	t.Helper()

	var rows entsql.Rows
	err := client.Driver().Query(
		context.Background(),
		`SELECT storage_provider, url FROM user_avatars WHERE user_id = ?`,
		[]any{userID},
		&rows,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		require.NoError(t, rows.Err())
		return nil
	}

	var record oauthPendingFlowAvatarRecord
	require.NoError(t, rows.Scan(&record.StorageProvider, &record.URL))
	require.NoError(t, rows.Err())
	return &record
}

func countProviderGrantRecords(
	t *testing.T,
	client *dbent.Client,
	userID int64,
	providerType string,
	grantReason string,
) int {
	t.Helper()

	var rows entsql.Rows
	err := client.Driver().Query(
		context.Background(),
		`SELECT COUNT(*) FROM user_provider_default_grants WHERE user_id = ? AND provider_type = ? AND grant_reason = ?`,
		[]any{userID, providerType, grantReason},
		&rows,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	require.True(t, rows.Next())
	var count int
	require.NoError(t, rows.Scan(&count))
	require.False(t, rows.Next())
	return count
}

type oauthPendingFlowUserRepo struct {
	client  *dbent.Client
	options oauthPendingFlowUserRepoOptions
}

type oauthPendingFlowUserRepoOptions struct {
	rejectDeleteWhileAuthIdentityExists bool
}

func (r *oauthPendingFlowUserRepo) Create(ctx context.Context, user *identitycore.User) error {
	entity, err := r.client.User.Create().
		SetEmail(user.Email).
		SetUsername(user.Username).
		SetNotes(user.Notes).
		SetPasswordHash(user.PasswordHash).
		SetRole(user.Role).
		SetBalance(user.Balance).
		SetConcurrency(user.Concurrency).
		SetStatus(user.Status).
		SetNillableTotpSecretEncrypted(user.TotpSecretEncrypted).
		SetTotpEnabled(user.TotpEnabled).
		SetNillableTotpEnabledAt(user.TotpEnabledAt).
		SetTotalRecharged(user.TotalRecharged).
		SetSignupSource(user.SignupSource).
		SetNillableLastLoginAt(user.LastLoginAt).
		SetNillableLastActiveAt(user.LastActiveAt).
		Save(ctx)
	if err != nil {
		return err
	}
	user.ID = entity.ID
	user.CreatedAt = entity.CreatedAt
	user.UpdatedAt = entity.UpdatedAt
	return nil
}

func (r *oauthPendingFlowUserRepo) CreateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string) error {
	return r.Create(ctx, user)
}

func (r *oauthPendingFlowUserRepo) GetByID(ctx context.Context, id int64) (*identitycore.User, error) {
	entity, err := r.client.User.Get(ctx, id)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, identitycore.ErrUserNotFound
		}
		return nil, err
	}
	return oauthPendingFlowServiceUser(entity), nil
}

func (r *oauthPendingFlowUserRepo) GetByEmail(ctx context.Context, email string) (*identitycore.User, error) {
	entity, err := r.client.User.Query().Where(dbuser.EmailEQ(email)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, identitycore.ErrUserNotFound
		}
		return nil, err
	}
	return oauthPendingFlowServiceUser(entity), nil
}

func (r *oauthPendingFlowUserRepo) GetFirstAdmin(context.Context) (*identitycore.User, error) {
	panic("unexpected GetFirstAdmin call")
}

func (r *oauthPendingFlowUserRepo) Update(ctx context.Context, user *identitycore.User, fields identitycore.UserUpdateFields) error {
	entity, err := r.client.User.UpdateOneID(user.ID).
		SetEmail(user.Email).
		SetUsername(user.Username).
		SetNotes(user.Notes).
		SetPasswordHash(user.PasswordHash).
		SetRole(user.Role).
		SetBalance(user.Balance).
		SetConcurrency(user.Concurrency).
		SetStatus(user.Status).
		SetNillableTotpSecretEncrypted(user.TotpSecretEncrypted).
		SetTotpEnabled(user.TotpEnabled).
		SetNillableTotpEnabledAt(user.TotpEnabledAt).
		SetTotalRecharged(user.TotalRecharged).
		SetSignupSource(user.SignupSource).
		SetNillableLastLoginAt(user.LastLoginAt).
		SetNillableLastActiveAt(user.LastActiveAt).
		Save(ctx)
	if err != nil {
		return err
	}
	user.UpdatedAt = entity.UpdatedAt
	return nil
}

func (r *oauthPendingFlowUserRepo) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string, fields identitycore.UserUpdateFields) error {
	return r.Update(ctx, user, fields)
}

func (r *oauthPendingFlowUserRepo) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	return r.client.User.UpdateOneID(userID).SetLastActiveAt(activeAt).Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) Delete(ctx context.Context, id int64) error {
	if r.options.rejectDeleteWhileAuthIdentityExists {
		count, err := r.client.AuthIdentity.Query().Where(authidentity.UserIDEQ(id)).Count(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.New("cannot delete user while auth identities still exist")
		}
	}
	return r.client.User.DeleteOneID(id).Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) GetUserAvatar(ctx context.Context, userID int64) (*identitycore.UserAvatar, error) {
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var rows entsql.Rows
	if err := driver.Query(
		ctx,
		`SELECT storage_provider, storage_key, url, content_type, byte_size, sha256 FROM user_avatars WHERE user_id = ?`,
		[]any{userID},
		&rows,
	); err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, rows.Err()
	}

	var avatar identitycore.UserAvatar
	if err := rows.Scan(
		&avatar.StorageProvider,
		&avatar.StorageKey,
		&avatar.URL,
		&avatar.ContentType,
		&avatar.ByteSize,
		&avatar.SHA256,
	); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &avatar, nil
}

func (r *oauthPendingFlowUserRepo) UpsertUserAvatar(ctx context.Context, userID int64, input identitycore.UpsertUserAvatarInput) (*identitycore.UserAvatar, error) {
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var result entsql.Result
	if err := driver.Exec(
		ctx,
		`INSERT INTO user_avatars (user_id, storage_provider, storage_key, url, content_type, byte_size, sha256, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET
	storage_provider = excluded.storage_provider,
	storage_key = excluded.storage_key,
	url = excluded.url,
	content_type = excluded.content_type,
	byte_size = excluded.byte_size,
	sha256 = excluded.sha256,
	updated_at = CURRENT_TIMESTAMP`,
		[]any{
			userID,
			input.StorageProvider,
			input.StorageKey,
			input.URL,
			input.ContentType,
			input.ByteSize,
			input.SHA256,
		},
		&result,
	); err != nil {
		return nil, err
	}

	return &identitycore.UserAvatar{
		StorageProvider: input.StorageProvider,
		StorageKey:      input.StorageKey,
		URL:             input.URL,
		ContentType:     input.ContentType,
		ByteSize:        input.ByteSize,
		SHA256:          input.SHA256,
	}, nil
}

func (r *oauthPendingFlowUserRepo) DeleteUserAvatar(ctx context.Context, userID int64) error {
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var result entsql.Result
	return driver.Exec(ctx, `DELETE FROM user_avatars WHERE user_id = ?`, []any{userID}, &result)
}

func (r *oauthPendingFlowUserRepo) List(context.Context, pagination.PaginationParams) ([]identitycore.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *oauthPendingFlowUserRepo) ListWithFilters(context.Context, pagination.PaginationParams, identitycore.UserListFilters) ([]identitycore.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (r *oauthPendingFlowUserRepo) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}

func (r *oauthPendingFlowUserRepo) AddBalance(ctx context.Context, id int64, amount float64) error {
	return r.client.User.UpdateOneID(id).AddBalance(amount).Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	if amount == 0 {
		return 0, nil
	}
	if err := r.client.User.UpdateOneID(id).AddBalance(-amount).Exec(ctx); err != nil {
		return 0, err
	}
	return amount, nil
}

func (r *oauthPendingFlowUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (identitycore.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (r *oauthPendingFlowUserRepo) SetBalance(ctx context.Context, id int64, value float64) (identitycore.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

func (r *oauthPendingFlowUserRepo) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}

func (r *oauthPendingFlowUserRepo) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

func (r *oauthPendingFlowUserRepo) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}
func (r *oauthPendingFlowUserRepo) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

func (r *oauthPendingFlowUserRepo) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

func (r *oauthPendingFlowUserRepo) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

func (r *oauthPendingFlowUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	count, err := r.client.User.Query().Where(dbuser.EmailEQ(email)).Count(ctx)
	return count > 0, err
}

func (r *oauthPendingFlowUserRepo) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	users, err := r.client.User.Query().All(ctx)
	if err != nil {
		return false, err
	}
	for _, user := range users {
		if identitycore.NormalizeRegistrationEmailAddress(user.Email) == normalizedEmail {
			return true, nil
		}
	}
	return false, nil
}

func (r *oauthPendingFlowUserRepo) LockRegistrationEmail(context.Context, string) error {
	return nil
}

func (r *oauthPendingFlowUserRepo) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (r *oauthPendingFlowUserRepo) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (r *oauthPendingFlowUserRepo) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (r *oauthPendingFlowUserRepo) ListUserAuthIdentities(ctx context.Context, userID int64) ([]identitycore.UserAuthIdentityRecord, error) {
	identities, err := r.client.AuthIdentity.Query().
		Where(authidentity.UserIDEQ(userID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	records := make([]identitycore.UserAuthIdentityRecord, 0, len(identities))
	for _, identity := range identities {
		if identity == nil {
			continue
		}
		records = append(records, identitycore.UserAuthIdentityRecord{
			ProviderType:    identity.ProviderType,
			ProviderKey:     identity.ProviderKey,
			ProviderSubject: identity.ProviderSubject,
			VerifiedAt:      identity.VerifiedAt,
			Issuer:          identity.Issuer,
			Metadata:        identity.Metadata,
			CreatedAt:       identity.CreatedAt,
			UpdatedAt:       identity.UpdatedAt,
		})
	}
	return records, nil
}

func (r *oauthPendingFlowUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

func (r *oauthPendingFlowUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	update := r.client.User.UpdateOneID(userID)
	if encryptedSecret == nil {
		update = update.ClearTotpSecretEncrypted()
	} else {
		update = update.SetTotpSecretEncrypted(*encryptedSecret)
	}
	return update.Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	return r.client.User.UpdateOneID(userID).
		SetTotpEnabled(true).
		SetTotpEnabledAt(time.Now().UTC()).
		Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	return r.client.User.UpdateOneID(userID).
		SetTotpEnabled(false).
		ClearTotpSecretEncrypted().
		ClearTotpEnabledAt().
		Exec(ctx)
}

func (r *oauthPendingFlowUserRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identitycore.User, error) {
	return r.GetByID(ctx, id)
}

func oauthPendingFlowServiceUser(entity *dbent.User) *identitycore.User {
	if entity == nil {
		return nil
	}
	return &identitycore.User{
		ID:                  entity.ID,
		Email:               entity.Email,
		Username:            entity.Username,
		Notes:               entity.Notes,
		PasswordHash:        entity.PasswordHash,
		Role:                entity.Role,
		Balance:             entity.Balance,
		Concurrency:         entity.Concurrency,
		Status:              entity.Status,
		SignupSource:        entity.SignupSource,
		LastLoginAt:         entity.LastLoginAt,
		LastActiveAt:        entity.LastActiveAt,
		TotpSecretEncrypted: entity.TotpSecretEncrypted,
		TotpEnabled:         entity.TotpEnabled,
		TotpEnabledAt:       entity.TotpEnabledAt,
		TotalRecharged:      entity.TotalRecharged,
		CreatedAt:           entity.CreatedAt,
		UpdatedAt:           entity.UpdatedAt,
	}
}

type oauthPendingFlowDefaultSubAssignerStub struct {
	calls []billing.AssignSubscriptionInput
}

type oauthPendingFlowAffiliateRepo struct {
	profiles map[int64]*promotion.AffiliateSummary
	byCode   map[string]int64
}

func newOAuthPendingFlowAffiliateRepo() *oauthPendingFlowAffiliateRepo {
	return &oauthPendingFlowAffiliateRepo{
		profiles: make(map[int64]*promotion.AffiliateSummary),
		byCode:   make(map[string]int64),
	}
}

func (r *oauthPendingFlowAffiliateRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*promotion.AffiliateSummary, error) {
	if userID <= 0 {
		return nil, identitycore.ErrUserNotFound
	}
	if profile := r.profiles[userID]; profile != nil {
		cloned := *profile
		return &cloned, nil
	}
	code := "AFF" + strconv.FormatInt(userID, 10)
	profile := &promotion.AffiliateSummary{
		UserID:    userID,
		AffCode:   code,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	r.profiles[userID] = profile
	r.byCode[code] = userID
	cloned := *profile
	return &cloned, nil
}

func (r *oauthPendingFlowAffiliateRepo) GetAffiliateByCode(_ context.Context, code string) (*promotion.AffiliateSummary, error) {
	userID := r.byCode[strings.ToUpper(strings.TrimSpace(code))]
	if userID <= 0 {
		return nil, promotion.ErrAffiliateProfileNotFound
	}
	return r.EnsureUserAffiliate(context.Background(), userID)
}

func (r *oauthPendingFlowAffiliateRepo) BindInviter(_ context.Context, userID, inviterID int64) (bool, error) {
	profile, err := r.EnsureUserAffiliate(context.Background(), userID)
	if err != nil {
		return false, err
	}
	inviter, err := r.EnsureUserAffiliate(context.Background(), inviterID)
	if err != nil {
		return false, err
	}
	if profile.InviterID != nil {
		return false, nil
	}
	stored := r.profiles[userID]
	stored.InviterID = &inviterID
	stored.UpdatedAt = time.Now().UTC()
	r.profiles[inviterID].AffCount = inviter.AffCount + 1
	return true, nil
}

func (r *oauthPendingFlowAffiliateRepo) setCode(t *testing.T, userID int64, code string) {
	t.Helper()
	profile, err := r.EnsureUserAffiliate(context.Background(), userID)
	require.NoError(t, err)
	delete(r.byCode, profile.AffCode)
	normalized := strings.ToUpper(strings.TrimSpace(code))
	stored := r.profiles[userID]
	stored.AffCode = normalized
	stored.AffCodeCustom = true
	r.byCode[normalized] = userID
}

func (r *oauthPendingFlowAffiliateRepo) inviterIDFor(t *testing.T, userID int64) int64 {
	t.Helper()
	profile := r.profiles[userID]
	require.NotNil(t, profile)
	require.NotNil(t, profile.InviterID)
	return *profile.InviterID
}

func (r *oauthPendingFlowAffiliateRepo) affCountFor(t *testing.T, userID int64) int {
	t.Helper()
	profile := r.profiles[userID]
	require.NotNil(t, profile)
	return profile.AffCount
}

func (r *oauthPendingFlowAffiliateRepo) AccrueQuota(context.Context, int64, int64, float64, int, *int64) (bool, error) {
	panic("unexpected AccrueQuota call")
}

func (r *oauthPendingFlowAffiliateRepo) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	panic("unexpected GetAccruedRebateFromInvitee call")
}

func (r *oauthPendingFlowAffiliateRepo) ThawFrozenQuota(context.Context, int64) (float64, error) {
	return 0, nil
}

func (r *oauthPendingFlowAffiliateRepo) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

func (r *oauthPendingFlowAffiliateRepo) ListInvitees(context.Context, int64, int) ([]promotion.AffiliateInvitee, error) {
	return nil, nil
}

func (r *oauthPendingFlowAffiliateRepo) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

func (r *oauthPendingFlowAffiliateRepo) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

func (r *oauthPendingFlowAffiliateRepo) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

func (r *oauthPendingFlowAffiliateRepo) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

func (r *oauthPendingFlowAffiliateRepo) ListUsersWithCustomSettings(context.Context, promotion.AffiliateAdminFilter) ([]promotion.AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

func (r *oauthPendingFlowAffiliateRepo) ListAffiliateInviteRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

func (r *oauthPendingFlowAffiliateRepo) ListAffiliateRebateRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

func (r *oauthPendingFlowAffiliateRepo) ListAffiliateTransferRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

func (r *oauthPendingFlowAffiliateRepo) GetAffiliateUserOverview(context.Context, int64) (*promotion.AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

func (s *oauthPendingFlowDefaultSubAssignerStub) AssignOrExtendSubscription(
	_ context.Context,
	input *billing.AssignSubscriptionInput,
) (*billing.UserSubscription, bool, error) {
	if input != nil {
		s.calls = append(s.calls, *input)
	}
	return nil, false, nil
}

type oauthPendingFlowTotpCacheStub struct {
	setupSessions  map[int64]*identitycore.TotpSetupSession
	loginSessions  map[string]*identitycore.TotpLoginSession
	verifyAttempts map[int64]int
}

func (s *oauthPendingFlowTotpCacheStub) GetSetupSession(_ context.Context, userID int64) (*identitycore.TotpSetupSession, error) {
	if s == nil || s.setupSessions == nil {
		return nil, nil
	}
	return s.setupSessions[userID], nil
}

func (s *oauthPendingFlowTotpCacheStub) SetSetupSession(_ context.Context, userID int64, session *identitycore.TotpSetupSession, _ time.Duration) error {
	if s.setupSessions == nil {
		s.setupSessions = map[int64]*identitycore.TotpSetupSession{}
	}
	s.setupSessions[userID] = session
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) DeleteSetupSession(_ context.Context, userID int64) error {
	delete(s.setupSessions, userID)
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) GetLoginSession(_ context.Context, tempToken string) (*identitycore.TotpLoginSession, error) {
	if s == nil || s.loginSessions == nil {
		return nil, nil
	}
	return s.loginSessions[tempToken], nil
}

func (s *oauthPendingFlowTotpCacheStub) SetLoginSession(_ context.Context, tempToken string, session *identitycore.TotpLoginSession, _ time.Duration) error {
	if s.loginSessions == nil {
		s.loginSessions = map[string]*identitycore.TotpLoginSession{}
	}
	s.loginSessions[tempToken] = session
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) DeleteLoginSession(_ context.Context, tempToken string) error {
	delete(s.loginSessions, tempToken)
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) IncrementVerifyAttempts(_ context.Context, userID int64) (int, error) {
	if s.verifyAttempts == nil {
		s.verifyAttempts = map[int64]int{}
	}
	s.verifyAttempts[userID]++
	return s.verifyAttempts[userID], nil
}

func (s *oauthPendingFlowTotpCacheStub) GetVerifyAttempts(_ context.Context, userID int64) (int, error) {
	if s == nil || s.verifyAttempts == nil {
		return 0, nil
	}
	return s.verifyAttempts[userID], nil
}

func (s *oauthPendingFlowTotpCacheStub) ClearVerifyAttempts(_ context.Context, userID int64) error {
	delete(s.verifyAttempts, userID)
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) SetStepUpGrant(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

func (s *oauthPendingFlowTotpCacheStub) HasStepUpGrant(_ context.Context, _ int64, _ string) (bool, error) {
	return false, nil
}

type oauthPendingFlowTotpEncryptorStub struct{}

func (oauthPendingFlowTotpEncryptorStub) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (oauthPendingFlowTotpEncryptorStub) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

// ConsumeRefreshToken 与此桩始终未找到凭据的读取行为一致。
func (s *oauthPendingFlowRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}

// 替身同步执行事务回调；真实行锁行为由 PostgreSQL 集成验证。
func (r *oauthPendingFlowAffiliateRepo) WithLockedInviter(ctx context.Context, _ int64, fn func(context.Context) error) error {
	return fn(ctx)
}
