package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// HTTP POST /v1/responses -> forwardOpenAIWSV2 keeps the canonical outbound
// tier separate from response.completed.service_tier for usage-time billing.
func TestForwardOpenAIWSV2_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {

	cases := []struct {
		name        string
		requestTier string
		stream      bool
	}{
		{name: "priority_nonstream", requestTier: "priority", stream: false},
		{name: "fast_stream", requestTier: "fast", stream: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

			options := &wsFixtureOptions{}
			options.Request.URLPolicy.Enabled = false
			options.Request.URLPolicy.AllowInsecureHTTP = true
			options.WS.Enabled = true
			options.WS.APIKeyEnabled = true
			options.WS.ResponsesWebsocketsV2 = true
			options.Pool.MaxConnsPerAccount = 1
			options.Pool.MinIdlePerAccount = 0
			options.Pool.MaxIdlePerAccount = 1
			options.Pool.QueueLimitPerConn = 8
			options.WS.DialTimeoutSeconds = 3
			options.WS.ReadTimeoutSeconds = 5
			options.WS.WriteTimeoutSeconds = 3

			captureConn := &openAIWSCaptureConn{
				events: [][]byte{
					[]byte(`{"type":"response.completed","response":{"id":"resp_tier_v2","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}`),
				},
			}
			captureDialer := &openAIWSCaptureDialer{conn: captureConn}
			pool := newOpenAIWSConnPool(options)
			pool.SetClientDialerForTest(captureDialer)

			svc := newWSFixture(wsFixtureInputs{options: options, transport: &auxiliaryHTTPRecorder{}, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector(), pool: pool})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5882,
				Name:        "openai-ws-v2-tier",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true}},
			}

			body := []byte(fmt.Sprintf(
				`{"model":"gpt-5.5","stream":%t,"service_tier":%q,"input":[{"type":"input_text","text":"hi"}]}`,
				tc.stream, tc.requestTier,
			))
			result, err := svc.Responses.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode, "must take HTTP POST → forwardOpenAIWSV2, not HTTP fallback")
			require.Equal(t, tc.stream, result.Stream)
			require.Equal(t, "resp_tier_v2", result.RequestID)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "priority", *result.ServiceTier)
			require.Equal(t, "default", result.UpstreamResponseServiceTier)
			require.Equal(t, "priority", captureConn.lastWrite["service_tier"],
				"outbound WS payload still carries the requested Fast tier")
		})
	}
}
