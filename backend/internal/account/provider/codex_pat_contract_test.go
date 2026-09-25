package provider_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAITokenProvider_PersonalAccessTokenDoesNotExpireLikeOAuth(t *testing.T) {
	expiresAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	account := &acctcore.Record{LoadLocation: time.LoadLocation, ID: 4001,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "at-expired-metadata",
			"auth_mode":          acctcore.OpenAIAuthModePersonalAccessToken,
			"openai_auth_mode":   "personal_access_token",
			"expires_at":         expiresAt,
			"chatgpt_account_id": "acct-123",
		}}

	provider := &acctcore.OpenAITokenSource{Metrics: &acctcore.OpenAITokenMetricsStore{}, Policy: acctcore.OpenAIProviderRefreshPolicy(), Debug: slog.Debug, Warn: slog.Warn}
	token, err := provider.GetAccessToken(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "at-expired-metadata", token)
}

func TestSetOpenAIChatGPTAccountHeadersAddsFedRAMP(t *testing.T) {
	account := &acctcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id":         "acct-fed",
			"chatgpt_account_is_fedramp": true,
		}}
	headers := make(http.Header)
	accountprovider.SetChatGPTAccountHeaders(headers, account)

	require.Equal(t, "acct-fed", headers.Get("chatgpt-account-id"))
	require.Equal(t, "true", headers.Get("x-openai-fedramp"))
}
