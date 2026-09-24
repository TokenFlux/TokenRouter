//go:build unit

package messageforward_test

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"

	"io"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 核对区间、缓存桶和分组倍率的组合，并确保按次费用不受推理倍率影响。

func TestMaxReasoningPricing_AnthropicForwardReportsOutboundEffort(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, effort := range []string{"xhigh", "max"} {
			t.Run(protocol+"/"+effort, func(t *testing.T) {
				stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
				svc := newHTTPRuntimeFixture(&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, messageforward.Dependencies{Transport: upstream}, nil)
				account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.anthropic.com"}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				var result *forwardcore.MessagesResult
				var err error
				if protocol == "responses" {
					result, err = svc.ForwardAsResponses(c.Request.Context(), c, account, []byte(`{"model":"claude-fable-5-1","input":"hi","reasoning":{"effort":"`+effort+`"}}`), nil)
				} else {
					result, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"`+effort+`"}`), nil)
				}
				require.NoError(t, err)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, effort, *result.ReasoningEffort)
				requestBody, err := io.ReadAll(upstream.lastReq.Body)
				require.NoError(t, err)
				require.Equal(t, effort, gjson.GetBytes(requestBody, "output_config.effort").String())
			})
		}
	}
}
