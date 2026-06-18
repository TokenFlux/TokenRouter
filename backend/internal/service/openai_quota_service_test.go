package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/model"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaServiceQueryUsageUsesCodexHeaders(t *testing.T) {
	account := &Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 3,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"user_id":"user-1","rate_limit_reset_credits":{"available_count":2}}`),
	}}
	svc := NewOpenAIQuotaService(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	usage, err := svc.QueryUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "user-1", usage.UserID)
	require.NotNil(t, usage.RateLimitResetCredits)
	require.Equal(t, 2, usage.RateLimitResetCredits.AvailableCount)
	require.Greater(t, usage.FetchedAt, int64(0))

	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "/backend-api/wham/usage", req.URL.Path)
	require.Equal(t, "Bearer oauth-token", req.Header.Get("Authorization"))
	require.Equal(t, "Codex Desktop", req.Header.Get("originator"))
	require.Equal(t, codexInviteResetDefaultUserAgent, req.Header.Get("User-Agent"))
	require.Equal(t, "chatgpt-acc", req.Header.Get("chatgpt-account-id"))
	require.Equal(t, "1", req.Header.Get("X-OpenAI-Attach-Auth"))
	require.Equal(t, "1", req.Header.Get("X-OpenAI-Attach-Integrity-State"))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(req.Context()))
}

func TestOpenAIQuotaServiceResetCreditSendsCreditIDAndRedeemRequestID(t *testing.T) {
	account := &Account{
		ID:       9,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"available_count":2,"credits":[{"id":"spent","status":"redeemed"},{"id":"credit-1","status":"available"}]}`),
		codexInviteResetJSONResponse(`{"code":"reset","windows_reset":1}`),
	}}
	svc := NewOpenAIQuotaService(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	result, err := svc.ResetCredit(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "reset", result.Code)
	require.Equal(t, 1, result.WindowsReset)

	require.Len(t, upstream.requests, 2)
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", upstream.requests[0].URL.Path)
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits/consume", upstream.requests[1].URL.Path)
	require.Equal(t, "application/json", upstream.requests[1].Header.Get("Content-Type"))
	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(upstream.bodies[1]), &payload))
	require.Equal(t, "credit-1", payload["credit_id"])
	require.NotEmpty(t, payload["redeem_request_id"])
	require.Contains(t, payload["redeem_request_id"], "-")
}

func TestOpenAIQuotaServiceResetCreditRejectsNoAvailableCredit(t *testing.T) {
	account := &Account{
		ID:       10,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"available_count":0,"credits":[{"id":"spent","status":"redeemed"}]}`),
	}}
	svc := NewOpenAIQuotaService(codexInviteResetAdminServiceStub{account: account}, upstream, nil, nil, nil)

	_, err := svc.ResetCredit(context.Background(), account.ID)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "OPENAI_QUOTA_NO_AVAILABLE_RESET_CREDIT", infraerrors.Reason(err))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", upstream.requests[0].URL.Path)
}

func TestOpenAIQuotaServiceUsesTLSRouterInviteResetSettings(t *testing.T) {
	quotaProfileID := int64(20)
	account := &Account{
		ID:       44,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	upstream := &codexInviteResetHTTPUpstreamStub{responses: []*http.Response{
		codexInviteResetJSONResponse(`{"rate_limit_reset_credits":{"available_count":0}}`),
	}}
	routerReader := &openAIOAuthTokenRouterReaderStub{routers: map[int64]*model.TLSFingerprintRouter{
		9: {
			ID:                                      9,
			Enabled:                                 true,
			CodexInviteResetUserAgent:               " Codex Desktop/0.135.0-alpha.1 (Windows 10.0.26200; x86_64) ",
			CodexInviteResetTLSFingerprintProfileID: &quotaProfileID,
		},
	}}
	profileService := &TLSFingerprintProfileService{
		localCache: map[int64]*model.TLSFingerprintProfile{
			10: {ID: 10, Name: "account-fixed"},
			20: {ID: 20, Name: "router-token"},
		},
	}
	svc := NewOpenAIQuotaService(codexInviteResetAdminServiceStub{account: account}, upstream, nil, profileService, routerReader)

	_, err := svc.QueryUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Codex Desktop/0.135.0-alpha.1 (Windows 10.0.26200; x86_64)", upstream.requests[0].Header.Get("User-Agent"))
	require.Len(t, upstream.profiles, 1)
	require.NotNil(t, upstream.profiles[0])
	require.Equal(t, "router-token", upstream.profiles[0].Name)
}

func TestOpenAIQuotaServiceRejectsUnsupportedAccount(t *testing.T) {
	account := &Account{
		ID:       7,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}
	svc := NewOpenAIQuotaService(codexInviteResetAdminServiceStub{account: account}, &codexInviteResetHTTPUpstreamStub{}, nil, nil, nil)

	_, err := svc.QueryUsage(context.Background(), account.ID)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "OPENAI_QUOTA_UNSUPPORTED_ACCOUNT", infraerrors.Reason(err))
	require.False(t, strings.Contains(err.Error(), "sk-test"))
}
