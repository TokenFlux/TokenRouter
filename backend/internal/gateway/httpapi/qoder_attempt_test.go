package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder forwards client protocols through wrapped upstream SSE, with
// conversation identity separate from generated response IDs and provider
// model routes. These fixtures protect explicit session precedence, full
// request replay, client model restoration and usage reported by the upstream
// stream. Tool calls can arrive as fragments, placeholders or parallel calls
// without reliable indexes.
//
// The blocking client holds the first accepted stream open while another
// request inspects conversation reservation and recovery. Body snapshots use a
// mutex, and pipe completion is controlled separately from later malformed
// responses. Wire fixtures exercise tool identity across Chat, Messages and
// Responses, including text-encoded calls and cache accounting;
// client-disconnect writers verify that tail usage remains available even when
// output cannot continue.

// Qoder wire fixtures carry cached-token accounting and XML, JSON or DSML tool
// text through the production parser and converters.
const (
	qoderCachedUsageSSEForTest    = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":66637,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":66643,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":66612,\\\"cacheable_tokens\\\":19},\\\"completion_tokens_details\\\":{\\\"reasoning_tokens\\\":0}}}\"}\n\n"
	qoderXMLToolCallFixture       = `<tool_call>Read<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>`
	qoderJSONShellToolCallFixture = `<tool_call>{"name":"shell","arguments":{"command":"pwd","description":"Print working directory"}}</tool_call>`

	qoderDSMLToolCallFixture = `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="Bash">
<｜｜DSML｜｜parameter name="command" string="true">ls -la</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="description" string="true">List root files</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`
)

// qoderContextClientFixture exposes the execution context through a supplied
// request callback.
type qoderContextClientFixture struct {
	// request must be configured before forwarding; the fixture calls it with the
	// execution context and returns its response or error unchanged.
	request func(context.Context) (*http.Response, error) // execution-context fixture callback
}

// qoderForwardTestHeader carries one request-header override for forwarding
// fixtures.
type qoderForwardTestHeader struct {
	key   string // request header field name
	value string // request header field value
}

// blockingQoderClientStub holds initial stream completion while recording
// concurrent request bodies.
type blockingQoderClientStub struct {
	t *testing.T // owning fixture test handle
	// mu protects Bodies, Headers, firstWriter, firstDone and nextError across
	// concurrent forwarding and test control. Bodies are copied on capture and
	// BodyAt, while Headers retains the supplied map without cloning it.
	mu sync.Mutex // request observation state mutex
	// cond shares mu and is broadcast after request capture. waitForCalls polls
	// the protected count instead of waiting on this condition variable.
	cond   *sync.Cond // request-arrival notification condition
	Bodies [][]byte   // copied upstream request bodies
	// Headers retains the supplied map. Callers leave its entries immutable
	// while forwarding or test control reads it; mu serializes the field assignment.
	Headers     map[string]string // retained latest upstream headers
	firstWriter *io.PipeWriter    // open first response pipe
	firstDone   bool              // first completion ownership flag
	nextError   bool              // one-shot malformed later response
}

// qoderTrackingReadCloser records whether conversion closes its fixture input
// reader.
type qoderTrackingReadCloser struct {
	*strings.Reader // underlying fixture string reader
	// closed is set by Close and inspected after synchronous conversion returns;
	// the fixture supplies no synchronization for concurrent close/read
	// observation.
	closed bool // observed fixture close flag
}

// qoderAnthropicStreamEventForTest pairs a fixture event name with decoded
// JSON data.
type qoderAnthropicStreamEventForTest struct {
	// Event and Data belong to one blank-line-delimited fixture block. Event keeps
	// its latest event-space label; object data lines reuse Data within that block,
	// while the next block starts with nil decoded data.
	Event string         // Anthropic SSE event name
	Data  map[string]any // decoded Anthropic event object
}

// qoderFailingHTTPWriter injects serial body-write failure after a configured
// delegation allowance.
type qoderFailingHTTPWriter struct {
	gin.ResponseWriter // delegated Gin headers and writes
	// failAfter and writes govern serial failure injection. Permitted calls
	// increment writes before delegation, even if the embedded writer then fails;
	// later calls fail directly once the attempt allowance is consumed.
	failAfter int // delegated write attempt allowance
	writes    int // delegated write attempt count
}

// assertQoderContextCapabilityForTest checks model input capacity and optional
// runtime override fields. Callers provide decoded object-shaped payloads;
// wantRuntime requires both context length and IDE override values, while
// false requires their absence.
func assertQoderContextCapabilityForTest(t *testing.T, payload map[string]any, wantTokens int, wantRuntime bool) {
	t.Helper()
	modelConfig := requireQoderPayloadMapForTest(t, payload["model_config"], "model_config")
	require.EqualValues(t, wantTokens, modelConfig["max_input_tokens"])

	parameters := requireQoderPayloadMapForTest(t, payload["parameters"], "parameters")
	contextLength, hasContextLength := parameters["context_length"]
	chatContext := requireQoderPayloadMapForTest(t, payload["chat_context"], "chat_context")
	extra := requireQoderPayloadMapForTest(t, chatContext["extra"], "chat_context.extra")
	runtimeOverride, hasRuntimeOverride := extra["ideModelConfigOverride"].(map[string]any)
	if wantRuntime {
		require.True(t, hasContextLength)
		require.EqualValues(t, wantTokens, contextLength)
		require.True(t, hasRuntimeOverride)
		require.EqualValues(t, wantTokens, runtimeOverride["max_input_tokens"])
		return
	}

	require.False(t, hasContextLength)
	require.False(t, hasRuntimeOverride)
}

// requireQoderPayloadMapForTest returns the supplied value as a decoded
// fixture object. A different dynamic type fails the test with its payload
// path, so callers can safely inspect the returned map afterward.
func requireQoderPayloadMapForTest(t *testing.T, value any, path string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "%s 应为 JSON 对象", path)
	return result
}

// StreamRequestContext delegates the supplied execution context to the
// configured fixture callback. This fixture ignores session, endpoint, body
// and header arguments; callers configure request before forwarding and
// interpret its response or error.
func (c qoderContextClientFixture) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, _ []byte, _ map[string]string) (*http.Response, error) {
	return c.request(ctx)
}

// TestQoderForwardContextDetachesStreamingFromClientCancellation cancels the
// client inside upstream execution for buffered and streamed input. Both
// receive a bounded execution deadline, while only streaming preserves a live
// context and final usage.
func TestQoderForwardContextDetachesStreamingFromClientCancellation(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			value, fixture, _ := gatewaytestkit.NewDefaultQoderFixture()
			calls := 0
			fixture.Client = qoderContextClientFixture{request: func(execution context.Context) (*http.Response, error) {
				calls++
				deadline, ok := execution.Deadline()
				require.True(t, ok)
				remaining := time.Until(deadline)
				require.LessOrEqual(t, remaining, qoder.QoderStreamTimeout)
				// Allow fixture setup to consume a minute of the fresh execution budget.
				require.Greater(t, remaining, qoder.QoderStreamTimeout-time.Minute)
				cancel()
				if !stream {
					require.ErrorIs(t, execution.Err(), context.Canceled)
					return nil, execution.Err()
				}
				require.NoError(t, execution.Err())
				body := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + "data: {\"body\":\"[DONE]\"}\n\n"
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			body := []byte(`{"model":"auto","stream":` + strconv.FormatBool(stream) + `,"messages":[{"role":"user","content":"hi"}]}`)
			result, err := ForwardQoderAttempt(ctx, c, fixture.Runtime, value, body, protocolcore.ProtocolOpenAIChatCompletions)
			require.Equal(t, 1, calls)
			if stream {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 3, result.Usage.OutputTokens)
			} else {
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, result)
			}
		})
	}
}

// TestQoderForwardPartialResult emits real Chat content and usage before an
// upstream failure. Forwarding must return the error together with measured
// input/output usage rather than discard the served partial result.
func TestQoderForwardPartialResult(t *testing.T) {
	a, s, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "fixture failure"})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	result, err := ForwardQoderAttempt(c.Request.Context(), c, s.Runtime, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)
	if err == nil || !strings.Contains(rec.Body.String(), "served") {
		t.Fatalf("fixture did not reach post-output failure: %v", err)
	}
	if result == nil {
		t.Fatal("ForwardChatCompletions discards observed partial usage before handler completion")
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 3 {
		t.Fatalf("incorrect observed usage: %+v", result.Usage)
	}
}

// TestQoderCanceledBeforeForward cancels the request before native Qoder
// execution begins. No upstream request may be recorded, and the returned
// error must preserve context.Canceled for outer cancellation handling.
func TestQoderCanceledBeforeForward(t *testing.T) {
	a, s, client := gatewaytestkit.NewDefaultQoderFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	_, err := ForwardQoderAttempt(ctx, c, s.Runtime, a, []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)
	if len(client.Requests) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled request starts %d upstream inference(s); err=%v", len(client.Requests), err)
	}
}

// TestQoderGatewayAllowsExplicitPreviewCompatibilityMapping maps a preview
// client model explicitly to a Qoder route. Upstream headers and context
// capability must use that route while result and client output preserve the
// requested alias.
func TestQoderGatewayAllowsExplicitPreviewCompatibilityMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	provider := &providercore.Record{
		ID:       88,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"qwen3.8-max-preview": "qmodel_38max",
			},
			"model_whitelist": []any{},
		},
	}
	gatewaytestkit.SetQoderCatalog(provider)
	client := &gatewaytestkit.QoderClient{
		Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := gatewaytestkit.NewQoderFixture(provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{}),
		client, nil)

	svc.Tokens.Core.Sessions = map[int64]providercore.QoderSessionCacheEntry[*qoder.SessionContext]{
		provider.ID: {
			CredentialsHash: providercore.QoderCredentialsHash(provider.Credentials),
			Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIChatCompletions)

	require.NoError(t, err)
	require.Equal(t, "qwen3.8-max-preview", result.Model)
	require.Equal(t, "qmodel_38max", result.UpstreamModel)
	require.Equal(t, "qmodel_38max", client.Headers["x-model-key"])
	require.Contains(t, rec.Body.String(), `"model":"qwen3.8-max-preview"`)
	assertQoderContextCapabilityForTest(t, qoderLastUpstreamPayloadForTest(t, client), 1000000, true)
}

// TestQoderGatewayForwardUsesOriginalModelAfterGroupMapping forwards mapped
// bodies through Chat, Responses and Messages with an explicit response-model
// override. Each result and client envelope must retain the original model
// while upstream identity follows the body.
func TestQoderGatewayForwardUsesOriginalModelAfterGroupMapping(t *testing.T) {
	tests := []struct {
		name string                                                                                                                                       // protocol or mapping case label
		path string                                                                                                                                       // fixture HTTP endpoint path
		body []byte                                                                                                                                       // encoded client request fixture
		call func(context.Context, *gatewaytestkit.QoderFixture, *gin.Context, *providercore.Record, []byte, string) (*forwardcore.MessagesResult, error) // selected protocol forwarding callback
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"qmodel","messages":[{"role":"user","content":"hi"}],"stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, provider *providercore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIChatCompletions, responseModel)
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"qmodel","input":"hi","stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, provider *providercore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIResponses, responseModel)
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: []byte(`{"model":"qmodel","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, provider *providercore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, provider, body, protocolcore.ProtocolAnthropicMessages, responseModel)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))

			result, err := tt.call(context.Background(), svc, c, provider, tt.body, "qwen3.7-plus")

			require.NoError(t, err)
			require.Equal(t, "qwen3.7-plus", result.Model)
			require.Equal(t, "qmodel", result.UpstreamModel)
			require.Equal(t, "qmodel", client.Headers["x-model-key"])
			require.Equal(t, "qwen3.7-plus", gjson.Get(rec.Body.String(), "model").String())
		})
	}
}

// TestQoderGatewayChatCompletionsReusesSessionAndSendsFullReplay extends an
// explicitly identified Chat conversation. Upstream session identity must
// remain stable while the second request replays system, user, assistant and
// new user turns.
func TestQoderGatewayChatCompletionsReusesSessionAndSendsFullReplay(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	first := qoderForwardChatCompletionsForTest(t, svc, provider, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.Len(t, client.Bodies, 2)
	require.Equal(t, first["session_id"], second["session_id"])
	firstMessages := qoderFixtureValue[[]any](t, first["messages"])
	require.Len(t, firstMessages, 2)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, firstMessages[0])["role"])

	secondMessages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, secondMessages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, secondMessages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, secondMessages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, secondMessages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, secondMessages[3])["role"])
	require.Equal(t, "next", qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])
}

// TestQoderGatewayChatCompletionsWithoutSessionDoesNotReuseByFirstText repeats
// initial Chat text without explicit session identity. Upstream sessions must
// differ while the second request retains its full three-message history for
// replay.
func TestQoderGatewayChatCompletionsWithoutSessionDoesNotReuseByFirstText(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()

	first := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"how many turns?"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "hello", qoderPayloadMessageTextForTest(qoderFixtureValue[map[string]any](t, messages[0])))
}

// TestQoderGatewayChatCompletionsMapsUpstreamToolNameToDeclaredOpenAITool
// returns upstream Bash for a declared lowercase Chat tool. Buffered output
// must restore bash, use tool_calls finish reason and forward the matching
// declared tool metadata.
func TestQoderGatewayChatCompletionsMapsUpstreamToolNameToDeclaredOpenAITool(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
		"stream":false
	}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_calls", gjson.Get(response, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.Get(response, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

// TestQoderGatewayChatCompletionsConvertsLegacyFunctions supplies legacy Chat
// functions instead of modern tools. The upstream tool list must preserve the
// function name, description and object schema during compatibility
// conversion.
func TestQoderGatewayChatCompletionsConvertsLegacyFunctions(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"weather"}],
		"functions":[{
			"name":"get_weather",
			"description":"Get weather",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}}}
		}],
		"function_call":{"name":"get_weather"},
		"stream":false
	}`)

	result, _ := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", body)

	require.False(t, result.Stream)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Len(t, tools, 1)
	// Legacy Chat clients supply tools through functions and function_call.
	function := qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])
	require.Equal(t, "get_weather", function["name"])
	require.Equal(t, "Get weather", function["description"])
	require.Equal(t, "object", qoderFixtureValue[map[string]any](t, function["parameters"])["type"])
}

// TestQoderGatewayChatCompletionsLegacyFunctionCallNoneClearsTools supplies
// legacy function definitions with function_call set to none. Buffered
// forwarding must leave the upstream tool list empty rather than expose
// disabled client tools.
func TestQoderGatewayChatCompletionsLegacyFunctionCallNoneClearsTools(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"plain answer"}],
		"functions":[{"name":"get_weather","parameters":{"type":"object"}}],
		"function_call":"none",
		"stream":false
	}`)

	result, _ := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", body)

	require.False(t, result.Stream)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Empty(t, tools)
}

// TestQoderGatewayMessagesMapsUpstreamToolNameToDeclaredAnthropicTool returns
// upstream Bash for a lowercase Messages declaration. Buffered Anthropic
// content must restore the declared name with tool_use content and stop
// reason, retaining matching upstream tool metadata.
func TestQoderGatewayMessagesMapsUpstreamToolNameToDeclaredAnthropicTool(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"name":"bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_use", gjson.Get(response, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.Get(response, "content.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "content.0.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

// TestQoderGatewayResponsesMapsUpstreamToolNameToDeclaredFunctionCall returns
// upstream Bash through buffered Responses conversion with a lowercase
// declaration. The function-call item must retain valid arguments and declared
// bash identity without the upstream spelling.
func TestQoderGatewayResponsesMapsUpstreamToolNameToDeclaredFunctionCall(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, provider, body)

	require.False(t, result.Stream)
	require.Equal(t, "response", gjson.Get(response, "object").String())
	require.Equal(t, "function_call", gjson.Get(response, "output.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "output.0.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.Get(response, "output.0.arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

// TestQoderGatewayResponsesStreamsDeclaredFunctionCallEvents streams an
// upstream tool under a declared lowercase name. Responses item and
// argument-completion events must expose bash and valid JSON, beginning with
// response.created and hiding upstream Bash.
func TestQoderGatewayResponsesStreamsDeclaredFunctionCallEvents(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":true
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, provider, body)

	require.True(t, result.Stream)
	events := qoderResponsesStreamEventsForTest(t, response)
	require.Equal(t, "response.created", events[0].Get("type").String())
	var added gjson.Result
	var argsDone gjson.Result
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				added = event
			}
		case "response.function_call_arguments.done":
			argsDone = event
		}
	}
	require.Equal(t, "bash", added.Get("item.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, argsDone.Get("arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
}

// TestQoderGatewayResponsesToolContinuationUsesToolResultsAsPrompt continues
// after two Responses function calls return results. The new upstream prompt
// must contain both identified tool results instead of replaying the original
// user command as the prompt.
func TestQoderGatewayResponsesToolContinuationUsesToolResultsAsPrompt(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]`
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run two commands"}],
		"tools":` + tools + `,
		"stream":true
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"run two commands"},
			{"type":"function_call","call_id":"call_a","name":"bash","arguments":"{\"command\":\"printf A\"}"},
			{"type":"function_call","call_id":"call_b","name":"bash","arguments":"{\"command\":\"printf B\"}"},
			{"type":"function_call_output","call_id":"call_a","output":"A\n"},
			{"type":"function_call_output","call_id":"call_b","output":"B\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, provider, firstBody, qoderHeader("session_id", "responses-tool-continuation"))
	qoderForwardResponsesResultAndBodyForTest(t, svc, provider, secondBody, qoderHeader("session_id", "responses-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "run two commands", prompt)
	require.Contains(t, prompt, `<tool_result id="call_a">`)
	require.Contains(t, prompt, "A\n")
	require.Contains(t, prompt, `<tool_result id="call_b">`)
	require.Contains(t, prompt, "B\n")
}

// TestQoderGatewayResponsesToolContinuationGroupsFunctionCallsIntoOneAssistantTurn
// continues a Responses turn containing parallel calls. Upstream history must
// group both calls into one assistant message and include their identified
// result text in the new prompt.
func TestQoderGatewayResponsesToolContinuationGroupsFunctionCallsIntoOneAssistantTurn(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},{"type":"function","name":"glob","parameters":{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}}]`
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"list docs and print marker"},
			{"type":"function_call","call_id":"call_glob","name":"glob","arguments":"{\"pattern\":\"docs/*.md\"}"},
			{"type":"function_call","call_id":"call_bash","name":"bash","arguments":"{\"command\":\"echo CHAIN_OPENAI_OK\"}"},
			{"type":"function_call_output","call_id":"call_glob","output":"docs/a.md\ndocs/b.md"},
			{"type":"function_call_output","call_id":"call_bash","output":"CHAIN_OPENAI_OK\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, provider, body, qoderHeader("session_id", "responses-grouped-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 4)
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]), 2)

	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "list docs and print marker", prompt)
	require.Contains(t, prompt, `<tool_result id="call_glob">`)
	require.Contains(t, prompt, "docs/a.md")
	require.Contains(t, prompt, `<tool_result id="call_bash">`)
	require.Contains(t, prompt, "CHAIN_OPENAI_OK")
}

// TestQoderGatewayClaudeRequestsWithoutSessionDoNotReuseByFirstText extends
// Messages input without explicit or detected Claude session state. Upstream
// sessions must differ, and the second request must retain tools and all four
// history turns.
func TestQoderGatewayClaudeRequestsWithoutSessionDoNotReuseByFirstText(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你一共和我对话了几句话？"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 4)
}

// TestQoderGatewayClaudeCodeContextWithoutSessionUsesStablePrefixKey installs
// Claude client context without explicit session identity. Stable prefix
// matching must reuse the upstream session while forwarding tools, complete
// history and the current continuation prompt.
func TestQoderGatewayClaudeCodeContextWithoutSessionUsesStablePrefixKey(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])
}

// TestQoderGatewayDoesNotCommitConversationOnUpstreamFailure fails one upstream
// attempt, then clears the fixture error and forwards the same user input. The
// subsequent request must contain one history message; no stable session seed
// is supplied, so this assertion observes replay rather than stable-key rollback.
func TestQoderGatewayDoesNotCommitConversationOnUpstreamFailure(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Err = errors.New("upstream failed")
	body := []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	_, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIChatCompletions)
	require.Error(t, err)

	client.Err = nil
	first := qoderForwardChatCompletionsForTest(t, svc, provider, "", body)
	require.Len(t, qoderFixtureValue[[]any](t, first["messages"]), 1)
}

// TestQoderGatewayReservesConversationAfterUpstreamAcceptsBeforeStreamCompletes
// leaves the first upstream stream unfinished while sending a continuation.
// Both payloads must share one session, and continuation must retain tools and
// full history before the first stream finishes.
func TestQoderGatewayReservesConversationAfterUpstreamAcceptsBeforeStreamCompletes(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	client := newBlockingQoderClientStub(t)
	svc.Client = client
	body1 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	// Leave the first upstream stream unfinished while continuation runs
	// independently; join forwarding after pipe completion before reading its error.
	go func() {
		defer wg.Done()
		_, firstErr = ForwardQoderAttempt(context.Background(), firstCtx, svc.Runtime, provider, body1, protocolcore.ProtocolAnthropicMessages)
	}()
	client.waitForCalls(1)

	secondPayload := qoderForwardMessagesForTest(t, svc, provider, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	firstPayload := qoderPayloadAtForTest(t, client, 0)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, secondPayload["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

// TestQoderGatewayDoesNotCommitFailedPostToolStreamAsComplete fails a
// post-tool continuation while another accepted stream remains open. Retrying
// must reuse the session with tools and the full tool-result history rather
// than treat the failed tail as completed.
func TestQoderGatewayDoesNotCommitFailedPostToolStreamAsComplete(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	client := newBlockingQoderClientStub(t)
	svc.Client = client
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	// Leave the first upstream stream unfinished while continuation runs
	// independently; join forwarding after pipe completion before reading its error.
	go func() {
		defer wg.Done()
		_, firstErr = ForwardQoderAttempt(context.Background(), firstCtx, svc.Runtime, provider, body1, protocolcore.ProtocolAnthropicMessages)
	}()
	client.waitForCalls(1)

	// Inject failure under the same lock consumed by later request capture,
	// leaving the already accepted first pipe unaffected by this one-shot malformed response.
	client.mu.Lock()
	client.nextError = true
	client.mu.Unlock()
	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := ForwardQoderAttempt(context.Background(), failedCtx, svc.Runtime, provider, body2, protocolcore.ProtocolAnthropicMessages)
	require.Error(t, failedErr)

	retryPayload := qoderForwardMessagesForTest(t, svc, provider, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	require.Equal(t, qoderPayloadAtForTest(t, client, 0)["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, retryPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, retryPayload["messages"])
	require.Len(t, messages, 4)
	system := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "system", system["role"])
	user := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "assistant", assistant["role"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]))
	toolResult := qoderFixtureValue[map[string]any](t, messages[3])
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "call_1", toolResult["tool_call_id"])
	require.Equal(t, "call_1", toolResult["tool_call_call_id"])
	require.Equal(t, "Bash", toolResult["name"])
	require.Equal(t, "/repo", toolResult["content"])
}

// TestQoderGatewayRollsBackAcceptedConversationOnStreamParseFailure injects
// malformed input after upstream acceptance. A retry must retain the same
// session but replay tools and complete tool-result history instead of
// consuming a failed reservation as completion.
func TestQoderGatewayRollsBackAcceptedConversationOnStreamParseFailure(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	firstPayload := qoderForwardMessagesForTest(t, svc, provider, "", body1, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""

	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := ForwardQoderAttempt(context.Background(), failedCtx, svc.Runtime, provider, body2, protocolcore.ProtocolAnthropicMessages)
	require.Error(t, failedErr)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	retryPayload := qoderForwardMessagesForTest(t, svc, provider, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, firstPayload["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, retryPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, retryPayload["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "tool", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

// TestQoderGatewayFallsBackToFullReplayWhenPrefixDoesNotMatch changes a
// conversation prefix under an existing identity. Forwarding must create a
// different upstream session and retain the changed message plus remaining
// history in the replay.
func TestQoderGatewayFallsBackToFullReplayWhenPrefixDoesNotMatch(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, provider, "stable-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"first"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "stable-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"changed"},
			{"role":"assistant","content":"answer"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "changed", qoderFixtureValue[map[string]any](t, qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[0])["content"])[0])["text"])
}

// TestQoderGatewayFallsBackToFullReplayWhenSystemOrToolsChange changes system
// instructions and tool definitions for a continuation. Forwarding must use a
// different session and replay the new system, tool metadata and complete
// history.
func TestQoderGatewayFallsBackToFullReplayWhenSystemOrToolsChange(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be detailed"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"tools":[{"type":"function","function":{"name":"write","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "be detailed", qoderFixtureValue[map[string]any](t, messages[0])["content"])
	tools := qoderFixtureValue[[]any](t, second["tools"])
	require.Equal(t, "write", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

// TestQoderGatewayUsesExplicitBodySessionID supplies body session identity
// across a continuation. Upstream session must remain stable while the second
// request retains all three messages rather than dropping previous
// conversation turns.
func TestQoderGatewayUsesExplicitBodySessionID(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 3)
}

// TestQoderGatewayAnthropicMetadataSessionWinsOverChangingHeader keeps
// Anthropic metadata session identity while changing the header. The
// continuation must reuse the upstream session, replay its history and send
// the latest user prompt.
func TestQoderGatewayAnthropicMetadataSessionWinsOverChangingHeader(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	metadata := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","provider_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	first := qoderForwardMessagesForTest(t, svc, provider, "volatile-header-1", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, provider, "volatile-header-2", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])
}

// TestQoderGatewayClaudeCodeUsesExplicitHeaderSessionBeforeStableSeed combines
// Claude system text and user-agent headers with explicit session identity.
// Matching headers must
// reuse one session with complete history, while another header must create a
// separate upstream session.
func TestQoderGatewayClaudeCodeUsesExplicitHeaderSessionBeforeStableSeed(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardMessagesForTest(t, svc, provider, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])

	other := qoderForwardMessagesForTest(t, svc, provider, "other-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
}

// TestQoderGatewayClaudeCodeUsesMetadataSessionBeforeStableSeed combines
// Claude system text and user-agent headers with metadata session identity.
// Matching metadata must
// reuse one upstream session and current prompt; another metadata identity
// must separate the conversation while retaining tools.
func TestQoderGatewayClaudeCodeUsesMetadataSessionBeforeStableSeed(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	metadata1 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","provider_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])

	metadata2 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","provider_uuid":"","session_id":"66666666-7777-8888-9999-aaaaaaaaaaaa"}`
	other := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata2)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, other["tools"]))
}

// TestQoderGatewayClaudeCodeIgnoresVolatileBillingCCHForSystemReuse changes
// only the volatile billing CCH text in Claude system input. Session reuse
// must survive that variation while tools and full message history remain in
// the forwarded request.
func TestQoderGatewayClaudeCodeIgnoresVolatileBillingCCHForSystemReuse(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

// TestQoderGatewayClaudeCodeUltimateStablePromptCacheKeyReportsCacheRead
// continues a stable cached ultimate-model request after different upstream
// usage reports. The second response must retain its session and expose
// measured uncached input, cache-read input and output values.
func TestQoderGatewayClaudeCodeUltimateStablePromptCacheKeyReportsCacheRead(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	firstBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system1) + `,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + largeTools + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system2) + `,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + largeTools + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, provider, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	firstPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.Equal(t, "ultimate", firstResult.UpstreamModel)
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, "ultimate", client.Headers["x-model-key"])

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1500,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":1533,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":1400,\\\"cacheable_tokens\\\":100}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)

	require.Equal(t, "ultimate", secondResult.UpstreamModel)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, 100, secondResult.Usage.InputTokens)
	require.Equal(t, 1400, secondResult.Usage.CacheReadInputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(1400), gjson.Get(secondResponse, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(100), gjson.Get(secondResponse, "usage.input_tokens").Int())
}

// TestQoderGatewayStillFullReplaysWhenNonBillingSystemChanges changes
// substantive Claude system text beyond billing metadata. Forwarding must
// create a new session and retain tools plus complete four-message history for
// the new request.
func TestQoderGatewayStillFullReplaysWhenNonBillingSystemChanges(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"Changed Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 4)
}

// TestQoderGatewayReusedAnthropicConversationOmitsUnchangedTools extends a
// reused Anthropic conversation with unchanged tools. The current assertions
// require those tools and all four history turns to remain present alongside
// the reused upstream session.
func TestQoderGatewayReusedAnthropicConversationOmitsUnchangedTools(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, provider, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, provider, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`))

	require.NotEmpty(t, qoderFixtureValue[[]any](t, first["tools"]))
	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

// TestQoderGatewayUsageComesFromUpstreamSSE supplies an upstream usage report
// unrelated to request byte length. The result must retain those measured
// input/output counts instead of estimating input from the body size.
func TestQoderGatewayUsageComesFromUpstreamSSE(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1234,\\\"completion_tokens\\\":56,\\\"total_tokens\\\":1290}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolAnthropicMessages)

	require.NoError(t, err)
	require.Equal(t, 1234, result.Usage.InputTokens)
	require.Equal(t, 56, result.Usage.OutputTokens)
	require.NotEqual(t, len(body), result.Usage.InputTokens)
}

// TestQoderGatewayBuildsClientVisibleOpenAIUsageWithUpstreamTotals supplies
// prompt usage containing a large cached portion. Internal usage must separate
// uncached and cache-read input while client Chat usage preserves upstream
// totals and detail buckets.
func TestQoderGatewayBuildsClientVisibleOpenAIUsageWithUpstreamTotals(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(66637), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(19), gjson.Get(response, "usage.prompt_tokens_details.cacheable_tokens").Int())
	require.Equal(t, int64(0), gjson.Get(response, "usage.completion_tokens_details.reasoning_tokens").Int())
}

// TestQoderGatewayOpenAIUsageKeepsUpstreamPromptWhenCachedExceedsPrompt
// reports cached tokens greater than total prompt tokens. Internal uncached
// input must clamp to zero while client Chat prompt/total values retain their
// upstream observations.
func TestQoderGatewayOpenAIUsageKeepsUpstreamPromptWhenCachedExceedsPrompt(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":16,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":15,\\\"cacheable_tokens\\\":1}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", body)

	require.Equal(t, 0, result.Usage.InputTokens)
	require.Equal(t, 15, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(10), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(16), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(15), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

// TestQoderGatewayBuildsClientVisibleAnthropicUsageWithCacheRead converts
// cached upstream usage into Messages output. Internal and client input must
// expose the uncached portion separately from cache reads, with no invented
// cache-creation bucket.
func TestQoderGatewayBuildsClientVisibleAnthropicUsageWithCacheRead(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(25), gjson.Get(response, "usage.input_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.output_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

// TestQoderGatewayDoesNotSubtractPreviousUsageOnFullReplay replays a
// conversation after a larger second usage report. The second result must
// retain the full current upstream input/output counts, tools and history
// without subtracting previous usage.
func TestQoderGatewayDoesNotSubtractPreviousUsageOnFullReplay(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, provider, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult := qoderForwardMessagesResultForTest(t, svc, provider, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, secondPayload["messages"]), 4)
}

// TestQoderGatewayReturnsDeltaUsageToAnthropicClientOnReusedConversation
// reuses a session with a larger second upstream usage report. Current
// assertions require full second-request input/output counts in both result
// and Anthropic response, with tools retained.
func TestQoderGatewayReturnsDeltaUsageToAnthropicClientOnReusedConversation(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, int64(1200), gjson.Get(firstResponse, "usage.input_tokens").Int())

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(10000), gjson.Get(secondResponse, "usage.input_tokens").Int())
	require.Equal(t, int64(33), gjson.Get(secondResponse, "usage.output_tokens").Int())

	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
}

// TestQoderGatewayAnthropicToolUseResultSendsIncrementalTail continues an
// Anthropic tool turn under the same session. Current assertions require tools
// and full system/user/assistant/tool history, with tool identity and result
// text also represented in the prompt.
func TestQoderGatewayAnthropicToolUseResultSendsIncrementalTail(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)

	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	user := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(user))

	assistant := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "assistant", assistant["role"])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_1", qoderFixtureValue[map[string]any](t, toolCalls[0])["id"])

	tool := qoderFixtureValue[map[string]any](t, messages[3])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	require.Equal(t, "repo", tool["content"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "repo\n")
}

// TestQoderGatewayOpenAIToolCallsSendIncrementalTail continues a Chat
// function-call turn under the same session. The forwarded request must retain
// tools and full user/assistant/tool history while the prompt contains the
// identified tool result.
func TestQoderGatewayOpenAIToolCallsSendIncrementalTail(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, provider, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	user := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]), 1)
	tool := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "/repo\n")
}

// TestQoderGatewayRepeatedClaudeCodeRequestKeepsNonEmptyIncrementalTail
// repeats Claude conversation input after completed tool work. The session
// must remain stable and the forwarded request must retain tools and six
// history messages with its original prefix.
func TestQoderGatewayRepeatedClaudeCodeRequestKeepsNonEmptyIncrementalTail(t *testing.T) {
	provider, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"repeated-claude-request-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	first := qoderForwardMessagesForTest(t, svc, provider, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 6)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
}

// TestQoderGatewayStreamsDeltaUsageToOpenAIClientOnReusedConversation reuses
// Chat session state with a larger second usage report. Current assertions
// require full second upstream counts in both returned usage and streaming
// client usage, while tools remain present.
func TestQoderGatewayStreamsDeltaUsageToOpenAIClientOnReusedConversation(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)
	secondBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Contains(t, firstStream, `"prompt_tokens":1200`)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, "", secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Contains(t, secondStream, `"prompt_tokens":10000`)
	require.Contains(t, secondStream, `"completion_tokens":33`)
}

// TestQoderGatewayAnthropicConversationAfterToolResultKeepsReducingPayload
// extends a conversation after a tool result and assistant reply. Current
// assertions require six complete history turns and tools, with second payload
// size at least the first payload size.
func TestQoderGatewayAnthropicConversationAfterToolResultKeepsReducingPayload(t *testing.T) {
	provider, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, provider, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 6)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(qoderFixtureValue[map[string]any](t, messages[1])))
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[2])["tool_calls"]))
	require.Equal(t, "tool", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "call_1", qoderFixtureValue[map[string]any](t, messages[3])["tool_call_id"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[4])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[5])["role"])

	require.GreaterOrEqual(t, len(client.BodyAt(1)), len(client.BodyAt(0)))
}

// TestQoderGatewayDoesNotAttachAmbiguousArgumentDeltaToParallelToolCall
// buffers two identified tool calls followed by an unassociated argument
// fragment. Both identities must survive with empty arguments, and the
// ambiguous fragment must not appear in client output.
func TestQoderGatewayDoesNotAttachAmbiguousArgumentDeltaToParallelToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
	require.NotContains(t, string(body), "lost")
}

// TestQoderGatewayForwardChatCompletionsHonorsCanceledContext forwards Chat
// input under a canceled client context. The attempt must return
// context.Canceled and leave the response writer untouched so outer
// cancellation handling owns presentation.
func TestQoderGatewayForwardChatCompletionsHonorsCanceledContext(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &providercore.Record{
		ID:       93,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-token",
		},
	}
	tokenSource := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{ExchangePAT: func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &qoder.AuthIdentity{SecurityOauthToken: "token", UID: "uid"}, nil
		}
	}})
	svc := gatewaytestkit.NewQoderFixture(tokenSource, nil, nil)

	_, err := ForwardQoderAttempt(ctx, c, svc.Runtime, provider, []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, c.Writer.Written())
}

// qoderPayloadPromptForTest reads the nested prompt string from a decoded
// Qoder payload. Missing or differently typed chat_context, text object or
// text string fails the test through the typed fixture helper.
func qoderPayloadPromptForTest(t *testing.T, payload map[string]any) string {
	t.Helper()
	chatContext := qoderFixtureValue[map[string]any](t, payload["chat_context"])
	return qoderFixtureValue[string](t, chatContext["text"])
}

// qoderPayloadMessageTextForTest returns the first string text block,
// including an empty string, before legacy string content. Nil or text-free
// messages return empty; callers use this permissive reader to compare both
// upstream history encodings.
func qoderPayloadMessageTextForTest(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	// Upstream fixture history can use typed content blocks or legacy string
	// content; ignore unusable blocks before selecting the first readable representation.
	contents, _ := msg["content"].([]any)
	for _, raw := range contents {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	if text, ok := msg["content"].(string); ok {
		return text
	}
	return ""
}

// qoderHeader returns one request-header override for the forwarding fixture
// helpers.
func qoderHeader(key, value string) qoderForwardTestHeader {
	return qoderForwardTestHeader{key: key, value: value}
}

// qoderForwardChatCompletionsForTest forwards Chat input and returns the last
// decoded upstream payload. It requires a QoderClient fixture and successful
// forwarding, discarding response/result values while failing on missing
// captured input.
func qoderForwardChatCompletionsForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	_, _ = qoderForwardChatCompletionsResultAndBodyForTest(t, svc, provider, sessionID, body, headers...)
	return qoderLastUpstreamPayloadForTest(t, qoderFixtureValue[*gatewaytestkit.QoderClient](t, svc.Client))
}

// qoderForwardChatCompletionsResultAndBodyForTest forwards Chat input into an
// in-memory recorder. Nonempty sessionID sets its header before additional
// overrides, which can replace it; forwarding errors fail the test and the
// result/body are returned.
func qoderForwardChatCompletionsResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	for _, header := range headers {
		c.Request.Header.Set(header.key, header.value)
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIChatCompletions)
	require.NoError(t, err)
	return result, rec.Body.String()
}

// qoderForwardMessagesForTest forwards Messages input and returns the latest
// upstream payload. A nonblank sessionID supplies a header before later
// overrides, which can replace it. The helper requires a nonnil result and a
// client exposing body count/index access.
func qoderForwardMessagesForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	result := qoderForwardMessagesResultForTest(t, svc, provider, body, append([]qoderForwardTestHeader{qoderHeader("session_id", sessionID)}, headers...)...)
	require.NotNil(t, result)
	client, ok := svc.Client.(interface {
		// BodyAt returns fixture request bytes for the specified index. Consumers
		// decode valid JSON and require that the requested call exists; this port
		// does not prescribe synchronization or copy behavior beyond its concrete
		// fixture.
		BodyAt(int) []byte
		// BodyCount returns the number of captured fixture request bodies for
		// observation.
		BodyCount() int
	})
	require.True(t, ok)
	return qoderPayloadAtForTest(t, client, client.BodyCount()-1)
}

// qoderForwardMessagesResultForTest returns successful Messages forwarding
// observations while discarding client response bytes. Callers supply the
// bound fixture and header overrides; any forwarding error fails the test in
// the delegated helper.
func qoderForwardMessagesResultForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, body []byte, headers ...qoderForwardTestHeader) *forwardcore.MessagesResult {
	t.Helper()
	result, _ := qoderForwardMessagesResultAndBodyForTest(t, svc, provider, body, headers...)
	return result
}

// qoderForwardMessagesResultAndBodyForTest forwards Messages input with
// nonblank header overrides. X-Test-Claude-Code-Context=true installs the
// test-only detected-client context before forwarding; errors fail the test,
// and result plus recorded response bytes are returned.
func qoderForwardMessagesResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	// Install detection directly for stable-prefix fixtures so conversation behavior
	// can be exercised independently of production client-signature recognition on these bytes.
	if strings.EqualFold(strings.TrimSpace(c.Request.Header.Get("X-Test-Claude-Code-Context")), "true") {
		c.Request = c.Request.WithContext(requeststate.SetClaudeCodeClient(c.Request.Context(), true))
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolAnthropicMessages)
	require.NoError(t, err)
	return result, rec.Body.String()
}

// qoderForwardResponsesResultAndBodyForTest forwards Responses input into an
// in-memory recorder using nonblank header overrides. Forwarding errors fail
// the test, while successful result observations and encoded response bytes
// are returned for assertions.
func qoderForwardResponsesResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, provider *providercore.Record, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, provider, body, protocolcore.ProtocolOpenAIResponses)
	require.NoError(t, err)
	return result, rec.Body.String()
}

// qoderLastUpstreamPayloadForTest decodes the final recorded Qoder request
// payload. A nil client or empty capture fails the test before indexed
// reading; callers receive a decoded object from the fixture’s latest call.
func qoderLastUpstreamPayloadForTest(t *testing.T, client *gatewaytestkit.QoderClient) map[string]any {
	t.Helper()
	require.NotNil(t, client)
	require.NotZero(t, client.BodyCount())
	return qoderPayloadAtForTest(t, client, client.BodyCount()-1)
}

// qoderPayloadAtForTest decodes JSON captured at the specified fixture request
// index. Callers supply a client with an existing valid body; JSON decode
// failure aborts the test before any payload assertion.
func qoderPayloadAtForTest(t *testing.T, client interface {
	// BodyAt returns fixture request bytes for the specified index. Consumers
	// decode valid JSON and require that the requested call exists; this port
	// does not prescribe synchronization or copy behavior beyond its concrete
	// fixture.
	BodyAt(int) []byte
}, index int,
) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(client.BodyAt(index), &payload))
	return payload
}

// newBlockingQoderClientStub creates the blocking request fixture with a
// condition sharing its mutex. Callers wait until the first pipe writer has
// been installed before invoking finishFirst once; later calls consume
// ordinary or one-shot malformed responses.
func newBlockingQoderClientStub(t *testing.T) *blockingQoderClientStub {
	t.Helper()
	client := &blockingQoderClientStub{t: t}
	client.cond = sync.NewCond(&client.mu)
	return client
}

// StreamRequestContext captures request bytes under mu and exposes an open
// pipe for the first accepted call. Callers complete that pipe once or cancel
// its execution context; later calls consume a one-shot malformed response
// flag or return ordinary content and usage.
//
// Body copying and call numbering occur under the lock, while pipe creation
// and HTTP reading progress independently. The cancellation goroutine closes
// the first writer so a waiting reader can exit without test completion. Later
// error consumption clears nextError under the same mutex, preventing one
// injected malformed response from contaminating every subsequent request.
func (s *blockingQoderClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string) (*http.Response, error) {
	s.mu.Lock()
	callNumber := len(s.Bodies) + 1
	// Copy request bytes and publish their count under mu before notification,
	// keeping concurrent inspection independent of the caller’s later byte-slice
	// mutation.
	s.Bodies = append(s.Bodies, append([]byte(nil), bodyJSON...))
	s.Headers = headers
	s.cond.Broadcast()
	s.mu.Unlock()

	if callNumber == 1 {
		reader, writer := io.Pipe()
		s.mu.Lock()
		s.firstWriter = writer
		s.mu.Unlock()
		// The pipe reader can outlive test control while forwarding is blocked;
		// cancellation closes its writer and unblocks reading even without finishFirst.
		go func() {
			<-ctx.Done()
			// Cancellation cleanup observes no separate close result; reader termination
			// is the fixture signal, and the injected upstream failure remains independent.
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Body: reader}, nil
	}
	s.mu.Lock()
	// Consume malformed-response injection once under mu so another concurrent
	// request cannot reuse the same failure after this call claims it.
	nextError := s.nextError
	s.nextError = false
	s.mu.Unlock()
	if nextError {
		body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// waitForCalls waits up to two seconds for the protected request count,
// failing the test on timeout. Callers choose a count expected from started
// forwarding; polling releases the mutex during each ten-millisecond interval.
//
// The count is checked again only after reacquiring the lock, allowing capture
// to progress while the test sleeps. Recording the first body precedes
// installation of its pipe writer, so this count alone does not guarantee
// finishFirst readiness. Although capture broadcasts cond, this helper does
// not use Cond.Wait; the deadline is a test observation budget.
func (s *blockingQoderClientStub) waitForCalls(count int) {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.Bodies) < count {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %d qoder calls, got %d", count, len(s.Bodies))
		}
		s.mu.Unlock()
		// Release the mutex during polling so upstream request capture can increase
		// the observed count before the next bounded readiness check.
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
	}
}

// finishFirst writes content, usage and DONE into the accepted first response
// pipe and closes it. Callers wait until firstWriter is installed and invoke
// it exactly once; waitForCalls alone does not establish that readiness.
// Absent or already-consumed completion ownership fails the test.
//
// The firstDone flag and writer pointer are read under the mutex before pipe
// I/O begins, preventing a second controller from claiming the same
// completion. Pipe writes happen after unlocking so the stream reader can
// progress independently. Write or close failure aborts the test instead of
// pretending that first-stream completion succeeded.
func (s *blockingQoderClientStub) finishFirst() {
	s.t.Helper()
	s.mu.Lock()
	writer := s.firstWriter
	if s.firstDone {
		writer = nil
	}
	// Claim pipe completion while holding mu, then release the lock before I/O
	// so readers progress and a second controller cannot complete this stream again.
	s.firstDone = true
	s.mu.Unlock()
	require.NotNil(s.t, writer)
	_, err := io.WriteString(writer,
		"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n"+
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":11}}\"}\n\n"+
			"data: {\"body\":\"[DONE]\"}\n\n")
	require.NoError(s.t, err)
	// Close the completed first pipe after writing its terminal marker so the
	// independent reader can finish and the test can join forwarding.
	require.NoError(s.t, writer.Close())
}

// BodyAt returns an independent copy of one captured request under the mutex.
// Out-of-range indexes return nil; callers wait for the required capture
// before decoding so the returned bytes represent that recorded request.
func (s *blockingQoderClientStub) BodyAt(index int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.Bodies) {
		return nil
	}
	// Copy captured bytes while mu still protects the indexed entry, allowing the
	// caller to decode or mutate its result without changing fixture storage.
	return append([]byte(nil), s.Bodies[index]...)
}

// BodyCount returns the captured request count under the fixture mutex.
func (s *blockingQoderClientStub) BodyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Bodies)
}

// qoderLargeToolsJSONForTest returns two object-schema tools with deliberately
// large descriptions for replay fixtures. The fixed structure is marshaled as
// JSON; a construction failure panics rather than supplying invalid tool
// input.
func qoderLargeToolsJSONForTest() string {
	// Large descriptions make retained tools significant in replay payload-size assertions.
	description := strings.Repeat("large schema field used by Claude Code. ", 80)
	body, err := json.Marshal([]map[string]any{
		{
			"name":        "Read",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": description,
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"file_path"},
			},
		},
		{
			"name":        "Bash",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": description,
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"command"},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// TestQoderConversationKeyPrefersExplicitSessionOverClaudeCodeStableSeed
// combines Claude client headers with explicit session identity. Conversation
// key source must be header with the expected provider-scoped value. The
// fixture supplies no detected Claude context for stable-prefix matching.
func TestQoderConversationKeyPrefersExplicitSessionOverClaudeCodeStableSeed(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")

	request := qoder.QoderPayloadRequest{
		Model:    "deepseek-v4-pro",
		Messages: []qoder.QoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoder.QoderConversationKey(QoderRequestMetadata(c), 7, request)

	require.Equal(t, "header", source)
	require.Equal(t, qoder.QoderProviderScopedConversationKey(7, "header:"+upstreamcore.IsolateSessionID(0, "header-session")), key)
	require.NotContains(t, key, "stable_seed")
}

// TestQoderConversationKeyPrefersMetadataOverClaudeCodeStableSeed combines
// Claude client headers with metadata session identity. Conversation key
// source and expected key must use metadata and provider scope. The fixture
// supplies no detected Claude context for stable-prefix matching.
func TestQoderConversationKeyPrefersMetadataOverClaudeCodeStableSeed(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")

	request := qoder.QoderPayloadRequest{
		Model:          "deepseek-v4-pro",
		MetadataUserID: anthropic.FormatMetadataUserID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "session-123", "2.1.80"),
		Messages:       []qoder.QoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoder.QoderConversationKey(QoderRequestMetadata(c), 7, request)

	require.Equal(t, "metadata_user_id", source)
	require.Equal(t, qoder.QoderProviderScopedConversationKey(7, "metadata_user_id:"+upstreamcore.IsolateSessionID(0, "session-123")), key)
	require.NotContains(t, key, "stable_seed")
}

// TestQoderGatewayNonStreamingReadDoesNotCommitResponseBeforeError reads a
// wrapped upstream authentication failure without a client output sink. It
// must return an error with no events, response bytes, cache headers or
// written writer state.
func TestQoderGatewayNonStreamingReadDoesNotCommitResponseBeforeError(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := qoder.ReadQoderSSEEventsContext(context.Background(), resp)

	require.Error(t, err)
	require.Empty(t, events)
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Get("Cache-Control"))
	require.False(t, c.Writer.Written())
}

// TestQoderPartialUsageOnError writes content and usage before failure through
// all three protocol converters. Each must return the upstream error with
// observed partial usage and output, preserving served accounting despite
// failure.
func TestQoderPartialUsageOnError(t *testing.T) {
	body := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "local fixture upstream failure"})
	writers := map[string]func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error){
		"chat": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderOpenAIStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", r)
		},
		"messages": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderAnthropicStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", r)
		},
		"responses": func(ctx context.Context, c *gin.Context, r *http.Response) (*qoder.QoderStreamResult, error) {
			return qoder.WriteQoderResponsesStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", r)
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			result, err := write(context.Background(), c, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
			if err == nil {
				t.Fatal("fixture must produce upstream error")
			}
			if !strings.Contains(rec.Body.String(), "served") {
				t.Fatal("fixture must emit real output")
			}
			if result == nil {
				t.Fatalf("observed input=12 output=3 before upstream error but result=nil; written=%d", rec.Body.Len())
			}
			if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 3 || !result.HasOutput {
				t.Fatalf("lost partial result: %+v", result)
			}
		})
	}
}

// Close marks the fixture reader closed and returns no error.
func (r *qoderTrackingReadCloser) Close() error {
	r.closed = true
	return nil
}

// qoderNoIndexNamedParallelToolCallEventsForTest returns three complete named
// calls without reported indexes and a DONE event.
func qoderNoIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`},
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"ls -la","description":"List files in current directory"}`},
		{Type: "tool_call_delta", ToolName: "glob", Arguments: `{"pattern":"**/*.md"}`},
		{IsDone: true},
	}
}

// qoderNoIndexNamedParallelToolCallsWrappedSSEForTest returns wrapped parallel
// call JSON with no indexes. Its payload keeps each complete argument object
// separate, then appends wrapped DONE for the production parser fixture.
func qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t *testing.T) string {
	t.Helper()
	return qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`}},
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls -la","description":"List files in current directory"}`}},
			map[string]any{"type": "function", "function": map[string]any{"name": "glob", "arguments": `{"pattern":"**/*.md"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
}

// qoderRepeatedIndexNamedParallelToolCallEventsForTest returns three distinct
// named calls reusing reported index zero before DONE.
func qoderRepeatedIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"pwd","description":"Print working directory"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "glob", Arguments: `{"pattern":"docs/*.md"}`},
		{IsDone: true},
	}
}

// qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest returns three
// wrapped call deltas reusing index zero. Each carries a complete named
// argument object, letting production parsing distinguish separate calls
// before the appended DONE.
func qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest(t *testing.T) string {
	t.Helper()
	return qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd","description":"Print working directory"}`}},
		}}},
	}}) +
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`}},
			}}},
		}}) +
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "glob", "arguments": `{"pattern":"docs/*.md"}`}},
			}}},
		}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
}

// TestQoderGatewayResponsesStreamCompletedOutputIncludesTextMessage streams
// text through wrapped Qoder input into Responses. Its completed response must
// include the joined text message and its client-facing output item metadata.
func TestQoderGatewayResponsesStreamCompletedOutputIncludesTextMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"content": "Hello "}},
		}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "world"}},
			}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Len(t, completed.Get("response.output").Array(), 1)
	require.Equal(t, "message", completed.Get("response.output.0.type").String())
	require.Equal(t, "assistant", completed.Get("response.output.0.role").String())
	require.Equal(t, "completed", completed.Get("response.output.0.status").String())
	require.Equal(t, "output_text", completed.Get("response.output.0.content.0.type").String())
	require.Equal(t, "Hello world", completed.Get("response.output.0.content.0.text").String())
}

// TestQoderGatewayResponsesStreamClosesUpstreamBody passes a tracked reader
// into streaming Responses conversion. The response writer must finish without
// error and the upstream body’s close flag must be set after conversion.
func TestQoderGatewayResponsesStreamClosesUpstreamBody(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := &qoderTrackingReadCloser{Reader: strings.NewReader(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"content": "ok"}},
		}}) +
			"data: {\"body\":\"[DONE]\"}\n\n",
	)}
	resp := &http.Response{Body: body}

	_, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, body.closed)
}

// TestQoderGatewayOpenAIStreamUsageRequiresIncludeUsage streams wrapped text
// and usage with default Chat options. Internal input/output usage must remain
// available while no client usage member is emitted without explicit
// inclusion.
func TestQoderGatewayOpenAIStreamUsageRequiresIncludeUsage(t *testing.T) {
	respBody := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"content": "ok"}},
	}}) +
		qoderWrappedSSELineForTest(t, map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 3,
				"total_tokens":      15,
			},
		}) +
		"data: {\"body\":\"[DONE]\"}\n\n"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(respBody))}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
	require.NoError(t, err)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), `"usage"`)
}

// TestQoderGatewayOpenAIStreamUsageChunkShapeWhenIncluded enables client Chat
// usage on a wrapped stream. Output must include a usage chunk with empty
// choices and the supplied input/output token values.
func TestQoderGatewayOpenAIStreamUsageChunkShapeWhenIncluded(t *testing.T) {
	respBody := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"content": "ok"}},
	}}) +
		qoderWrappedSSELineForTest(t, map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 3,
				"total_tokens":      15,
			},
		}) +
		"data: {\"body\":\"[DONE]\"}\n\n"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(respBody))}

	_, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp, qoder.QoderOpenAIStreamIncludeUsage(true))
	require.NoError(t, err)
	body := rec.Body.String()
	require.Contains(t, body, `"usage"`)
	require.Contains(t, body, `"choices":[]`)
	require.Contains(t, body, `"prompt_tokens":12`)
	require.Contains(t, body, `"completion_tokens":3`)
}

// TestQoderGatewayStreamClientDisconnectStillCollectsUsage fails client body
// writes after initial output for Chat, Messages and Responses. Each converter
// must return measured tail usage and output evidence without turning the
// simulated disconnect into a forwarding error.
func TestQoderGatewayStreamClientDisconnectStillCollectsUsage(t *testing.T) {
	respBody := qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"content": "ok"}},
	}}) +
		qoderWrappedSSELineForTest(t, map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 3,
				"total_tokens":      15,
			},
		}) +
		"data: {\"body\":\"[DONE]\"}\n\n"

	tests := []struct {
		name  string                                                                                // protocol or mapping case label
		write func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error) // selected protocol response writer
	}{
		{
			name: "openai chat completions",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderOpenAIStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
			},
		},
		{
			name: "anthropic messages",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderAnthropicStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
			},
		},
		{
			name: "responses",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderResponsesStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			c.Writer = &qoderFailingHTTPWriter{ResponseWriter: c.Writer, failAfter: 1}
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(respBody))}

			result, err := tt.write(context.Background(), c, resp)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.HasOutput)
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
		})
	}
}

// TestQoderGatewayStreamWritersDoNotWriteBeforeUpstreamError supplies an
// initial wrapped agent-limit failure to all protocol writers. Each must
// return that classified error without a result or any written client bytes,
// preserving failover eligibility.
func TestQoderGatewayStreamWritersDoNotWriteBeforeUpstreamError(t *testing.T) {
	// Wrapped agent-limit reset fields use Unix epoch milliseconds.
	errorLine := qoderWrappedErrorSSELineForTest(t, http.StatusTooManyRequests, map[string]any{
		"code":                "115",
		"message":             "agent limit",
		"agentLimitResetTime": time.Date(2026, 7, 12, 7, 28, 9, 0, time.UTC).UnixMilli(),
	})

	tests := []struct {
		name  string                                                                                // protocol or mapping case label
		write func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error) // selected protocol response writer
	}{
		{
			name: "openai chat completions",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderOpenAIStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "qwen3.7-plus", resp)
			},
		},
		{
			name: "anthropic messages",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderAnthropicStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "qwen3.7-plus", resp)
			},
		},
		{
			name: "responses",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderResponsesStreamResponse(ctx, upstreamcore.NewOutputContext(NewResponseSink(c)), "qwen3.7-plus", resp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(errorLine))}

			result, err := tt.write(context.Background(), c, resp)
			require.Error(t, err)
			require.Nil(t, result)
			var apiErr *qoder.APIError
			require.ErrorAs(t, err, &apiErr)
			require.True(t, apiErr.IsAgentLimit())
			require.Equal(t, -1, c.Writer.Size(), "handler failover depends on no bytes being written before the upstream error")
			require.Empty(t, rec.Body.String())
		})
	}
}

// TestQoderGatewayResponsesStreamMapsReasoningDelta streams reasoning
// fragments before answer text. Responses events and completed output must
// retain joined reasoning followed by the answer message and their appropriate
// item types.
func TestQoderGatewayResponsesStreamMapsReasoningDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"reasoning_content": "think "}},
		}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"reasoning_content": "first"}},
			}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "answer"}},
			}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	var sawReasoningAdded, sawReasoningDelta, sawReasoningDone bool
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "reasoning" {
				sawReasoningAdded = true
			}
		case "response.reasoning_summary_text.delta":
			if event.Get("delta").String() == "think " || event.Get("delta").String() == "first" {
				sawReasoningDelta = true
			}
		case "response.output_item.done":
			if event.Get("item.type").String() == "reasoning" && event.Get("item.summary.0.text").String() == "think first" {
				sawReasoningDone = true
			}
		}
	}
	require.True(t, sawReasoningAdded, rec.Body.String())
	require.True(t, sawReasoningDelta, rec.Body.String())
	require.True(t, sawReasoningDone, rec.Body.String())

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Equal(t, "reasoning", completed.Get("response.output.0.type").String())
	require.Equal(t, "think first", completed.Get("response.output.0.summary.0.text").String())
	require.Equal(t, "message", completed.Get("response.output.1.type").String())
	require.Equal(t, "answer", completed.Get("response.output.1.content.0.text").String())
}

// TestQoderGatewayResponsesStreamAllowsTextAfterReasoningInterleave streams
// text, reasoning and more text in that order. Completed Responses output must
// preserve three corresponding items rather than lose or merge text across the
// reasoning boundary.
func TestQoderGatewayResponsesStreamAllowsTextAfterReasoningInterleave(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"content": "first"}},
		}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"reasoning_content": "think"}},
			}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "second"}},
			}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Equal(t, "message", completed.Get("response.output.0.type").String())
	require.Equal(t, "first", completed.Get("response.output.0.content.0.text").String())
	require.Equal(t, "reasoning", completed.Get("response.output.1.type").String())
	require.Equal(t, "think", completed.Get("response.output.1.summary.0.text").String())
	require.Equal(t, "message", completed.Get("response.output.2.type").String())
	require.Equal(t, "second", completed.Get("response.output.2.content.0.text").String())
}

// TestQoderGatewayResponsesStreamCompletedOutputIncludesFunctionCalls streams
// one mapped Bash function call through Responses. Completed output must
// retain call identity, declared lowercase name, valid JSON arguments and
// completed item status.
func TestQoderGatewayResponsesStreamCompletedOutputIncludesFunctionCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
			}}},
		}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "deepseek-v4-pro", resp, qoder.QoderResponsesStreamToolNameMapper(qoder.QoderDeclaredToolNameMapper([]any{map[string]any{"type": "function", "name": "bash"}})))
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Len(t, completed.Get("response.output").Array(), 1)
	require.Equal(t, "function_call", completed.Get("response.output.0.type").String())
	require.Equal(t, "call_1", completed.Get("response.output.0.call_id").String())
	require.Equal(t, "bash", completed.Get("response.output.0.name").String())
	require.Equal(t, "completed", completed.Get("response.output.0.status").String())
	require.JSONEq(t, `{"command":"pwd"}`, completed.Get("response.output.0.arguments").String())
}

// TestQoderGatewayWritesResponsesStreamKeepsNoIndexNamedParallelFunctionCalls
// streams three named calls with no indexes, including repeated names.
// Responses output must keep three separate items and valid argument objects
// without concatenating unrelated JSON.
func TestQoderGatewayWritesResponsesStreamKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	addedNames := make([]string, 0)
	argsDone := make([]string, 0)
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				addedNames = append(addedNames, event.Get("item.name").String())
			}
		case "response.function_call_arguments.done":
			arguments := event.Get("arguments").String()
			require.NotContains(t, arguments, `}{`)
			argsDone = append(argsDone, arguments)
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, addedNames)
	require.Len(t, argsDone, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, argsDone[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, argsDone[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, argsDone[2])
}

// TestQoderGatewayWritesResponsesStreamKeepsRepeatedIndexNamedParallelFunctionCalls
// streams distinct named calls that all report index zero. Responses output
// must retain three separate names and argument objects rather than combine
// the reused-index calls.
func TestQoderGatewayWritesResponsesStreamKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	addedNames := make([]string, 0)
	argsDone := make([]string, 0)
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				addedNames = append(addedNames, event.Get("item.name").String())
			}
		case "response.function_call_arguments.done":
			arguments := event.Get("arguments").String()
			require.NotContains(t, arguments, `}{`)
			argsDone = append(argsDone, arguments)
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, addedNames)
	require.Len(t, argsDone, 3)
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, argsDone[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, argsDone[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, argsDone[2])
}

// TestQoderGatewayWritesResponsesStreamDoesNotReserveOutputIndexForTypeOnlyPlaceholder
// starts with a type-only tool placeholder before a real call. Responses must
// assign output index zero to the real named call instead of reserving an
// empty item.
func TestQoderGatewayWritesResponsesStreamDoesNotReserveOutputIndexForTypeOnlyPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"type": "function"},
				}}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"tool_calls": []any{
						map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
					}}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n")),
	}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	for _, event := range events {
		if event.Get("type").String() != "response.output_item.added" || event.Get("item.type").String() != "function_call" {
			continue
		}
		require.Equal(t, int64(0), event.Get("output_index").Int(), event.Raw)
		require.Equal(t, "Bash", event.Get("item.name").String())
		return
	}
	t.Fatalf("function_call output_item.added not found in %s", rec.Body.String())
}

// TestQoderGatewayWritesOpenAIStream converts typed text and reasoning
// fixtures to Chat output. It must emit assistant/text deltas and stop plus
// DONE while omitting the hidden reasoning text.
func TestQoderGatewayWritesOpenAIStream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

// TestQoderGatewayWritesOpenAIToolCallsStream converts split arguments for one
// named tool call. Chat chunks must retain its index, ID and lowercase name
// while carrying both fragments and tool_calls completion.
func TestQoderGatewayWritesOpenAIToolCallsStream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"index":0`)
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, `"arguments":"{\"cmd\":"`)
	require.Contains(t, body, `"arguments":"\"pwd\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayWritesOpenAIToolCallsStreamSkipsEmptyArgumentPlaceholder
// supplies an empty-object placeholder before real tool arguments. Chat must
// emit the complete argument object without prefixing it with the placeholder
// or duplicating JSON objects.
func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsEmptyArgumentPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"arguments":"{}"`)
	require.NotContains(t, body, `"arguments":"{}{\"command\"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayWritesOpenAIToolCallsStreamSkipsTypeOnlyPlaceholderChunk
// starts with a tool type lacking call data. Every emitted tool_calls array
// must contain an entry, and the body must include the named call, its
// arguments and tool_calls completion.
func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsTypeOnlyPlaceholderChunk(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	chunks := qoderOpenAIStreamChunksForTest(t, rec.Body.String())
	for _, chunk := range chunks {
		toolCalls := gjson.GetBytes(chunk, "choices.0.delta.tool_calls")
		if toolCalls.Exists() {
			require.NotEqual(t, int64(0), toolCalls.Get("#").Int(), string(chunk))
		}
	}
	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayWritesOpenAIToolCallsStreamMergesIndexDriftForSameCall
// changes the reported index while continuing one call ID. Chat must keep that
// call on index zero while emitting both argument fragments, with no chunk
// assigned index one.
func TestQoderGatewayWritesOpenAIToolCallsStreamMergesIndexDriftForSameCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, `"arguments":"{\"cmd\":"`)
	require.Contains(t, body, `"arguments":"\"pwd\"}"`)
	require.NotContains(t, body, `"index":1`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayWritesOpenAIToolCallsStreamKeepsParallelCallIndexes
// interleaves arguments for two identified tool calls. Chat output must attach
// each argument object to the corresponding stable index rather than
// cross-associate parallel calls.
func TestQoderGatewayWritesOpenAIToolCallsStreamKeepsParallelCallIndexes(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	chunks := qoderOpenAIStreamChunksForTest(t, rec.Body.String())
	toolDeltas := make([]map[string]any, 0)
	for _, chunk := range chunks {
		rawDeltas := gjson.GetBytes(chunk, "choices.0.delta.tool_calls").Array()
		for _, rawDelta := range rawDeltas {
			var delta map[string]any
			require.NoError(t, json.Unmarshal([]byte(rawDelta.Raw), &delta))
			toolDeltas = append(toolDeltas, delta)
		}
	}
	require.Len(t, toolDeltas, 4)
	require.Equal(t, float64(0), toolDeltas[2]["index"])
	require.Equal(t, `{"path":"a"}`, qoderFixtureValue[map[string]any](t, toolDeltas[2]["function"])["arguments"])
	require.Equal(t, float64(1), toolDeltas[3]["index"])
	require.Equal(t, `{"path":"b"}`, qoderFixtureValue[map[string]any](t, toolDeltas[3]["function"])["arguments"])
}

// TestQoderGatewayWritesOpenAIToolCallsStreamDropsAmbiguousParallelArgumentDelta
// follows two parallel calls with an unassociated argument fragment. Chat must
// retain both call IDs, omit the fragment and emit no tool chunk assigned
// index two.
func TestQoderGatewayWritesOpenAIToolCallsStreamDropsAmbiguousParallelArgumentDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"id":"call_2"`)
	require.NotContains(t, body, "lost")
	require.NotContains(t, body, `"index":2`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayWritesOpenAIStreamParsesXMLTextToolCall feeds XML-style tool
// text through Chat conversion. Output must contain the Read function and
// valid file-path arguments with tool completion, while removing raw tool
// tags.
func TestQoderGatewayWritesOpenAIStreamParsesXMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"arguments":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

// TestQoderGatewayWritesOpenAIStreamParsesDSMLTextToolCall feeds DSML
// invocation text through Chat conversion. Output must contain Bash with
// parsed command/description arguments and tool completion, without leaking
// DSML or invoke markup.
func TestQoderGatewayWritesOpenAIStreamParsesDSMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := writeQoderOpenAIEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

// TestQoderGatewayWritesAnthropicStream converts typed text and reasoning
// fixtures into Messages events. It must expose thinking blocks/deltas, text
// content and a complete message lifecycle through message_stop.
func TestQoderGatewayWritesAnthropicStream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"type":"thinking_delta"`)
	require.Contains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

// TestQoderGatewayWritesAnthropicStreamParsesXMLTextToolCall feeds XML-style
// tool text through Messages conversion. A Read tool_use block and JSON delta
// must replace raw tags and end with the tool_use stop reason.
func TestQoderGatewayWritesAnthropicStreamParsesXMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

// TestQoderGatewayWritesAnthropicStreamParsesDSMLTextToolCall feeds DSML
// invocation text through Messages conversion. Bash tool arguments must
// preserve command and description, with tool_use completion and no raw
// DSML/invoke markup.
func TestQoderGatewayWritesAnthropicStreamParsesDSMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

// TestQoderGatewayWritesAnthropicStreamParsesJSONTextToolCall feeds JSON tool
// text using shell through Messages conversion. It must normalize to Bash with
// valid command/description input instead of misreading the encoded object as
// a tool name.
func TestQoderGatewayWritesAnthropicStreamParsesJSONTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"Print working directory\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, `"name":"{\"name\"`)
	require.NotContains(t, body, "No such tool")
}

// TestQoderGatewayWritesAnthropicToolUseStream converts one complete argument
// object for a named tool call. Messages must emit its tool_use block and
// complete JSON input, followed by block closure and tool-use message completion.
func TestQoderGatewayWritesAnthropicToolUseStream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.Contains(t, body, "event: message_stop")
}

// TestQoderGatewayWritesAnthropicToolUseStreamKeepsSplitArgumentsInOneBlock
// splits one tool argument object into successive typed events. Messages must
// emit one tool block and one complete input JSON delta rather than split the
// call into blocks.
func TestQoderGatewayWritesAnthropicToolUseStreamKeepsSplitArgumentsInOneBlock(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`))
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`))
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamDoesNotFinalizeEmptyObjectPlaceholder
// supplies an empty input placeholder before full named arguments. Messages
// must preserve the call identity and one valid complete delta without
// emitting placeholder input or an empty name.
func TestQoderGatewayWritesAnthropicToolUseStreamDoesNotFinalizeEmptyObjectPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "toolu_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolType: "function", Arguments: `{"command":"printf cc-single-20260617","description":"Print single nonce"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`), body)
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`), body)
	streamEvents := qoderAnthropicStreamEventsForTest(t, body)
	var toolBlock map[string]any
	var partialJSON string
	for _, event := range streamEvents {
		if event.Event == "content_block_start" {
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolBlock = block
			}
		}
		if event.Event == "content_block_delta" {
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partialJSON = qoderFixtureValue[string](t, delta["partial_json"])
			}
		}
	}
	require.Equal(t, "toolu_1", toolBlock["id"])
	require.Equal(t, "Bash", toolBlock["name"])
	require.JSONEq(t, `{"command":"printf cc-single-20260617","description":"Print single nonce"}`, partialJSON)
	require.NotContains(t, body, `"partial_json":"{}"`)
	require.NotContains(t, body, `"name":""`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamSkipsTypeOnlyPlaceholder
// supplies only a tool-type placeholder without actual call identity or
// arguments. Messages must omit tool blocks/deltas and finish the turn
// normally rather than invent tool use.
func TestQoderGatewayWritesAnthropicToolUseStreamSkipsTypeOnlyPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.NotContains(t, body, `"type":"tool_use"`)
	require.NotContains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"stop_reason":"end_turn"`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamKeepsNoIndexNamedParallelToolCalls
// supplies three named parallel calls without indexes. Messages must retain
// three names and separate valid input objects with tool-use completion,
// avoiding merged unrelated JSON.
func TestQoderGatewayWritesAnthropicToolUseStreamKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, qoderFixtureValue[string](t, block["name"]))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := qoderFixtureValue[string](t, delta["partial_json"])
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamKeepsRepeatedIndexNamedParallelToolCalls
// supplies three distinct named calls sharing index zero. Messages must
// preserve separate tool blocks and arguments rather than concatenate each
// reused-index object into the previous call.
func TestQoderGatewayWritesAnthropicToolUseStreamKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, qoderFixtureValue[string](t, block["name"]))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := qoderFixtureValue[string](t, delta["partial_json"])
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamKeepsSameIndexNewIDSplitArgumentsAligned
// changes call IDs while reusing an index and splitting arguments. Messages
// must retain three named calls with their correct full objects, without empty
// names or merged JSON.
func TestQoderGatewayWritesAnthropicToolUseStreamKeepsSameIndexNewIDSplitArgumentsAligned(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "thinking"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"command":"pwd`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"command":"printf OPENCODE_`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `PARALLEL_OK"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_3", ToolType: "function", ToolName: "glob"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"pattern":"docs/*.md`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", events)
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, qoderFixtureValue[string](t, block["name"]))
				require.NotEmpty(t, block["id"], rec.Body.String())
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				inputDeltas = append(inputDeltas, qoderFixtureValue[string](t, delta["partial_json"]))
			}
		}
	}
	require.Equal(t, []string{"bash", "bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, inputDeltas[2])
	require.NotContains(t, rec.Body.String(), `"name":""`)
	require.NotContains(t, rec.Body.String(), `}{`)
}

// TestQoderGatewayWritesAnthropicToolUseStreamKeepsParallelCallIndexes
// interleaves two identified tool calls and their arguments. Each Messages
// JSON delta must belong to its open tool block, with both indexes closed
// after their respective input.
func TestQoderGatewayWritesAnthropicToolUseStreamKeepsParallelCallIndexes(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 2, strings.Count(body, `"type":"tool_use"`))
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"id":"call_2"`)
	streamEvents := qoderAnthropicStreamEventsForTest(t, body)
	inputDeltas := make(map[int]string)
	openToolBlocks := make(map[int]bool)
	for _, event := range streamEvents {
		if event.Event == "content_block_start" {
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				openToolBlocks[int(qoderFixtureValue[float64](t, event.Data["index"]))] = true
			}
			continue
		}
		if event.Event == "content_block_stop" {
			delete(openToolBlocks, int(qoderFixtureValue[float64](t, event.Data["index"])))
			continue
		}
		if event.Event != "content_block_delta" {
			continue
		}
		delta, _ := event.Data["delta"].(map[string]any)
		if delta["type"] != "input_json_delta" {
			continue
		}
		index := int(qoderFixtureValue[float64](t, event.Data["index"]))
		require.True(t, openToolBlocks[index], "input delta must be inside an open tool_use block")
		inputDeltas[index] = qoderFixtureValue[string](t, delta["partial_json"])
	}
	require.Equal(t, `{"path":"a"}`, inputDeltas[0])
	require.Equal(t, `{"path":"b"}`, inputDeltas[1])
	require.Empty(t, openToolBlocks)
}

// TestQoderGatewayStreamsResponseWithoutPrebuffering passes an in-memory
// wrapped text stream through Chat conversion. Output must contain
// assistant/text chunks and DONE with HTTP 200, while returned usage remains
// zero because this fixture supplies no usage report.
func TestQoderGatewayStreamsResponseWithoutPrebuffering(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

// TestQoderGatewayStreamsOpenAIResponseParsesXMLTextToolCall parses wrapped
// XML tool text into streaming Chat output. It must expose Read and valid
// file-path arguments with tool completion, remove markup and leave unreported
// usage zero.
func TestQoderGatewayStreamsOpenAIResponseParsesXMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": qoderXMLToolCallFixture}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"arguments":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

// TestQoderGatewayStreamsOpenAIResponseMapsToolNameToDeclaredOpenAITool
// streams upstream Bash with a lowercase client declaration. Output must
// expose bash with tool_calls completion and no upstream spelling, while
// absent usage remains zero.
func TestQoderGatewayStreamsOpenAIResponseMapsToolNameToDeclaredOpenAITool(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}
	tools := []any{map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":       "bash",
			"parameters": map[string]any{"type": "object"},
		},
	}}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp, qoder.QoderOpenAIStreamToolNameMapper(qoder.QoderDeclaredToolNameMapper(tools)))
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"bash"`)
	require.NotContains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

// TestQoderGatewayStreamsAnthropicResponseMapsThinking streams wrapped
// reasoning and text through Messages output. Thinking blocks/deltas and text
// must survive through message_stop, with zero returned usage when no report
// exists.
func TestQoderGatewayStreamsAnthropicResponseMapsThinking(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"type":"thinking_delta"`)
	require.Contains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

// TestQoderGatewayStreamsAnthropicResponseParsesXMLTextToolCall streams
// wrapped XML tool text through Messages conversion. Read tool_use and parsed
// file-path input must replace markup and preserve tool completion without
// invented usage.
func TestQoderGatewayStreamsAnthropicResponseParsesXMLTextToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "<tool_call>Re"}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"content": "ad<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>"}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"partial_json":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

// TestQoderGatewayStreamsAnthropicResponseMapsFlatToolCallInput supplies flat
// structured tool-call input through the wrapped parser. Messages must retain
// call ID and Bash identity with valid command JSON and tool-use completion.
func TestQoderGatewayStreamsAnthropicResponseMapsFlatToolCallInput(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"tool_call_id": "call_1", "name": "Bash", "arguments": map[string]any{"command": "pwd"}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	var toolStart map[string]any
	var partialJSON string
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolStart = block
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partialJSON += qoderFixtureValue[string](t, delta["partial_json"])
			}
		}
	}
	require.NotNil(t, toolStart)
	require.Equal(t, "call_1", toolStart["id"])
	require.Equal(t, "Bash", toolStart["name"])
	require.JSONEq(t, `{"command":"pwd"}`, partialJSON)
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

// TestQoderGatewayWritesAnthropicResponseKeepsNoIndexNamedParallelToolCalls
// parses wrapped parallel calls with names but no indexes. Messages must
// retain three tool identities and separate input objects without merged JSON,
// ending with tool-use completion.
func TestQoderGatewayWritesAnthropicResponseKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, qoderFixtureValue[string](t, block["name"]))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := qoderFixtureValue[string](t, delta["partial_json"])
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

// TestQoderGatewayStreamsAnthropicResponseReplacesEmptyToolArguments streams
// an empty argument placeholder followed by complete input. Messages must
// expose the complete command/description object without concatenating it to
// the earlier empty object.
func TestQoderGatewayStreamsAnthropicResponseReplacesEmptyToolArguments(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": "{}"}},
				}}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"tool_calls": []any{
						map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"arguments": map[string]any{"command": "pwd", "description": "Print working directory"}}},
					}}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstreamcore.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"Print working directory\"}"`)
	require.NotContains(t, body, `"partial_json":"{}{\"command\"`)
}

// TestQoderGatewayStreamsAnthropicResponseRejectsMalformedToolArguments
// supplies malformed wrapped tool arguments to Messages conversion. The writer
// must return a descriptive malformed-arguments error and no stream result
// rather than accepting invalid JSON input.
func TestQoderGatewayStreamsAnthropicResponseRejectsMalformedToolArguments(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"cmd":`}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

// TestQoderGatewayStreamsAnthropicResponseCompletesEmptyContentBlock supplies
// usage and DONE without semantic content. Messages must emit an empty text
// block with normal closure, end_turn and message_stop while retaining
// measured input usage.
func TestQoderGatewayStreamsAnthropicResponseCompletesEmptyContentBlock(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"content_block":{"text":"","type":"text"}`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, `"stop_reason":"end_turn"`)
	require.Contains(t, body, "event: message_stop")
}

// TestQoderGatewayStreamsAnthropicResponseCompletesOnEOFWithoutDone ends a
// usage-only wrapped stream at EOF without DONE. Messages must emit an empty
// text lifecycle and exactly one message_stop while retaining the upstream
// input count.
func TestQoderGatewayStreamsAnthropicResponseCompletesOnEOFWithoutDone(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"content_block":{"text":"","type":"text"}`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, "event: message_delta")
	require.Contains(t, body, `"stop_reason":"end_turn"`)
	require.Contains(t, body, "event: message_stop")
	require.Equal(t, 1, strings.Count(body, "event: message_stop"))
}

// TestQoderGatewayWritesAnthropicStreamNormalizesExecuteBashToolCall supplies
// execute_bash tool text through typed conversion. Messages output must
// normalize its name to Bash and preserve valid command arguments without
// exposing the original tool spelling.
func TestQoderGatewayWritesAnthropicStreamNormalizesExecuteBashToolCall(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "execute_bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := writeQoderAnthropicEvents(t, upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"name":"execute_bash"`)
}

// TestQoderGatewayStreamsOpenAIUsageForBillingAndClientWhenRequested enables
// Chat client usage on wrapped upstream input. Returned billing counts and the
// empty-choices client usage chunk must retain measured input/output totals
// before DONE.
func TestQoderGatewayStreamsOpenAIUsageForBillingAndClientWhenRequested(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":11}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp, qoder.QoderOpenAIStreamIncludeUsage(true))

	require.NoError(t, err)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"prompt_tokens":5`)
	require.Contains(t, body, `"completion_tokens":6`)
	require.Contains(t, body, `"total_tokens":11`)
	usageChunk := qoderOpenAIUsageChunkForTest(t, body)
	require.Len(t, usageChunk.Get("choices").Array(), 0, usageChunk.Raw)
	require.Contains(t, body, "data: [DONE]\n\n")
}

// TestQoderGatewayStreamsOpenAIUsageForBillingWithoutClientChunkByDefault
// streams measured usage without client inclusion options. Returned accounting
// must retain input/output counts while client chunks omit usage and still
// contain text plus DONE.
func TestQoderGatewayStreamsOpenAIUsageForBillingWithoutClientChunkByDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "Hi"}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{
					"prompt_tokens":     5,
					"completion_tokens": 6,
					"total_tokens":      11,
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "auto", resp)

	require.NoError(t, err)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.NotContains(t, body, `"usage":`)
	require.Contains(t, body, `"delta":{"content":"Hi"}`)
	require.Contains(t, body, "data: [DONE]\n\n")
}

// TestQoderGatewayStreamsAnthropicUsageForBillingAndClient streams measured
// input and output usage into Messages. Both returned accounting and client
// usage must retain those values, with the normal message_stop event
// completing output.
func TestQoderGatewayStreamsAnthropicUsageForBillingAndClient(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":9,\\\"total_tokens\\\":17}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), upstreamcore.NewOutputContext(NewResponseSink(c)), "claude-opus-4-6", resp)

	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"input_tokens":8`)
	require.Contains(t, body, `"output_tokens":9`)
	require.Contains(t, body, "event: message_stop")
}

// qoderOpenAIStreamChunksForTest returns valid JSON chunks from fixture
// data-space fields. Empty data, DONE and other lines are ignored; malformed
// selected JSON fails the test, and callers supply this fixed fixture layout.
func qoderOpenAIStreamChunksForTest(t *testing.T, stream string) [][]byte {
	t.Helper()
	// Select only data-space fixture lines and ignore transport sentinels before
	// validating JSON, so chunk assertions inspect client objects rather than SSE
	// framing.
	chunks := make([][]byte, 0)
	for block := range strings.SplitSeq(stream, "\n\n") {
		for line := range strings.SplitSeq(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "" || data == "[DONE]" {
				continue
			}
			require.True(t, gjson.Valid(data), "invalid SSE JSON data: %s", data)
			chunks = append(chunks, []byte(data))
		}
	}
	return chunks
}

// qoderWrappedSSELineForTest encodes a JSON object as the string-valued body
// of a Qoder wrapper. Marshal failures abort the test; the returned data-space
// frame includes its blank separator for production parsing.
func qoderWrappedSSELineForTest(t *testing.T, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	wrapper, err := json.Marshal(map[string]string{"body": string(body)})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}

// qoderWrappedErrorSSELineForTest encodes a Qoder error wrapper with JSON body
// text and source statusCodeValue. Marshal failures abort the test; callers
// select the status independently of body error codes.
func qoderWrappedErrorSSELineForTest(t *testing.T, statusCode int, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	wrapper, err := json.Marshal(map[string]any{
		"body":            string(body),
		"statusCodeValue": statusCode,
	})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}

// qoderOpenAIUsageChunkForTest finds the first valid fixture JSON chunk
// containing usage. It ignores empty, DONE, malformed and unsupported lines,
// but fails the test when no selected usage chunk exists.
func qoderOpenAIUsageChunkForTest(t *testing.T, body string) gjson.Result {
	t.Helper()
	// Skip unsupported or invalid fixture lines while finding usage; the required
	// usage result still fails explicitly when no usable client chunk contains
	// that member.
	for frame := range strings.SplitSeq(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		for line := range strings.SplitSeq(frame, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "" || data == "[DONE]" || !gjson.Valid(data) {
				continue
			}
			chunk := gjson.Parse(data)
			if chunk.Get("usage").Exists() {
				return chunk
			}
		}
	}
	t.Fatalf("OpenAI usage chunk not found in %s", body)
	return gjson.Result{}
}

// qoderResponsesStreamEventsForTest returns JSON from supported Responses
// data-space fields. Empty frames, comment-prefixed frames and DONE are
// ignored; selected malformed JSON fails the test, and callers supply this
// fixture serialization.
func qoderResponsesStreamEventsForTest(t *testing.T, body string) []gjson.Result {
	t.Helper()
	// Keep comment-prefixed fixture frames outside event assertions, then
	// validate selected JSON fields so Responses identity checks compare actual
	// client event objects.
	var events []gjson.Result
	for frame := range strings.SplitSeq(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" || strings.HasPrefix(frame, ":") {
			continue
		}
		for line := range strings.SplitSeq(frame, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "" || data == "[DONE]" {
				continue
			}
			require.True(t, gjson.Valid(data), "invalid Responses SSE JSON data: %s", data)
			events = append(events, gjson.Parse(data))
		}
	}
	return events
}

// qoderResponsesCompletedEventForTest returns the first decoded
// response.completed fixture event. Its absence fails the test rather than
// supplying a usable empty result, so callers can assert terminal response
// contents.
func qoderResponsesCompletedEventForTest(t *testing.T, body string) gjson.Result {
	t.Helper()
	for _, event := range qoderResponsesStreamEventsForTest(t, body) {
		if event.Get("type").String() == "response.completed" {
			return event
		}
	}
	t.Fatalf("response.completed event not found in %s", body)
	return gjson.Result{}
}

// qoderAnthropicStreamEventsForTest pairs named fixture frames with decoded
// data-space JSON. Callers supply event-space/data-space serialization;
// malformed selected data fails the test and frames without an event name are
// omitted.
//
// Each blank-line block starts empty event/data state, keeping neighboring frames
// separate. Data lines are decoded individually rather than joined: object
// lines reuse the map and retain keys a later object does not overwrite, while
// JSON null clears it. Supported frame order remains available for tool-block
// lifecycle assertions; callers use the general SSE parser for other layouts.
func qoderAnthropicStreamEventsForTest(t *testing.T, stream string) []qoderAnthropicStreamEventForTest {
	t.Helper()
	// Start independent decoded state for every fixture frame, keeping tool block
	// names and JSON data associated with their own blank-line-delimited event.
	events := make([]qoderAnthropicStreamEventForTest, 0)
	for block := range strings.SplitSeq(stream, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		event := qoderAnthropicStreamEventForTest{}
		for line := range strings.SplitSeq(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				// Tool-block fixtures emit one object per frame. Decode that data line
				// independently so its block name and index remain available to the
				// lifecycle assertions; multiple object lines would reuse this map.
				require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event.Data))
			}
		}
		if event.Event != "" {
			events = append(events, event)
		}
	}
	return events
}

// Write delegates serial body writes until the configured attempt allowance is
// exhausted. Later calls return zero bytes with a fixture error; each
// permitted call increments writes before delegation, even if the embedded
// writer subsequently returns an error.
func (w *qoderFailingHTTPWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

// qoderFixtureValue returns the supplied decoded fixture value as T. A failed
// type assertion aborts the test, so callers use it before inspecting concrete
// map, slice or scalar fixture values.
func qoderFixtureValue[T any](t *testing.T, raw any) T {
	t.Helper()
	value, ok := raw.(T)
	require.True(t, ok, "unexpected decoded fixture type: %T", raw)
	return value
}

// qoderEventResponse encodes typed event fixtures into HTTP 200 wrapped Qoder
// SSE for the production parser. DONE emits an unwrapped sentinel; HasUsage
// takes precedence over event type and preserves explicit zero token
// observations. Unsupported ordinary event types fail the test.
//
// Text and reasoning occupy distinct delta fields, while tool deltas carry
// identity, name and argument text with index included only when explicitly
// observed. Optional prompt/completion detail pointers retain their
// corresponding usage buckets. Wrapping the JSON through the shared fixture
// encoder exercises the same split representation that the production reader
// must recover.
func qoderEventResponse(t *testing.T, events []qoder.SSEEvent) *http.Response {
	t.Helper()
	// Encode fixture meaning into wrapped wire JSON before production parsing;
	// usage presence, optional indexes and distinct reasoning fields must survive
	// that representation.
	var body strings.Builder
	for _, event := range events {
		if event.IsDone {
			_, _ = body.WriteString("data: [DONE]\n\n")
			continue
		}
		inner := map[string]any{}
		if event.HasUsage {
			usage := map[string]any{"prompt_tokens": event.PromptTokens, "completion_tokens": event.CompletionTokens, "total_tokens": event.TotalTokens}
			if d := event.UsageDetails.PromptTokensDetails; d != nil {
				usage["prompt_tokens_details"] = map[string]any{"cached_tokens": d.CachedTokens, "cacheable_tokens": d.CacheableTokens}
			}
			if d := event.UsageDetails.CompletionTokensDetails; d != nil {
				usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": d.ReasoningTokens}
			}
			inner["usage"] = usage
		} else {
			delta := map[string]any{}
			switch event.Type {
			case "text_delta":
				delta["content"] = event.Text
			case "reasoning_delta":
				delta["reasoning_content"] = event.Text
			case "tool_call_delta":
				tool := map[string]any{"id": event.ToolCallID, "type": event.ToolType, "function": map[string]any{"name": event.ToolName, "arguments": event.Arguments}}
				if event.HasToolCallIndex {
					tool["index"] = event.ToolCallIndex
				}
				delta["tool_calls"] = []any{tool}
			default:
				t.Fatalf("unsupported event fixture %q", event.Type)
			}
			inner["choices"] = []any{map[string]any{"delta": delta}}
		}
		_, _ = body.WriteString(qoderWrappedSSELineForTest(t, inner))
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body.String()))}
}

// writeQoderOpenAIEvents sends typed fixtures through production Chat parsing
// and conversion using the test context. Client usage is always enabled and
// supplied tool mappers are appended in order; the converter owns the fixture
// response body.
func writeQoderOpenAIEvents(t *testing.T, c *upstreamcore.OutputContext, model string, events []qoder.SSEEvent, mappers ...qoder.QoderToolNameMapper) error {
	t.Helper()
	options := []qoder.QoderOpenAIStreamResponseOption{qoder.QoderOpenAIStreamIncludeUsage(true)}
	for _, mapper := range mappers {
		options = append(options, qoder.QoderOpenAIStreamToolNameMapper(mapper))
	}
	_, err := qoder.WriteQoderOpenAIStreamResponse(t.Context(), c, model, qoderEventResponse(t, events), options...)
	return err
}

// writeQoderAnthropicEvents sends typed fixtures through production Messages
// parsing and conversion using the test context. Supplied tool-name mappers
// are appended in order, and the converter owns closing the constructed
// response body.
func writeQoderAnthropicEvents(t *testing.T, c *upstreamcore.OutputContext, model string, events []qoder.SSEEvent, mappers ...qoder.QoderToolNameMapper) error {
	t.Helper()
	var options []qoder.QoderAnthropicStreamResponseOption
	for _, mapper := range mappers {
		options = append(options, qoder.QoderAnthropicStreamToolNameMapper(mapper))
	}
	_, err := qoder.WriteQoderAnthropicStreamResponse(t.Context(), c, model, qoderEventResponse(t, events), options...)
	return err
}

// TestQoderThinkingFlowsThroughAllEndpoints varies CN/global models and
// reasoning input across Chat, Responses and Messages. Upstream thinking
// switches and permitted effort copies must agree with each route’s configured
// normalization.
func TestQoderThinkingFlowsThroughAllEndpoints(t *testing.T) {
	tests := []struct {
		name        string     // protocol or mapping case label
		site        qoder.Site // configured Qoder site identity
		endpoint    string     // fixture client protocol selector
		body        string     // encoded client request fixture
		wantEnabled bool       // expected upstream reasoning switch
		// wantEffortPath selects whether all four upstream effort copies exist.
		// When true, each equals wantEffort; when false, the helper checks absence
		// and ignores the empty expected value.
		wantEffort     string // expected normalized reasoning effort
		wantEffortPath bool   // expected explicit effort fields
	}{
		{
			name:           "cn deepseek chat completions",
			site:           qoder.SiteCN,
			endpoint:       "chat",
			body:           `{"model":"deepseek-v4-pro","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "cn deepseek responses",
			site:           qoder.SiteCN,
			endpoint:       "responses",
			body:           `{"model":"deepseek-v4-pro","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "cn deepseek anthropic messages",
			site:           qoder.SiteCN,
			endpoint:       "messages",
			body:           `{"model":"deepseek-v4-pro","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek chat completions",
			site:           qoder.SiteGlobal,
			endpoint:       "chat",
			body:           `{"model":"deepseek-v4-pro","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek responses",
			site:           qoder.SiteGlobal,
			endpoint:       "responses",
			body:           `{"model":"deepseek-v4-pro","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek anthropic budget",
			site:           qoder.SiteGlobal,
			endpoint:       "messages",
			body:           `{"model":"deepseek-v4-pro","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:           "global qwen 38 chat completions public alias",
			site:           qoder.SiteGlobal,
			endpoint:       "chat",
			body:           `{"model":"qwen3.8-max","reasoning_effort":"low","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "low",
			wantEffortPath: true,
		},
		{
			name:           "global qwen 38 responses raw route",
			site:           qoder.SiteGlobal,
			endpoint:       "responses",
			body:           `{"model":"qmodel_38max","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "xhigh",
			wantEffortPath: true,
		},
		{
			name:           "global qwen 38 anthropic messages",
			site:           qoder.SiteGlobal,
			endpoint:       "messages",
			body:           `{"model":"qwen3.8-max","max_tokens":1024,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "medium",
			wantEffortPath: true,
		},
		{
			name:           "cn qwen 38 chat completions raw route",
			site:           qoder.SiteCN,
			endpoint:       "chat",
			body:           `{"model":"qmodel_38max","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "medium",
			wantEffortPath: true,
		},
		{
			name:           "cn qwen 38 responses public alias",
			site:           qoder.SiteCN,
			endpoint:       "responses",
			body:           `{"model":"qwen3.8-max","reasoning":{"effort":"max"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "xhigh",
			wantEffortPath: true,
		},
		{
			name:           "cn qwen 38 anthropic messages",
			site:           qoder.SiteCN,
			endpoint:       "messages",
			body:           `{"model":"qwen3.8-max","max_tokens":1024,"thinking":{"budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "xhigh",
			wantEffortPath: true,
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			credentials := map[string]any{"site": string(tt.site)}
			provider := &providercore.Record{
				ID:          int64(920 + index),
				Name:        "qoder-" + string(tt.site),
				Platform:    capability.PlatformQoder,
				Type:        capability.ProviderTypeCosy,
				Credentials: credentials,
			}
			gatewaytestkit.SetQoderCatalog(provider)
			client := &gatewaytestkit.QoderClient{
				Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
					"data: {\"body\":\"[DONE]\"}\n\n",
			}
			service := gatewaytestkit.NewQoderFixture(provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{}),
				client, nil)

			service.Tokens.Core.Sessions = map[int64]providercore.QoderSessionCacheEntry[*qoder.SessionContext]{
				provider.ID: {
					CredentialsHash: providercore.QoderCredentialsHash(provider.Credentials),
					Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
				},
			}

			var err error
			switch tt.endpoint {
			case "chat":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, provider, []byte(tt.body), protocolcore.ProtocolOpenAIChatCompletions)
			case "responses":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, provider, []byte(tt.body), protocolcore.ProtocolOpenAIResponses)
			case "messages":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, provider, []byte(tt.body), protocolcore.ProtocolAnthropicMessages)
			default:
				t.Fatalf("unexpected endpoint %q", tt.endpoint)
			}
			require.NoError(t, err)
			payload := qoderLastUpstreamPayloadForTest(t, client)
			assertQoderThinkingPayload(t, payload, tt.wantEnabled, tt.wantEffort, tt.wantEffortPath)
		})
	}
}

// TestQoderThinkingUsesProviderMappedRouteKey maps a custom client alias to a
// reasoning-capable provider route. Upstream model and thinking switches must
// follow that mapped route while unsupported effort fields stay absent.
func TestQoderThinkingUsesProviderMappedRouteKey(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	provider := &providercore.Record{
		ID:       901,
		Name:     "qoder-global",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site": "global",
			"model_mapping": map[string]any{
				"custom-qwen": "qmodel_38max",
			},
		},
	}
	gatewaytestkit.SetQoderCatalog(provider)
	client := &gatewaytestkit.QoderClient{
		Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	service := gatewaytestkit.NewQoderFixture(provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{}),
		client, nil)

	service.Tokens.Core.Sessions = map[int64]providercore.QoderSessionCacheEntry[*qoder.SessionContext]{
		provider.ID: {
			CredentialsHash: providercore.QoderCredentialsHash(provider.Credentials),
			Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{
		"model":"custom-qwen",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`)

	result, err := ForwardQoderAttempt(context.Background(), c, service.Runtime, provider, body, protocolcore.ProtocolOpenAIChatCompletions)
	require.NoError(t, err)
	require.Equal(t, "qmodel_38max", result.UpstreamModel)
	payload := qoderLastUpstreamPayloadForTest(t, client)
	assertQoderThinkingPayload(t, payload, true, "medium", true)
}

// assertQoderThinkingPayload checks both reasoning switches and all four
// effort locations in a decoded payload. Callers choose whether effort is
// present; absent effort requires every copy to be missing, while present
// values must agree.
func assertQoderThinkingPayload(t *testing.T, payload map[string]any, enabled bool, effort string, hasEffort bool) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(raw, "model_config.is_reasoning").Exists())
	require.Equal(t, enabled, gjson.GetBytes(raw, "model_config.is_reasoning").Bool())
	require.True(t, gjson.GetBytes(raw, "chat_context.extra.modelConfig.is_reasoning").Exists())
	require.Equal(t, enabled, gjson.GetBytes(raw, "chat_context.extra.modelConfig.is_reasoning").Bool())

	paths := []string{
		"parameters.reasoning_effort",
		"model_config.reasoning_effort",
		"chat_context.extra.modelConfig.reasoning_effort",
		"chat_context.extra.ideModelConfigOverride.reasoning_effort",
	}
	for _, path := range paths {
		if hasEffort {
			require.Equal(t, effort, gjson.GetBytes(raw, path).String(), path)
			continue
		}
		require.False(t, gjson.GetBytes(raw, path).Exists(), path)
	}
}

// TestMixedGroupQoderValidationRetainsClientError sends a stateful request through
// the mixed-platform native dispatcher. The error must be presented once as a
// local 400 and stay recognizable to scheduler feedback without invoking transport.
func TestMixedGroupQoderValidationRetainsClientError(t *testing.T) {
	row, fixture, client := gatewaytestkit.NewDefaultQoderFixture()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	executor := UnifiedTextExecutor{Qoder: fixture.Runtime}
	result, err := executor.native(t.Context(), c, gatewayprovider.NewExecutionProvider(row), []byte(`{"model":"ultimate","input":"test","store":true}`), protocolcore.ProtocolOpenAIResponses)
	require.Nil(t, result)
	require.True(t, routing.IsModelAttributeError(err))
	require.True(t, qoder.IsRequestError(err))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unsupported_response_state")
	require.True(t, IsResponseCommitted(c))
	require.Zero(t, client.BodyCount())
}
