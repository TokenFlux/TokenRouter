package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAISetupTokenWSCompatibility(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	c.Request.Header.Set("session_id", "session-one")
	c.Set("api_key", &apikey.APIKey{ID: 17})

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token":       "setup-token-value",
			"chatgpt_account_id": "chatgpt-setup",
		}},
	}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}})

	wsURL, err := svc.buildOpenAIResponsesWSURL(account)
	require.NoError(t, err)
	require.Equal(t, "wss://chatgpt.com/backend-api/codex/responses", wsURL)
	foreignURL, err := svc.buildOpenAIResponsesWSURL(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeSetupToken}})
	require.NoError(t, err)
	require.Equal(t, "wss://api.openai.com/v1/responses", foreignURL)

	headers, session, err := svc.buildOpenAIWSHeaders(
		context.Background(), c, account, "setup-token-value", egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "gpt-5.1-codex", "",
	)
	require.NoError(t, err)
	require.Equal(t, "Bearer setup-token-value", headers.Get("authorization"))
	require.Equal(t, "chatgpt-setup", headers.Get("chatgpt-account-id"))
	require.NotEmpty(t, headers.Get("originator"))
	require.Equal(t, "session-one", session.SessionID)
	require.NotEqual(t, session.SessionID, headers.Get("session_id"))

	payload := svc.buildOpenAIWSCreatePayload(map[string]any{"store": true}, account)
	require.Equal(t, false, payload["store"])
}
