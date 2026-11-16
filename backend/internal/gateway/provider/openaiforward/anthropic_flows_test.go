package openaiforward

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// nativeFlowTestPorts captures native Anthropic request construction inputs and
// supplies in-memory SSE or JSON responses for flow tests.
type nativeFlowTestPorts struct {
	sink           *cacheOutputSink // captured client output writer
	upstreamModel  string           // mapped vendor model returned by UpstreamModel
	forceCache1h   bool             // simulated per-Key Claude 1h cache decision
	streamResponse bool             // whether SendNative returns SSE instead of JSON
	builtBodies    [][]byte         // captured request payloads passed to BuildNative
	cacheModels    []string         // captured model arguments passed to CacheTTL
}

// BillingModel selects fallback when present or retains the supplied model.
func (p *nativeFlowTestPorts) BillingModel(model, fallback string) string {
	if fallback != "" {
		return fallback
	}
	return model
}

// Debug ignores structured flow diagnostics in unit tests.
func (*nativeFlowTestPorts) Debug(string, ...zap.Field) {}

// ErrorResponse panics when unexpected HTTP error presentation runs in success scenarios.
func (*nativeFlowTestPorts) ErrorResponse(*http.Response, string) (*Result, error) {
	panic("unexpected ErrorResponse call")
}

// FailoverHTTP returns nil because test exchanges use HTTP 200 responses.
func (*nativeFlowTestPorts) FailoverHTTP(context.Context, *http.Response, []byte, string, string) error {
	return nil
}

// PrepareTransport accepts proxy preparation before request construction.
func (*nativeFlowTestPorts) PrepareTransport() {}

// ReadUpstreamError panics when unexpected error-body capture runs in success scenarios.
func (*nativeFlowTestPorts) ReadUpstreamError(*http.Response) ([]byte, string) {
	panic("unexpected ReadUpstreamError call")
}

// Sink returns the test buffer collecting client output.
func (p *nativeFlowTestPorts) Sink() upstream.OutputSink { return p.sink }

// UpstreamModel returns the configured vendor model when set or retains model.
func (p *nativeFlowTestPorts) UpstreamModel(model string) string {
	if p.upstreamModel != "" {
		return p.upstreamModel
	}
	return model
}

// Profile returns a minimal native Anthropic provider profile for logging.
func (*nativeFlowTestPorts) Profile() MessagesProfile {
	return MessagesProfile{ID: 7, Profile: Profile{Name: "opencode-claude", Platform: "opencode"}}
}

// Error panics on unexpected local validation errors in valid flow scenarios.
func (*nativeFlowTestPorts) Error(_ int, _, message string) {
	panic("unexpected Error: " + message)
}

// PrepareModel returns the supplied Messages body without attribute rewrites.
func (*nativeFlowTestPorts) PrepareModel(_ context.Context, body []byte, _ string) ([]byte, error) {
	return body, nil
}

// ThinkingFallback preserves the supplied effort pointer unchanged.
func (*nativeFlowTestPorts) ThinkingFallback(effort *string, _ []byte, _ string) *string {
	return effort
}

// StripEmpty preserves the supplied request bytes in these focused cache tests.
func (*nativeFlowTestPorts) StripEmpty(body []byte) []byte { return body }

// FilterSearch preserves the supplied request bytes in these focused cache tests.
func (*nativeFlowTestPorts) FilterSearch(body []byte, _ string) []byte { return body }

// CacheLimit seeds an ephemeral five-minute system breakpoint when absent so
// converted Responses and Chat bodies carry a concrete cache marker into CacheTTL.
func (*nativeFlowTestPorts) CacheLimit(body []byte) []byte {
	if !gjson.GetBytes(body, "system.0.cache_control").Exists() {
		updated, err := sjson.SetBytes(body, "system.0.cache_control", map[string]any{
			"type": "ephemeral",
			"ttl":  "5m",
		})
		if err == nil {
			return updated
		}
	}
	return body
}

// CacheTTL records model and rewrites system.0.cache_control.ttl to 1h when
// forceCache1h is enabled and model names a Claude target.
func (p *nativeFlowTestPorts) CacheTTL(_ context.Context, body []byte, model string) []byte {
	p.cacheModels = append(p.cacheModels, model)
	if !p.forceCache1h || !strings.Contains(model, "claude") {
		return body
	}
	if gjson.GetBytes(body, "system.0.cache_control.type").String() == "ephemeral" {
		updated, err := sjson.SetBytes(body, "system.0.cache_control.ttl", "1h")
		if err == nil {
			body = updated
		}
	}
	if gjson.GetBytes(body, "messages.0.content.0.cache_control.type").String() == "ephemeral" {
		updated, err := sjson.SetBytes(body, "messages.0.content.0.cache_control.ttl", "1h")
		if err == nil {
			body = updated
		}
	}
	return body
}

// Log ignores formatted diagnostic messages in unit tests.
func (*nativeFlowTestPorts) Log(string, ...any) {}

// ProtocolAPIKey supplies a nonblank test credential for native exchange.
func (*nativeFlowTestPorts) ProtocolAPIKey() string { return "sk-opencode-test" }

// TargetURL returns a deterministic Messages endpoint URL.
func (*nativeFlowTestPorts) TargetURL() (string, error) {
	return "https://opencode.ai/zen/v1/messages", nil
}

// StreamContext returns ctx with a no-op release callback.
func (*nativeFlowTestPorts) StreamContext(ctx context.Context, _ bool) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

// BuildNative captures a copy of body and constructs an outbound HTTP request.
func (p *nativeFlowTestPorts) BuildNative(ctx context.Context, body []byte, _, url string) (*http.Request, error) {
	p.builtBodies = append(p.builtBodies, append([]byte(nil), body...))
	return http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
}

// SendNative returns an in-memory SSE or JSON Anthropic response matching the flow mode.
func (p *nativeFlowTestPorts) SendNative(*http.Request) (*http.Response, error) {
	if p.streamResponse {
		sse := "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-6","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
			"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
			"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
			"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n" +
			"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	}
	jsonResp := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(jsonResp)),
	}, nil
}

// TransportErrorNative returns err unchanged for test diagnostics.
func (*nativeFlowTestPorts) TransportErrorNative(_ context.Context, err error) error { return err }

// DirectOptions returns buffered native Messages reader callbacks for unit tests.
func (*nativeFlowTestPorts) DirectOptions() NativeAnthropicOptions {
	return NativeAnthropicOptions{
		ProviderID:     7,
		UpdateWindow:   func(context.Context, http.Header) {},
		ReadBody:       io.ReadAll,
		InvalidJSON:    func(_ context.Context, _ *http.Response, _ []byte, err error, _ string) error { return err },
		ForceCache:     func(context.Context) bool { return false },
		CopyHeaders:    func(http.Header, http.Header) {},
		ReverseTools:   func(body []byte) []byte { return body },
		MaxLineSize:    func() int { return 1 << 20 },
		StreamInterval: func() time.Duration { return 0 },
		Log:            func(string, ...any) {},
		HandleTimeout:  func(context.Context, string) {},
	}
}

// AdaptResponsesTools passes body through with an empty client-tool mapping.
func (*nativeFlowTestPorts) AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	return body, bridge.ResponsesClientToolMapping{}, nil
}

// ResponsesToAnthropic converts r using the production protocol bridge.
func (*nativeFlowTestPorts) ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error) {
	return bridge.ResponsesToAnthropicRequest(r)
}

// ChatToResponses converts r using default options in the production protocol bridge.
func (*nativeFlowTestPorts) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return bridge.ChatCompletionsToResponses(r, bridge.RequestOptions{})
}

// ResponsesEffort returns no explicit effort override in cache-policy flow tests.
func (*nativeFlowTestPorts) ResponsesEffort([]byte, ...string) *string { return nil }

// ChatEffort returns no explicit effort override in cache-policy flow tests.
func (*nativeFlowTestPorts) ChatEffort([]byte, ...string) *string { return nil }

// MapStatus returns status unchanged.
func (*nativeFlowTestPorts) MapStatus(status int) int { return status }

// OutputOptions supplies conversion reader callbacks for Responses and Chat tests.
func (*nativeFlowTestPorts) OutputOptions() AnthropicOutputOptions {
	return AnthropicOutputOptions{
		Runtime:        bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		MaxLineSize:    func() int { return 1 << 20 },
		StreamInterval: func() time.Duration { return 0 },
		CopyHeaders:    func(http.Header, http.Header) {},
		ReverseTools:   func(body []byte) []byte { return body },
		Error:          func(_ int, _, message string) { panic("unexpected output error: " + message) },
		Warn:           func(string, ...zap.Field) {},
	}
}

// TestForwardNativeAnthropicFlows_ApplyCacheTTLByMappedModel runs native
// Messages, Responses, and Chat flows across Claude and mapped non-Claude
// upstream models. It checks CacheTTL receives the final upstream model and
// BuildNative receives 1h cache lifetimes only for Claude targets.
func TestForwardNativeAnthropicFlows_ApplyCacheTTLByMappedModel(t *testing.T) {
	t.Run("messages_claude_versus_glm", func(t *testing.T) {
		msgBody := []byte(`{
			"model":"claude-sonnet-4-6",
			"max_tokens":64,
			"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],
			"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]
		}`)

		claudePorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "claude-sonnet-4-6", forceCache1h: true}
		_, err := ForwardNativeMessages(context.Background(), msgBody, "", claudePorts)
		require.NoError(t, err)
		require.Equal(t, []string{"claude-sonnet-4-6"}, claudePorts.cacheModels)
		require.Equal(t, "1h", gjson.GetBytes(claudePorts.builtBodies[0], "system.0.cache_control.ttl").String())
		require.Equal(t, "1h", gjson.GetBytes(claudePorts.builtBodies[0], "messages.0.content.0.cache_control.ttl").String())

		glmPorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "glm-5", forceCache1h: true}
		_, err = ForwardNativeMessages(context.Background(), msgBody, "", glmPorts)
		require.NoError(t, err)
		require.Equal(t, []string{"glm-5"}, glmPorts.cacheModels)
		require.Equal(t, "5m", gjson.GetBytes(glmPorts.builtBodies[0], "system.0.cache_control.ttl").String())
		require.Equal(t, "5m", gjson.GetBytes(glmPorts.builtBodies[0], "messages.0.content.0.cache_control.ttl").String())
	})

	t.Run("responses_claude_versus_glm", func(t *testing.T) {
		respBody := []byte(`{"model":"claude-sonnet-4-6","instructions":"be helpful","input":"hello"}`)

		claudePorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "claude-sonnet-4-6", forceCache1h: true, streamResponse: true}
		_, err := ForwardNativeResponses(context.Background(), respBody, "", claudePorts)
		require.NoError(t, err)
		require.Equal(t, []string{"claude-sonnet-4-6"}, claudePorts.cacheModels)
		require.Equal(t, "1h", gjson.GetBytes(claudePorts.builtBodies[0], "system.0.cache_control.ttl").String())

		glmPorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "glm-5", forceCache1h: true, streamResponse: true}
		_, err = ForwardNativeResponses(context.Background(), respBody, "", glmPorts)
		require.NoError(t, err)
		require.Equal(t, []string{"glm-5"}, glmPorts.cacheModels)
		require.Equal(t, "5m", gjson.GetBytes(glmPorts.builtBodies[0], "system.0.cache_control.ttl").String())
	})

	t.Run("chat_claude_versus_glm", func(t *testing.T) {
		chatBody := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"system","content":"be helpful"},{"role":"user","content":"hello"}]}`)

		claudePorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "claude-sonnet-4-6", forceCache1h: true, streamResponse: true}
		_, err := ForwardNativeChat(context.Background(), chatBody, "", claudePorts)
		require.NoError(t, err)
		require.Equal(t, []string{"claude-sonnet-4-6"}, claudePorts.cacheModels)
		require.Equal(t, "1h", gjson.GetBytes(claudePorts.builtBodies[0], "system.0.cache_control.ttl").String())

		glmPorts := &nativeFlowTestPorts{sink: &cacheOutputSink{}, upstreamModel: "glm-5", forceCache1h: true, streamResponse: true}
		_, err = ForwardNativeChat(context.Background(), chatBody, "", glmPorts)
		require.NoError(t, err)
		require.Equal(t, []string{"glm-5"}, glmPorts.cacheModels)
		require.Equal(t, "5m", gjson.GetBytes(glmPorts.builtBodies[0], "system.0.cache_control.ttl").String())
	})
}
