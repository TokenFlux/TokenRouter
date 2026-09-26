package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// tokenExecutionContract 替换外部选择和交换，保留真实 HTTP、映射与尝试循环。
type tokenExecutionContract struct {
	OpenAITokenExecution
	t          *testing.T
	events     []string
	selections int
}

func (f *tokenExecutionContract) PlanTokenRoute(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	f.events = append(f.events, "plan")
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "group-model"}})
}

func (f *tokenExecutionContract) TokenSessionHash(*gin.Context, []byte) string {
	f.events = append(f.events, "session")
	return "token-session"
}

func (f *tokenExecutionContract) CheckKey(_ context.Context, _ *apikey.APIKey, _ *billing.UserSubscription, platform string, afterWait bool) error {
	f.events = append(f.events, "funding")
	require.Equal(f.t, "openai", platform)
	require.False(f.t, afterWait)
	return nil
}

func (*tokenExecutionContract) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, _ string) []byte {
	return body
}

func (f *tokenExecutionContract) SelectCount(_ context.Context, _ *int64, hash, model, platform string) (OpenAICountTarget, error) {
	f.events = append(f.events, "select")
	f.selections++
	require.Equal(f.t, "token-session", hash)
	require.Equal(f.t, "group-model", model)
	require.Equal(f.t, "openai", platform)
	return tokenContractTarget{f: f, id: 1}, nil
}

func (f *tokenExecutionContract) SelectInputTokens(_ context.Context, _ *int64, hash, model, routingModel string, excluded map[int64]struct{}, platform string) (InputTokensSelection, error) {
	f.events = append(f.events, "select")
	f.selections++
	require.Equal(f.t, "token-session", hash)
	require.Equal(f.t, "client-model", model)
	require.Equal(f.t, "group-model", routingModel)
	require.Equal(f.t, "openai", platform)
	if f.selections == 2 {
		require.Contains(f.t, excluded, int64(1))
	}
	return InputTokensSelection{
		Target:  tokenContractTarget{f: f, id: int64(f.selections)},
		Release: func() { f.events = append(f.events, "release") },
	}, nil
}

type tokenContractTarget struct {
	f  *tokenExecutionContract
	id int64
}

func (t tokenContractTarget) Snapshot() account.AccountSnapshot {
	return account.AccountSnapshot{ID: t.id, Platform: "openai"}
}

func (tokenContractTarget) RetryLimit() int { return 0 }

func (t tokenContractTarget) ForwardCount(_ context.Context, c *gin.Context, body []byte, model string) error {
	_, observed := c.Get(OpsAuthLatencyMsKey)
	require.True(t.f.t, observed)
	t.f.events = append(t.f.events, "forward")
	require.Equal(t.f.t, "group-model", model)
	require.Equal(t.f.t, "group-model", gjson.GetBytes(body, "model").String())
	c.JSON(http.StatusOK, gin.H{"input_tokens": 17})
	return nil
}

func (t tokenContractTarget) ForwardInputTokens(_ context.Context, c *gin.Context, body []byte) error {
	t.f.events = append(t.f.events, "forward")
	require.Equal(t.f.t, "group-model", gjson.GetBytes(body, "model").String())
	if t.id == 1 {
		return &forward.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": 17})
	return nil
}

func TestOpenAITokensNativeHTTPContracts(t *testing.T) {
	for _, inputTokens := range []bool{false, true} {
		name := "messages-count"
		if inputTokens {
			name = "responses-input"
		}
		t.Run(name, func(t *testing.T) {
			fixture := &tokenExecutionContract{t: t}
			handler := NewOpenAITokensHandler(OpenAITokenOptions{MaxSwitches: 2}, OpenAITokenPorts{Execution: fixture, Funding: fixture, Diagnoser: routing.ModelAvailabilityDiagnoserFunc(unexpectedCountModelDiagnosis), ResolvedDiagnoser: routing.ModelAvailabilityDiagnoserFunc(unexpectedCountModelDiagnosis)}, fixture)
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", strings.NewReader(`{"model":"client-model","input":"hello","messages":[{"role":"user","content":"hello"}]}`))
			group := int64(7)
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7, GroupID: &group, Group: &routing.Group{
				ID: group, Platform: "openai", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses},
			}})
			c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
			if inputTokens {
				handler.ResponsesInputTokens(c)
				require.Equal(t, []string{"funding", "plan", "session", "select", "forward", "release", "select", "forward", "release"}, fixture.events)
			} else {
				handler.CountTokens(c)
				require.Equal(t, []string{"plan", "funding", "session", "select", "forward"}, fixture.events)
			}
			require.Equal(t, http.StatusOK, writer.Code)
			require.JSONEq(t, `{"input_tokens":17}`, writer.Body.String())
		})
	}
}
