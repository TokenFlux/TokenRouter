package provider

import (
	"context"
	"encoding/json"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

func TestCodexInviteResetServiceGetStatusAggregatesDesktopEndpoints(t *testing.T) {
	account := &accountcore.Record{
		ID:          42,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 3,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"requires_explicit_confirmation":true,"should_show":false,"has_rewards":true,"grant_action":"rate_limit_reset_credit","grant_amount":3}`),
		codexInviteResetJSONResponse(`{"rules":[{"text":"friend must send first Codex message"}]}`),
		codexInviteResetJSONResponse(`{"rate_limit_reset_credits":{"available_count":2}}`),
		codexInviteResetJSONResponse(`{"available_count":2,"credits":[{"id":"credit-1","status":"available","title":"Reset","reset_type":"primary","granted_at":"2026-07-01T04:05:06Z"},{"id":"credit-2","status":"available"}]}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	status, err := svc.GetStatus(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "codex_referral_persistent_invite", status.ReferralKey)
	require.Equal(t, 2, status.AvailableCount)
	require.True(t, status.RequiresConsent)
	require.NotNil(t, status.ShouldShow)
	require.False(t, *status.ShouldShow)
	require.Equal(t, "rate_limit_reset_credit", status.GrantAction)
	require.NotNil(t, status.GrantAmount)
	require.Equal(t, 3, *status.GrantAmount)
	require.NotNil(t, status.HasRewards)
	require.True(t, *status.HasRewards)
	require.Equal(t, "rate_limit_reset", status.GrantType)
	require.Len(t, status.Credits, 2)
	require.Equal(t, "primary", status.Credits[0].ResetType)
	require.Equal(t, "2026-07-01T04:05:06Z", status.Credits[0].GrantedAt)
	require.Equal(t, "friend must send first Codex message", status.EligibilityRules[0])

	require.Len(t, upstream.requests, 4)
	require.Equal(t, "/backend-api/referrals/invite/eligibility", upstream.requests[0].URL.Path)
	require.Equal(t, "codex_referral_persistent_invite", upstream.requests[0].URL.Query().Get("referral_key"))
	require.Equal(t, "true", upstream.requests[0].URL.Query().Get("supports_rewardless_invites"))
	require.Equal(t, "/backend-api/wham/referrals/eligibility_rules", upstream.requests[1].URL.Path)
	require.Equal(t, "/backend-api/wham/usage", upstream.requests[2].URL.Path)
	require.Equal(t, "true", upstream.requests[2].URL.Query().Get("supports_rewardless_invites"))
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", upstream.requests[3].URL.Path)
	require.Equal(t, "Bearer oauth-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Codex Desktop", upstream.requests[0].Header.Get("originator"))
	require.Equal(t, openai.CodexInviteDefaultUserAgent, upstream.requests[0].Header.Get("User-Agent"))
	require.Equal(t, "1", upstream.requests[0].Header.Get("X-OpenAI-Attach-Auth"))
	require.Equal(t, "1", upstream.requests[0].Header.Get("X-OpenAI-Attach-Integrity-State"))
	require.Equal(t, "none", upstream.requests[0].Header.Get("sec-fetch-site"))
	require.Equal(t, "no-cors", upstream.requests[0].Header.Get("sec-fetch-mode"))
	require.Equal(t, "empty", upstream.requests[0].Header.Get("sec-fetch-dest"))
	require.Equal(t, "u=4, i", upstream.requests[0].Header.Get("priority"))
	require.Equal(t, "chatgpt-acc", upstream.requests[0].Header.Get("chatgpt-account-id"))
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.requests[0].Context()))
}

func TestCodexInviteResetServiceGetStatusKeepsUsageCreditsWhenEligibilityReturns422AndDetailsFail(t *testing.T) {
	account := &accountcore.Record{
		ID:       50,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONStatusResponse(http.StatusUnprocessableEntity, `{"detail":[{"type":"missing","loc":["query","legacy_field"],"msg":"Field required"}]}`),
		codexInviteResetJSONResponse(`{"rules":[]}`),
		codexInviteResetJSONResponse(`{"rate_limit_reset_credits":{"available_count":1}}`),
		codexInviteResetJSONStatusResponse(http.StatusUnauthorized, `{"detail":"expired"}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	status, err := svc.GetStatus(context.Background(), account.ID)
	require.NoError(t, err)
	require.False(t, status.InviteAvailable)
	require.Equal(t, "CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE", status.InviteUnavailableReason)
	require.Equal(t, "当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用", status.InviteUnavailableMessage)
	require.Equal(t, 1, status.AvailableCount)
	require.Empty(t, status.Credits)
	require.NotContains(t, status.InviteUnavailableMessage, "Field required")
	require.Len(t, upstream.requests, 4)
	require.Equal(t, "/backend-api/wham/usage", upstream.requests[2].URL.Path)
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", upstream.requests[3].URL.Path)
}

func TestCodexInviteResetServiceSendInviteNormalizesEmails(t *testing.T) {
	account := &accountcore.Record{
		ID:          7,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"invites":[{"email":"a@example.com"}],"message":"ok"}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	result, err := svc.SendInvite(context.Background(), account.ID, []string{"a@example.com, b@example.com", "A@example.com"})
	require.NoError(t, err)
	require.Equal(t, "ok", result.Message)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/backend-api/wham/referrals/invite", upstream.requests[0].URL.Path)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(upstream.bodies[0]), &payload))
	require.Equal(t, "codex_referral_persistent_invite", payload["referral_key"])
	require.Equal(t, []any{"a@example.com", "b@example.com"}, payload["emails"])
}

func TestCodexInviteResetServiceSendInviteMapsUnavailableInvite(t *testing.T) {
	account := &accountcore.Record{
		ID:          8,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONStatusResponse(http.StatusForbidden, `{"detail":"该推荐码对应的推荐邀请不可用"}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	result, err := svc.SendInvite(context.Background(), account.ID, []string{"a@example.com"})
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, s15httpx.ErrorCode(err))
	require.Equal(t, "CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE", apperror.Reason(err))
	require.Equal(t, "当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用", apperror.Message(err))
	require.Equal(t, "该推荐码对应的推荐邀请不可用", apperror.FromError(err).Metadata["upstream_detail"])
}

func TestCodexInviteResetServiceConsumeSendsRedeemRequestID(t *testing.T) {
	account := &accountcore.Record{
		ID:          9,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"code":"reset","available_count":0,"windows_reset":2}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	result, err := svc.Consume(context.Background(), account.ID, "credit-1")
	require.NoError(t, err)
	require.Equal(t, "reset", result.Code)
	require.Equal(t, "credit-1", result.CreditID)
	require.NotEmpty(t, result.RedeemRequestID)
	require.Equal(t, 2, result.WindowsReset)
	require.NotNil(t, result.AvailableCount)
	require.Equal(t, 0, *result.AvailableCount)

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(upstream.bodies[0]), &payload))
	require.Equal(t, "credit-1", payload["credit_id"])
	require.Equal(t, result.RedeemRequestID, payload["redeem_request_id"])
}

func TestCodexInviteResetServiceConsumeAllowsAutomaticCreditSelection(t *testing.T) {
	account := &accountcore.Record{
		ID:          10,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"code":"reset","windows_reset":1}`),
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	result, err := svc.Consume(context.Background(), account.ID, "")
	require.NoError(t, err)
	require.Empty(t, result.CreditID)
	require.Equal(t, 1, result.WindowsReset)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(upstream.bodies[0]), &payload))
	require.NotEmpty(t, payload["redeem_request_id"])
	require.NotContains(t, payload, "credit_id")
}

func TestCodexInviteResetServiceUsesTLSRouterInviteResetUserAgent(t *testing.T) {
	account := &accountcore.Record{
		ID:          43,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
		Extra: map[string]any{
			"tls_fingerprint_router_id": int64(9),
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"requires_explicit_confirmation":true}`),
		codexInviteResetJSONResponse(`{"rules":[]}`),
		codexInviteResetJSONResponse(`{"available_count":0,"credits":[]}`),
	}}
	routerReader := &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		9: {
			ID:                        9,
			Enabled:                   true,
			CodexInviteResetUserAgent: " Codex Desktop/0.135.0-alpha.1 (Windows 10.0.26200; x86_64) ",
		},
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, routerReader)

	_, err := svc.GetStatus(context.Background(), account.ID)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "Codex Desktop/0.135.0-alpha.1 (Windows 10.0.26200; x86_64)", upstream.requests[0].Header.Get("User-Agent"))
}

func TestCodexInviteResetServiceDoesNotReuseTokenUserAgent(t *testing.T) {
	account := &accountcore.Record{
		ID:          45,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
		Extra: map[string]any{
			"tls_fingerprint_router_id": int64(9),
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"requires_explicit_confirmation":true}`),
		codexInviteResetJSONResponse(`{"rules":[]}`),
		codexInviteResetJSONResponse(`{"available_count":0,"credits":[]}`),
	}}
	routerReader := &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		9: {
			ID:                         9,
			Enabled:                    true,
			ChatGPTOAuthTokenUserAgent: "codex-tui/0.135.0 (Windows 10.0.26200; x86_64)",
		},
	}}
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, routerReader)

	_, err := svc.GetStatus(context.Background(), account.ID)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, openai.CodexInviteDefaultUserAgent, upstream.requests[0].Header.Get("User-Agent"))
}

func TestCodexInviteResetServiceUsesTLSRouterInviteResetTLSProfile(t *testing.T) {
	inviteResetProfileID := int64(20)
	account := &accountcore.Record{
		ID:          44,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"requires_explicit_confirmation":true}`),
		codexInviteResetJSONResponse(`{"rules":[]}`),
		codexInviteResetJSONResponse(`{"available_count":0,"credits":[]}`),
	}}
	routerReader := &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		9: {
			ID:                                      9,
			Enabled:                                 true,
			CodexInviteResetTLSFingerprintProfileID: &inviteResetProfileID,
		},
	}}
	profileService := newTLSProfileServiceWithCacheForTest(map[int64]*egress.TLSFingerprintProfile{
		10: {ID: 10, Name: "account-fixed"},
		20: {ID: 20, Name: "router-token"},
	})
	svc := newCodexInviteForTest(codexInviteResetAdminServiceStub{account: account}, upstream, nil, profileService, routerReader)

	_, err := svc.GetStatus(context.Background(), account.ID)
	require.NoError(t, err)
	require.Len(t, upstream.profiles, 3)
	require.NotNil(t, upstream.profiles[0])
	require.Equal(t, "router-token", upstream.profiles[0].Name)
}
