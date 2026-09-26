package messageforward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
)

func TestGatewayService_AnthropicAPIKeyPassthrough_BearerAuthScheme(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "secret=1")

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "ollama-key",
				"base_url": "https://ollama.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough":        true,
				"anthropic_apikey_auth_scheme": accountcore.AnthropicAPIKeyAuthSchemeAuthorizationBearer,
			},
		},
	}

	msgReq, wireBody, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		account, []byte(`{"model":"gpt-oss:20b","messages":[]}`), "ollama-key",
	)
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/messages?beta=true", msgReq.URL.String())
	require.JSONEq(t, `{"model":"gpt-oss:20b","messages":[]}`, string(wireBody))
	require.Equal(t, "Bearer ollama-key", claude.GetHeaderRaw(msgReq.Header, "authorization"))
	require.Empty(t, claude.GetHeaderRaw(msgReq.Header, "x-api-key"))
	require.Empty(t, claude.GetHeaderRaw(msgReq.Header, "cookie"))

	countReq, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		account, []byte(`{"model":"gpt-oss:20b","messages":[]}`), "ollama-key", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/messages/count_tokens?beta=true", countReq.URL.String())
	require.Equal(t, "Bearer ollama-key", claude.GetHeaderRaw(countReq.Header, "authorization"))
	require.Empty(t, claude.GetHeaderRaw(countReq.Header, "x-api-key"))
	require.Empty(t, claude.GetHeaderRaw(countReq.Header, "cookie"))
}

// TestGatewayService_AnthropicAPIKeyPassthrough_BuildRequestRejectsInvalidBaseURL 覆盖透传模式下模型映射的各种边界情况
func TestGatewayService_AnthropicAPIKeyPassthrough_BuildRequestRejectsInvalidBaseURL(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "k",
				"base_url": "://invalid-url",
			},
		},
	}

	_, _, err := svc.buildPassthroughRequest(context.Background(), c, &AttemptState{}, account, []byte(`{}`), "k")
	require.Error(t, err)
}

func TestGatewayService_AnthropicOAuth_NotAffectedByAPIKeyPassthroughToggle(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeOAuth,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		},
	}

	require.False(t, account.View().IsAnthropicAPIKeyPassthroughEnabled())

	req, _, err := svc.buildRequest(context.Background(), c, &AttemptState{}, account, []byte(`{"model":"claude-3-7-sonnet-20250219"}`), "oauth-token", "oauth", "claude-3-7-sonnet-20250219", true, false)
	require.NoError(t, err)
	require.Equal(t, "Bearer oauth-token", claude.GetHeaderRaw(req.Header, "authorization"))
	require.Contains(t, claude.GetHeaderRaw(req.Header, "anthropic-beta"), claude.BetaOAuth, "OAuth 链路仍应按原逻辑补齐 oauth beta")
}
