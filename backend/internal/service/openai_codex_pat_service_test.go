package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAITokenProvider_PersonalAccessTokenDoesNotExpireLikeOAuth(t *testing.T) {
	expiresAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4001,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "at-expired-metadata",
			"auth_mode":          accountcore.OpenAIAuthModePersonalAccessToken,
			"openai_auth_mode":   "personal_access_token",
			"expires_at":         expiresAt,
			"chatgpt_account_id": "acct-123",
		}},
	}

	provider := newOpenAITokenSourceForTest(nil, nil, nil)
	token, err := provider.GetAccessToken(context.Background(), gatewayprovider.ExecutionRecord(account))

	require.NoError(t, err)
	require.Equal(t, "at-expired-metadata", token)
}

func TestSetOpenAIChatGPTAccountHeadersAddsFedRAMP(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id":         "acct-fed",
			"chatgpt_account_is_fedramp": true,
		}},
	}
	headers := make(http.Header)
	accountprovider.SetChatGPTAccountHeaders(headers, account.View())

	require.Equal(t, "acct-fed", headers.Get("chatgpt-account-id"))
	require.Equal(t, "true", headers.Get("x-openai-fedramp"))
}
