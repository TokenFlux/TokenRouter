package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// bridgeDiagnosticRoundTrip replaces the wire response while preserving the production
// upstream pool's dispatch observation and response ownership behavior.
type bridgeDiagnosticRoundTrip func(*http.Request) (*http.Response, error)

// bridgeDiagnosticTransport serves bridge response fixtures through the real pool.
type bridgeDiagnosticTransport struct {
	pool      *httpclient.UpstreamPool // production HTTP pool
	responses []*http.Response         // ordered fixture responses
}

// bridgeRetryDelayBody holds one rejected response read before retry can start.
// The independent delay distinguishes the rejected exchange from accepted timing.
type bridgeRetryDelayBody struct {
	reader   io.Reader // rejected response body
	delayed  bool      // first delay consumed
	released time.Time // injected wait completion
}

// A WS continuation executed over HTTP removes previous_response_id after
// ingress reconstructs its retained input. Function-only upstream requests
// lower custom and namespace tools, then restore client tool forms in output
// and retain declaration mappings for later omitted-tool turns.
//
// A terminal event can provide usage and a failed result even when the adapter
// returns no ordinary error. Bare-error synthesis, pre-output failover and
// already delivered partial output therefore have different asserted results.
// Current-turn replay after a later 429 carries matching call context to a
// replacement provider while the client keeps its earlier response. Buffered
// worker outcomes let network fixtures inspect completion and continuation
// separately from reading client events.

// TestGrokWSModelUsesCanonicalID resolves an explicitly named Grok model and
// an omitted model through bridge helpers. The named ID remains grok; omission
// selects the upstream default while leaving the billing model empty.
func TestGrokWSModelUsesCanonicalID(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Credentials: map[string]any{}}}
	require.Equal(t, "grok", resolveGrokWSUpstreamModel(provider, []byte(`{"model":"grok"}`), "grok"))
	require.Equal(t, "grok", resolveGrokWSUpstreamModel(provider, []byte(`{"model":"grok"}`), ""))

	billingModel, upstreamModel := resolveGrokWSModels(provider, []byte(`{"model":"grok"}`), "")
	require.Equal(t, "grok", billingModel)
	require.Equal(t, "grok", upstreamModel)

	billingModel, upstreamModel = resolveGrokWSModels(provider, []byte(`{"input":"hello"}`), "")
	require.Empty(t, billingModel)
	require.Equal(t, xai.DefaultResponsesModel, upstreamModel)
}

// TestGrokContentPolicySSEErrorDoesNotMutateOrFailover supplies a
// new_sensitive error inside Grok HTTP 200 SSE. The client receives that code
// with a nonnil result and ordinary error, while failover, provider blocking
// and repository mutation counters remain absent.
func TestGrokContentPolicySSEErrorDoesNotMutateOrFailover(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"error\",\"error\":{\"code\":\"new_sensitive\",\"message\":\"text is sensitive\"}}\n\n",
		)),
	}}
	svc := newWSFixture(wsFixtureInputs{providers: repo, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 4721, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"grok-4.5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "grok-4.5", RoutingModel: "grok-4.5", GrokCacheIdentity: "cache-id", Turn: 1, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.Error(t, err)
	require.NotNil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Len(t, writes, 1)
	require.Contains(t, string(writes[0]), "new_sensitive")
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.rateLimitedCalls)
	require.Zero(t, repo.updateCalls)
	require.False(t, wsFixtureProviderBlocked(svc, provider))
}

// wsFixtureRuntimeOptions returns the fixture's WS settings pointer or nil
// when options are absent. The pointer aliases the original WS field, so
// subsequent fixture writes are visible through the returned configuration.
func wsFixtureRuntimeOptions(options *wsFixtureOptions) *OpenAIWSOptions {
	if options == nil {
		return nil
	}
	return &options.WS
}

// TestResolveOpenAIWSClientFirstMessageTimeout compares absent settings,
// zero-valued settings and an explicit 120-second override. The first two
// select thirty seconds and the positive override becomes the returned
// duration.
func TestResolveOpenAIWSClientFirstMessageTimeout(t *testing.T) {
	defaultTimeout := time.Duration(30) * time.Second
	require.Equal(t, defaultTimeout, ResolveOpenAIWSClientFirstMessageTimeout(nil))

	options := &wsFixtureOptions{}
	require.Equal(t, defaultTimeout, ResolveOpenAIWSClientFirstMessageTimeout(wsFixtureRuntimeOptions(options)))

	options.WS.ClientFirstMessageTimeoutSeconds = 120
	require.Equal(t, 120*time.Second, ResolveOpenAIWSClientFirstMessageTimeout(wsFixtureRuntimeOptions(options)))
}

// TestPrepareOpenAIWSHTTPBridgeBodyStripsWSFields converts a WS request
// carrying transport controls and a large integer. Conversion removes type,
// generate and continuation, forces streaming, retains model/input and exact
// integer bytes, and rejects trailing JSON.
func TestPrepareOpenAIWSHTTPBridgeBodyStripsWSFields(t *testing.T) {
	body, err := prepareOpenAIWSHTTPBridgeBody(nil, []byte(`{"type":"response.create","generate":true,"model":"gpt-5","stream":false,"previous_response_id":"resp_prev","input":"hi","sequence":900719925474099312345}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "type").Exists())
	require.False(t, gjson.GetBytes(body, "generate").Exists())
	require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
	require.Equal(t, "gpt-5", gjson.GetBytes(body, "model").String())
	require.True(t, gjson.GetBytes(body, "stream").Bool())
	require.Equal(t, "hi", gjson.GetBytes(body, "input").String())
	require.Equal(t, "900719925474099312345", gjson.GetBytes(body, "sequence").Raw)
	_, err = prepareOpenAIWSHTTPBridgeBody(nil, []byte(`{"type":"response.create"}{"trailing":true}`))
	require.Error(t, err)
}

// TestPrepareOpenAIWSHTTPBridgeBodyPreservesUnknownModelReasoning supplies
// explicit none effort for an unknown model to custom and official API Key
// providers. Both bridge-body conversions retain none, limiting this test to
// conversion before later model preparation.
func TestPrepareOpenAIWSHTTPBridgeBodyPreservesUnknownModelReasoning(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"company-coding-model","reasoning":{"effort":"none"},"input":"hi"}`)
	compatible := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://compat.example/v1",
	}}}

	body, err := prepareOpenAIWSHTTPBridgeBody(compatible, payload)
	require.NoError(t, err)
	require.Equal(t, "none", gjson.GetBytes(body, "reasoning.effort").String())

	officialBody, err := prepareOpenAIWSHTTPBridgeBody(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}, payload)
	require.NoError(t, err)
	require.Equal(t, "none", gjson.GetBytes(officialBody, "reasoning.effort").String())
}

// TestProxyOpenAIWSHTTPBridgeTurn_KeepsOutboundAndObservedServiceTiersSeparate
// sends priority while the terminal declares default. The upstream request and
// result ServiceTier retain priority, while UpstreamResponseServiceTier and
// UpstreamResponseModel retain the observed response values.
func TestProxyOpenAIWSHTTPBridgeTurn_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {
	// The request sends priority while the upstream response declares default.
	// Keep these values separate so billing can resolve the effective tier from both sources.
	sse := `data: {"type":"response.completed","response":{"id":"resp_tier","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" + ``
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5881, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","stream":true,"service_tier":"priority","input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5.5", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String())
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	require.Equal(t, "gpt-5.5", result.UpstreamResponseModel)
}

// TestProxyOpenAIWSHTTPBridgeTurn_NormalizesFastWithoutLosingObservedDefault
// sends the fast alias and receives an upstream default declaration. The
// result records canonical priority for the outbound tier and separately keeps
// default and the observed model.
func TestProxyOpenAIWSHTTPBridgeTurn_NormalizesFastWithoutLosingObservedDefault(t *testing.T) {
	// The client alias fast is canonicalized to priority while the local observer
	// independently captures the upstream default declaration.
	sse := `data: {"type":"response.completed","response":{"id":"resp_tier2","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" + ``
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5882, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","stream":true,"service_tier":"fast","input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5.5", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	require.Equal(t, "gpt-5.5", result.UpstreamResponseModel)
}

// TestProxyOpenAIWSHTTPBridgeTurnAPIKeyAdaptsClientTools sends a custom tool
// declaration and matching historical call/output through an API Key bridge.
// Upstream bytes use function tools. The output-item event restores custom
// input and omits arguments; completed output and saved replay retain the same
// client call type and input.
func TestProxyOpenAIWSHTTPBridgeTurnAPIKeyAdaptsClientTools(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","status":"in_progress"}}`,
		``,
		`data: {"type":"response.function_call_arguments.done","sequence_number":1,"output_index":0,"item_id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}"}`,
		``,
		`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}}`,
		``,
		`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_tools","status":"completed","output":[{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5659, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{
		"type":"response.create","model":"gpt-5","stream":true,
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}],
		"input":[
			{"type":"custom_tool_call","id":"previous_item","call_id":"previous_call","name":"exec","input":"echo ready"},
			{"type":"custom_tool_call_output","call_id":"previous_call","output":"ready"}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(upstream.lastBody, "input.0.type").String())
	require.JSONEq(t, `{"input":"echo ready"}`, gjson.GetBytes(upstream.lastBody, "input.0.arguments").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.input").Exists())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.1.type").String())

	var outputDone, completed []byte
	for _, event := range events {
		switch gjson.GetBytes(event, "type").String() {
		case "response.output_item.done":
			outputDone = event
		case "response.completed":
			completed = event
		}
	}
	require.NotEmpty(t, outputDone)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(outputDone, "item.type").String())
	require.Equal(t, "pwd", gjson.GetBytes(outputDone, "item.input").String())
	require.False(t, gjson.GetBytes(outputDone, "item.arguments").Exists())
	require.NotEmpty(t, completed)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(completed, "response.output.0.type").String())
	require.Equal(t, "pwd", gjson.GetBytes(completed, "response.output.0.input").String())
	replayInput, replayExists := result.WSReplayInput()
	require.True(t, replayExists)
	require.Len(t, replayInput, 1)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(replayInput[0], "type").String())
	require.Equal(t, "pwd", gjson.GetBytes(replayInput[0], "input").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnAPIKeyKeepsDiscoveredFunctionNamespace sends
// a discovered plain function through an API-key HTTP bridge. Upstream history
// uses its bare declaration name, while client completion and replay recover
// the original namespace and call correlation.
func TestProxyOpenAIWSHTTPBridgeTurnAPIKeyKeepsDiscoveredFunctionNamespace(t *testing.T) {
	sse := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_js\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_2\",\"name\":\"js\",\"arguments\":\"{}\",\"status\":\"completed\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5659, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","tools":[{"type":"tool_search"}],"input":[{"type":"tool_search_call","call_id":"search_1","arguments":{"query":"js"}},{"type":"tool_search_output","call_id":"search_1","tools":[{"type":"function","name":"js","parameters":{"type":"object"}}]},{"type":"function_call","call_id":"call_1","name":"js","namespace":"js","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5.6-sol", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "js", gjson.GetBytes(upstream.lastBody, "input.2.name").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.2.namespace").Exists())
	require.Len(t, events, 1)
	require.Equal(t, "js", gjson.GetBytes(events[0], "response.output.0.namespace").String())
	require.Equal(t, "call_2", gjson.GetBytes(events[0], "response.output.0.call_id").String())
	replay, exists := result.WSReplayInput()
	require.True(t, exists)
	require.Len(t, replay, 1)
	require.Equal(t, "js", gjson.GetBytes(replay[0], "namespace").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnAPIKeyRestoresClientToolsInResponseDone ends
// a lowered custom-tool stream with response.done. Four returned events
// include a resequenced terminal with restored custom call input and no
// arguments; retained tool replay also uses custom_tool_call.
func TestProxyOpenAIWSHTTPBridgeTurnAPIKeyRestoresClientToolsInResponseDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","status":"in_progress"}}`,
		``,
		`data: {"type":"response.function_call_arguments.done","sequence_number":1,"output_index":0,"item_id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}"}`,
		``,
		`data: {"type":"response.done","sequence_number":2,"response":{"id":"resp_tools","status":"completed","output":[{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5764, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{
		"type":"response.create","model":"gpt-5","stream":true,
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}],
		"input":"run pwd"
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, events, 4)
	terminal := events[len(events)-1]
	require.Equal(t, "response.done", gjson.GetBytes(terminal, "type").String())
	require.Equal(t, int64(3), gjson.GetBytes(terminal, "sequence_number").Int())
	require.Equal(t, "custom_tool_call", gjson.GetBytes(terminal, "response.output.0.type").String())
	require.Equal(t, "pwd", gjson.GetBytes(terminal, "response.output.0.input").String())
	require.False(t, gjson.GetBytes(terminal, "response.output.0.arguments").Exists())
	replayInput, replayExists := result.WSReplayInput()
	require.True(t, replayExists)
	require.Len(t, replayInput, 1)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(replayInput[0], "type").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnGrokPromotesDiscoveryAndRestoresNamespaceSSE
// supplies tool_search discoveries containing a namespace function. Grok
// receives a flattened declaration and sanitized output, while stored mapping
// and emitted added, done and terminal items restore namespace and function
// names.
func TestProxyOpenAIWSHTTPBridgeTurnGrokPromotesDiscoveryAndRestoresNamespaceSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"item_spawn","call_id":"call_spawn","name":"multi_agent_v1__spawn_agent","status":"in_progress"}}`,
		"",
		`data: {"type":"response.function_call_arguments.done","sequence_number":1,"output_index":0,"item_id":"item_spawn","call_id":"call_spawn","name":"multi_agent_v1__spawn_agent","arguments":"{\"message\":\"work\"}"}`,
		"",
		`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"function_call","id":"item_spawn","call_id":"call_spawn","name":"multi_agent_v1__spawn_agent","arguments":"{\"message\":\"work\"}","status":"completed"}}`,
		"",
		`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_spawn","status":"completed","output":[{"type":"function_call","id":"item_spawn","call_id":"call_spawn","name":"multi_agent_v1__spawn_agent","arguments":"{\"message\":\"work\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5765, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL},
		},
	}
	payload := []byte(`{
		"type":"response.create","model":"grok-4.5","stream":true,
		"tools":[{"type":"tool_search"}],
		"input":[
			{"type":"tool_search_call","call_id":"call_search","arguments":{"query":"subagent"},"status":"completed"},
			{"type":"tool_search_output","call_id":"call_search","execution":"client","status":"completed","tools":[
				{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
			]}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "grok-4.5", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "multi_agent_v1__spawn_agent", gjson.GetBytes(upstream.lastBody, "tools.1.name").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.1.type").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.1.tools").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.1.status").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.1.execution").Exists())
	state, ok := gatewayhttp.OpenAIWSHTTPBridgeToolStateFromContext(c)
	require.True(t, ok)
	require.Equal(t, "multi_agent_v1", state.ClientMapping.NamespaceTools["multi_agent_v1__spawn_agent"].Namespace)
	require.Len(t, events, 4)
	require.Equal(t, "spawn_agent", gjson.GetBytes(events[0], "item.name").String())
	require.Equal(t, "multi_agent_v1", gjson.GetBytes(events[0], "item.namespace").String())
	require.Equal(t, "spawn_agent", gjson.GetBytes(events[2], "item.name").String())
	require.Equal(t, "multi_agent_v1", gjson.GetBytes(events[2], "item.namespace").String())
	require.Equal(t, "spawn_agent", gjson.GetBytes(events[3], "response.output.0.name").String())
	require.Equal(t, "multi_agent_v1", gjson.GetBytes(events[3], "response.output.0.namespace").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnGrokInheritsToolSearchAndPromotesFollowupDiscovery
// first declares tool_search then omits tools on a discovery turn. The second
// request inherits search, promotes the namespace function, retains cache
// identity and restores that function in terminal output.
func TestProxyOpenAIWSHTTPBridgeTurnGrokInheritsToolSearchAndPromotesFollowupDiscovery(t *testing.T) {
	firstSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_first\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	secondSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_second\",\"output\":[{\"type\":\"function_call\",\"id\":\"item_spawn\",\"call_id\":\"call_spawn\",\"name\":\"multi_agent_v1__spawn_agent\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(firstSSE))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(secondSSE))},
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5766, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL, "subscription_tier": "free"},
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	first := []byte(`{"type":"response.create","model":"grok-4.5","stream":true,"tools":[{"type":"tool_search"}],"input":"discover tools"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: first, PayloadBytes: len(first), OriginalModel: "grok-4.5", GrokCacheIdentity: "grok-ws-cache", Turn: 1, WriteClient: func([]byte) error { return nil }})
	require.NoError(t, err)

	second := []byte(`{
		"type":"response.create","model":"grok-4.5","stream":true,
		"input":[{"type":"tool_search_output","call_id":"call_search","status":"completed","tools":[
			{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
		]}]
	}`)
	var events [][]byte
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: second, PayloadBytes: len(second), OriginalModel: "grok-4.5", GrokCacheIdentity: "grok-ws-cache", Turn: 2, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "tool_search", gjson.GetBytes(upstream.bodies[1], "tools.0.name").String())
	require.Equal(t, "multi_agent_v1__spawn_agent", gjson.GetBytes(upstream.bodies[1], "tools.1.name").String())
	require.NotEqual(t, "none", gjson.GetBytes(upstream.bodies[1], "tool_choice").String())
	require.Equal(t, "grok-ws-cache", gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.Len(t, events, 1)
	require.Equal(t, "spawn_agent", gjson.GetBytes(events[0], "response.output.0.name").String())
	require.Equal(t, "multi_agent_v1", gjson.GetBytes(events[0], "response.output.0.namespace").String())
}

// TestOpenAIWSHTTPBridgeAPIKeyReusesClientToolMappingWhenFollowupOmitsTools
// declares a custom tool before a followup with its result and undeclared
// namespace history. Both HTTP bodies contain the callable custom function,
// while historical replay names stay out of saved mappings and declarations.
func TestOpenAIWSHTTPBridgeAPIKeyReusesClientToolMappingWhenFollowupOmitsTools(t *testing.T) {
	response := func(id string) *http.Response {
		body := `data: {"type":"response.completed","response":{"id":"` + id + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n" // scripted successful terminal SSE bytes
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		response("resp_custom_first"),
		response("resp_custom_second"),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5822, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	write := func([]byte) error { return nil }

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.6","stream":true,"tools":[{"type":"custom","name":"exec"}],"input":"run pwd"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: firstPayload, PayloadBytes: len(firstPayload), OriginalModel: "gpt-5.6", Turn: 1, WriteClient: write})
	require.NoError(t, err)

	secondPayload := []byte(`{"type":"response.create","model":"gpt-5.6","stream":true,"previous_response_id":"resp_custom_first","input":[{"type":"custom_tool_call_output","id":"ctco_client_output_1","call_id":"call_custom_1","output":"ok"},{"type":"function_call","namespace":"mcp__cua_proxy","name":"js","call_id":"old_call","arguments":"{}"},{"type":"function_call_output","call_id":"old_call","output":"old result"}]}`)
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test-token", Payload: secondPayload, PayloadBytes: len(secondPayload), OriginalModel: "gpt-5.6", Turn: 2, WriteClient: write})
	require.NoError(t, err)

	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "function", gjson.GetBytes(upstream.bodies[0], "tools.0.type").String())
	require.Equal(t, "exec", gjson.GetBytes(upstream.bodies[0], "tools.0.name").String())
	require.Equal(t, "function", gjson.GetBytes(upstream.bodies[1], "tools.0.type").String())
	require.Equal(t, "exec", gjson.GetBytes(upstream.bodies[1], "tools.0.name").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.id").Exists())
	require.Equal(t, "mcp__cua_proxy__js", gjson.GetBytes(upstream.bodies[1], "input.1.name").String())
	require.Equal(t, "old_call", gjson.GetBytes(upstream.bodies[1], "input.2.call_id").String())
	require.Equal(t, int64(1), gjson.GetBytes(upstream.bodies[1], "tools.#").Int())
	state, present := gatewayhttp.OpenAIWSHTTPBridgeToolStateFromContext(c)
	require.True(t, present)
	require.NotContains(t, state.ClientMapping.NamespaceTools, "mcp__cua_proxy__js")
}

// TestOpenAIWSHTTPBridgeFullCustomToolHistoryWithoutPreviousResponseIDDoesNotReplay
// sends anchored continuation followed by repeated unanchored complete tool
// history. The anchored body keeps its surviving prior input; each complete
// history contains its three supplied items once, with matching call and
// output IDs.
func TestOpenAIWSHTTPBridgeFullCustomToolHistoryWithoutPreviousResponseIDDoesNotReplay(t *testing.T) {
	completed := func(responseID string, output string) string {
		return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + responseID + "\",\"model\":\"gpt-5.1\",\"output\":" + output + ",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_1", `[{"type":"custom_tool_call","id":"item_1","call_id":"call_1","name":"exec","input":"pwd"}]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_2", `[]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_3", `[]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_4", `[]`)))},
	}}
	options := &wsFixtureOptions{}
	options.Request.URLPolicy.Enabled = false
	options.Request.URLPolicy.AllowInsecureHTTP = true

	options.WS.HTTPBridgeThresholdBytes = 1
	options.WS.ReadTimeoutSeconds = 3
	options.WS.WriteTimeoutSeconds = 3

	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9002, Name: "oauth-full-context", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"responses_websockets_v2_enabled": true},
			Concurrency: 1, Status: billing.StatusActive, Schedulable: true,
		},
	}

	// Buffer session return while four client responses are consumed, then
	// inspect completed HTTP bodies for repeated history under that same execution.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, provider, "test-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeAndRead := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
		cancelWrite()
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	}

	writeAndRead(`{"type":"response.create","model":"gpt-5.1","input":"run pwd"}`)
	writeAndRead(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_1","input":[{"role":"user","content":"continue without tool output"}]}`)
	fullContext := `{"type":"response.create","model":"gpt-5.1","input":[{"type":"custom_tool_call","id":"item_1","call_id":"call_1","name":"exec","input":"pwd"},{"type":"custom_tool_call_output","call_id":"call_1","output":"/tmp"},{"role":"user","content":"continue"}]}`
	writeAndRead(fullContext)
	writeAndRead(fullContext)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket bridge proxy to finish")
	}

	require.Len(t, upstream.bodies, 4)
	orphanInput := gjson.GetBytes(upstream.bodies[1], "input").Array()
	require.Len(t, orphanInput, 2)
	require.Equal(t, "run pwd", orphanInput[0].String())
	require.Equal(t, "user", orphanInput[1].Get("role").String())
	for _, body := range upstream.bodies[2:] {
		input := gjson.GetBytes(body, "input").Array()
		require.Len(t, input, 3)
		require.Equal(t, "custom_tool_call", input[0].Get("type").String())
		require.Equal(t, "call_1", input[0].Get("call_id").String())
		require.Equal(t, "custom_tool_call_output", input[1].Get("type").String())
		require.Equal(t, "call_1", input[1].Get("call_id").String())
	}
}

// TestOpenAIWSHTTPBridgeObjectToolOutputWithoutPreviousResponseIDReplaysMatchingCall
// receives a custom call then supplies an unanchored output object. The second
// HTTP input expands to three entries containing the prior call and the output
// with the same call_id.
func TestOpenAIWSHTTPBridgeObjectToolOutputWithoutPreviousResponseIDReplaysMatchingCall(t *testing.T) {
	completed := func(responseID string, output string) string {
		return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + responseID + "\",\"model\":\"gpt-5.1\",\"output\":" + output + ",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_1", `[{"type":"custom_tool_call","id":"item_1","call_id":"call_1","name":"exec","input":"pwd"}]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_2", `[]`)))},
	}}
	options := &wsFixtureOptions{}
	options.Request.URLPolicy.Enabled = false
	options.Request.URLPolicy.AllowInsecureHTTP = true

	options.WS.HTTPBridgeThresholdBytes = 1
	options.WS.ReadTimeoutSeconds = 3
	options.WS.WriteTimeoutSeconds = 3

	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9003, Name: "oauth-output-only", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"responses_websockets_v2_enabled": true},
			Concurrency: 1, Status: billing.StatusActive, Schedulable: true,
		},
	}

	// Retain the server outcome until both client terminals arrive, making the
	// second recorded input available after matching-call replay has finished.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, provider, "test-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeAndRead := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
		cancelWrite()
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	}

	writeAndRead(`{"type":"response.create","model":"gpt-5.1","input":"run pwd"}`)
	writeAndRead(`{"type":"response.create","model":"gpt-5.1","input":{"type":"custom_tool_call_output","call_id":"call_1","output":"/tmp"}}`)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket bridge proxy to finish")
	}

	require.Len(t, upstream.bodies, 2)
	secondInput := gjson.GetBytes(upstream.bodies[1], "input").Array()
	require.Len(t, secondInput, 3)
	require.Equal(t, "custom_tool_call", secondInput[1].Get("type").String())
	require.Equal(t, "call_1", secondInput[1].Get("call_id").String())
	require.Equal(t, "custom_tool_call_output", secondInput[2].Get("type").String())
	require.Equal(t, "call_1", secondInput[2].Get("call_id").String())
}

// TestOpenAIWSHTTPBridgeDecisionKeepsSmallFramesOnWS compares sizes below and
// at the threshold plus a supplied continuation. Only the unanchored equality
// case bridges for ordinary providers; Grok selects HTTP even for a small
// anchored payload.
func TestOpenAIWSHTTPBridgeDecisionKeepsSmallFramesOnWS(t *testing.T) {
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{WS: OpenAIWSOptions{HTTPBridgeThresholdBytes: 100}}})

	require.False(t, svc.shouldBridgeOpenAIWSHTTP(nil, 99, ""))
	require.True(t, svc.shouldBridgeOpenAIWSHTTP(nil, 100, ""))
	require.False(t, svc.shouldBridgeOpenAIWSHTTP(nil, 1000, "resp_existing"))

	require.True(t, svc.shouldBridgeOpenAIWSHTTP(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok}}, 1, "resp_existing"))
}

// TestOpenAIWSPassthroughFirstMessageBridgeDecision compares size, event type,
// duplicate keys and malformed JSON before dedicated dialing. Large valid
// unanchored creates bridge, including absent/null/blank type; small,
// anchored, duplicate, nonstring and other-event inputs remain outside that
// choice.
func TestOpenAIWSPassthroughFirstMessageBridgeDecision(t *testing.T) {
	// Build an exact-size frame to exercise the inclusive bridge threshold.
	const threshold = 100
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{WS: OpenAIWSOptions{HTTPBridgeThresholdBytes: threshold}}})
	exactThresholdPayload := `{"type":"response.create","input":"` +
		strings.Repeat("x", threshold-len(`{"type":"response.create","input":""}`)) + `"}`

	tests := []struct {
		name    string // bridge scenario diagnostic label
		payload string // client WebSocket request JSON
		want    bool   // expected bridge selection result
	}{
		{name: "oversized without previous response id bridges", payload: `{"type":"response.create","input":"` + strings.Repeat("x", 100) + `"}`, want: true},
		{name: "omitted type bridges as response create", payload: `{"input":"` + strings.Repeat("x", 100) + `"}`, want: true},
		{name: "blank type bridges as response create", payload: `{"type":"   ","padding":"` + strings.Repeat("x", 100) + `"}`, want: true},
		{name: "exact threshold bridges", payload: exactThresholdPayload, want: true},
		{name: "small stays passthrough", payload: `{"type":"response.create","input":"x"}`},
		{name: "previous response id stays passthrough", payload: `{"type":"response.create","previous_response_id":"resp_previous","input":"` + strings.Repeat("x", 100) + `"}`},
		{name: "duplicate type stays passthrough", payload: `{"type":"response.create","type":"response.create","input":"` + strings.Repeat("x", 100) + `"}`},
		{name: "duplicate previous response id stays passthrough", payload: `{"type":"response.create","previous_response_id":null,"previous_response_id":null,"input":"` + strings.Repeat("x", 100) + `"}`},
		{name: "null type bridges as response create", payload: `{"type":null,"padding":"` + strings.Repeat("x", 100) + `"}`, want: true},
		{name: "non-string type stays passthrough", payload: `{"type":123,"padding":"` + strings.Repeat("x", 100) + `"}`},
		{name: "oversized response cancel stays passthrough", payload: `{"type":"response.cancel","padding":"` + strings.Repeat("x", 100) + `"}`},
		{name: "oversized other event stays passthrough", payload: `{"type":"session.update","padding":"` + strings.Repeat("x", 100) + `"}`},
		{name: "oversized malformed JSON stays passthrough", payload: `{"type":"response.create","padding":"` + strings.Repeat("x", 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, svc.shouldBridgeOpenAIWSPassthroughFirstMessage(nil, []byte(tt.payload)))
		})
	}
}

// TestProxyOpenAIWSHTTPBridgeTurnTransportErrorFailoverSafety injects EOF into
// first and later HTTP exchanges. The first produces a diagnostic 502 failover
// before client writes; the later turn returns an ordinary failure and writes
// one 502 error event.
func TestProxyOpenAIWSHTTPBridgeTurnTransportErrorFailoverSafety(t *testing.T) {
	tests := []struct {
		// turn chooses first or later execution; wantFailover selects typed upstream
		// failure versus ordinary error assertions. Positive wantWrites additionally
		// checks the emitted error event type and HTTP status.
		name         string // bridge scenario diagnostic label
		turn         int    // local bridge execution turn number
		wantFailover bool   // expected upstream failure marker presence
		wantWrites   int    // expected client event write count
	}{
		{name: "first_turn_fails_over_before_downstream_event", turn: 1, wantFailover: true},
		{name: "later_turn_does_not_replay_completed_turns", turn: 2, wantWrites: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{err: io.EOF}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 8,
					Name:        "api-key",
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
				},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: tt.turn, WriteClient: func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			}})

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			if tt.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
				require.JSONEq(t, string([]byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)), string(failoverErr.ResponseBody))
			} else {
				require.Error(t, err)
				require.False(t, errors.As(err, &failoverErr))
			}
			require.Len(t, writes, tt.wantWrites)
			if tt.wantWrites > 0 {
				require.Equal(t, "error", gjson.GetBytes(writes[0], "type").String())
				require.Equal(t, int64(http.StatusBadGateway), gjson.GetBytes(writes[0], "status").Int())
			}
		})
	}
}

// TestProxyOpenAIWSHTTPBridgeTurnHTTPStatusFailoverSafety supplies first-turn
// 401, 429 and 500 responses alongside a later 500. Initial failures retain
// their status in failover with no writes; later 500 produces one client event
// and an ordinary error.
func TestProxyOpenAIWSHTTPBridgeTurnHTTPStatusFailoverSafety(t *testing.T) {
	tests := []struct {
		// turn and status supply one failed exchange. wantFailover selects typed
		// error assertions that retain status; wantWrites records whether that outcome
		// has already produced a client event for this turn.
		name         string // bridge scenario diagnostic label
		turn         int    // local bridge execution turn number
		status       int    // scripted upstream HTTP failure status
		wantFailover bool   // expected upstream failure marker presence
		wantWrites   int    // expected client event write count
	}{
		{name: "first_turn_401", turn: 1, status: http.StatusUnauthorized, wantFailover: true},
		{name: "first_turn_429", turn: 1, status: http.StatusTooManyRequests, wantFailover: true},
		{name: "first_turn_500", turn: 1, status: http.StatusInternalServerError, wantFailover: true},
		{name: "later_turn_500_does_not_replay", turn: 2, status: http.StatusInternalServerError, wantWrites: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: tt.status,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"server_error","message":"temporary upstream failure"}}`)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: tt.turn, WriteClient: func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			}})

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			if tt.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, tt.status, failoverErr.StatusCode)
			} else {
				require.Error(t, err)
				require.False(t, errors.As(err, &failoverErr))
			}
			require.Len(t, writes, tt.wantWrites)
		})
	}
}

// TestProxyOpenAIWSHTTPBridgeTurnRetriesRejectedFieldBeforeClientOutput
// scripts an explicit truncation rejection before a completed stream. Two HTTP
// bodies show truncation present then removed, and the client writer receives
// only the final completion event.
func TestProxyOpenAIWSHTTPBridgeTurnRetriesRejectedFieldBeforeClientOutput(t *testing.T) {
	upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"truncation","message":"Unsupported parameter: truncation"}}`,
			)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_retry\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n",
			)),
		},
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 91, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, upstream.callCount)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "truncation").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "truncation").Exists())
	require.Len(t, writes, 1)
	require.Equal(t, "response.completed", gjson.GetBytes(writes[0], "type").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnSSEErrorFailoverSafety supplies a rate-limit
// error inside HTTP 200 for first and second turns. Both return a 429 upstream
// failure with nil result and no client writes.
func TestProxyOpenAIWSHTTPBridgeTurnSSEErrorFailoverSafety(t *testing.T) {
	for _, turn := range []int{1, 2} {
		t.Run(fmt.Sprintf("turn_%d", turn), func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"limited\"}}\n\n",
				)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 10, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: turn, WriteClient: func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			}})

			var failoverErr *forwardcore.UpstreamFailoverError
			require.Nil(t, result)
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Empty(t, writes)
		})
	}
}

// TestBuildOpenAIWSCurrentTurnRetryPayloadRejectsOrphanToolOutput supplies
// replay containing an output whose call context is missing. Retry
// construction returns no error but reports unsafe and supplies no replacement
// payload.
func TestBuildOpenAIWSCurrentTurnRetryPayloadRejectsOrphanToolOutput(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"mapped-model","previous_response_id":"resp_old"}`)
	fullInput := []json.RawMessage{
		json.RawMessage(`{"type":"function_call_output","call_id":"missing_call","output":"done"}`),
	}

	retryPayload, retrySafe, err := openai.BuildOpenAIWSCurrentTurnRetryPayload(payload, fullInput, true, "gpt-5.6-sol")

	require.NoError(t, err)
	require.False(t, retrySafe)
	require.Nil(t, retryPayload)
}

// TestBuildOpenAIWSCurrentTurnRetryPayloadRebuildsChainWithoutPreviousResponseID
// supplies matching call/output replay for provider replacement. The safe
// result removes the old continuation, restores the client model and retains
// both input items.
func TestBuildOpenAIWSCurrentTurnRetryPayloadRebuildsChainWithoutPreviousResponseID(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"mapped-model","previous_response_id":"resp_old","input":"continue"}`)
	fullInput := []json.RawMessage{
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"inspect","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"done"}`),
	}

	retryPayload, retrySafe, err := openai.BuildOpenAIWSCurrentTurnRetryPayload(payload, fullInput, true, "gpt-5.6-sol")

	require.NoError(t, err)
	require.True(t, retrySafe)
	require.False(t, gjson.GetBytes(retryPayload, "previous_response_id").Exists())
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(retryPayload, "model").String())
	require.Len(t, gjson.GetBytes(retryPayload, "input").Array(), 2)
}

// TestProxyOpenAIWSHTTPBridgeTurnLaterTurn429FailsOverBeforeClientWrite
// supplies OAuth 429 on turn 281 with an old continuation. The adapter returns
// a typed 429 failover and nil result while the client writer count remains
// zero.
func TestProxyOpenAIWSHTTPBridgeTurnLaterTurn429FailsOverBeforeClientWrite(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"60"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 5845, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_old","input":[{"role":"user","content":"continue"}]}`)
	writes := 0

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5.6-sol", Turn: 281, WriteClient: func([]byte) error {
		writes++
		return nil
	}})

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, writes)
}

// TestOpenAIWSHTTPBridgeLaterTurn429CarriesCurrentTurnReplayPayload completes
// one turn, fails its tool-output continuation and retries on another
// provider. Retry bytes remove previous_response_id, retain four conversation
// items and one matching call, and the client receives the second response
// without replaying the first turn.
func TestOpenAIWSHTTPBridgeLaterTurn429CarriesCurrentTurnReplayPayload(t *testing.T) {
	options := &wsFixtureOptions{}
	options.Request.URLPolicy.Enabled = false
	options.Request.URLPolicy.AllowInsecureHTTP = true

	options.WS.ReadTimeoutSeconds = 3
	options.WS.WriteTimeoutSeconds = 3

	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":          []string{"text/event-stream"},
				openAIWSTurnStateHeader: []string{"old-provider-state"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_first\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"first-ok\"}]},{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"inspect\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n",
			)),
		},
		{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"60"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_second\",\"output\":[{\"id\":\"msg_2\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"second-ok\"}]}],\"usage\":{\"input_tokens\":4,\"output_tokens\":1}}}\n\n",
			)),
		},
	}}
	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5845, Name: "limited", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
			Extra: map[string]any{"openai_oauth_responses_websockets_v2_mode": "http_bridge"},
		},
	}
	nextProvider := *provider
	nextProvider.Record.ID = 5846
	nextProvider.Record.Name = "replacement"

	serverErrCh := make(chan error, 1)
	// Retain one current-turn replay while replacement execution sends its terminal;
	// the main test can inspect retry bytes after reading that client completion.
	retryPayloadCh := make(chan []byte, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = r.Clone(r.Context())
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		proxyErr := svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, provider, "access-token-a", firstMessage, nil)
		if _, ok := errors.AsType[*forwardcore.UpstreamFailoverError](proxyErr); !ok {
			serverErrCh <- proxyErr
			return
		}
		retryPayload, retryCurrentTurn := ws.CurrentTurnRetryPayload(proxyErr)
		if !retryCurrentTurn || len(retryPayload) == 0 {
			serverErrCh <- errors.New("missing current-turn retry payload")
			return
		}
		// Send this later turn's replay before invoking the replacement provider, so
		// assertions inspect the same full-input request that produces the second response.
		retryPayloadCh <- retryPayload
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(
			r.Context(), ginCtx, conn, &nextProvider, "access-token-b", retryPayload, nil,
		)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[{"role":"user","content":"first"}]}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, completed, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())

	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_first","input":[{"type":"function_call_output","call_id":"call_1","output":"second"}]}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead = context.WithTimeout(context.Background(), 3*time.Second)
	_, retriedCompleted, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(retriedCompleted, "type").String())
	require.Equal(t, "resp_second", gjson.GetBytes(retriedCompleted, "response.id").String())
	_ = clientConn.Close(coderws.StatusNormalClosure, "done")

	select {
	case retryPayload := <-retryPayloadCh:
		require.False(t, gjson.GetBytes(retryPayload, "previous_response_id").Exists())
		require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(retryPayload, "model").String())
		input := gjson.GetBytes(retryPayload, "input")
		require.True(t, input.IsArray())
		require.Len(t, input.Array(), 4)
		require.Contains(t, input.Raw, "first")
		require.Contains(t, input.Raw, "first-ok")
		require.Contains(t, input.Raw, "second")
		require.Equal(t, 1, strings.Count(input.Raw, `"id":"fc_1"`))
		require.Equal(t, 2, strings.Count(input.Raw, `"call_id":"call_1"`))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for current-turn failover")
	}

	select {
	case proxyErr := <-serverErrCh:
		require.NoError(t, proxyErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replacement-provider completion")
	}
	require.Len(t, upstream.bodies, 3)
	require.Contains(t, string(upstream.bodies[0]), "first")
	require.NotContains(t, string(upstream.bodies[2]), "previous_response_id")
	require.Contains(t, string(upstream.bodies[2]), "second")
}

// TestProxyOpenAIWSHTTPBridgeTurnRewritesCapacityShedCodeForClient compares
// later bare error and failed-terminal overload frames. Each writes one
// server_error representation retaining the message; bare-error stream end
// returns an error while the supplied failed terminal returns nil.
func TestProxyOpenAIWSHTTPBridgeTurnRewritesCapacityShedCodeForClient(t *testing.T) {
	tests := []struct {
		name    string // bridge scenario diagnostic label
		turn    int    // local bridge execution turn number
		body    string // scripted upstream SSE failure bytes
		wantErr bool   // expected final ordinary error presence
	}{
		{
			name:    "turn2_error_frame",
			turn:    2,
			body:    "data: {\"type\":\"error\",\"error\":{\"type\":\"service_unavailable_error\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}\n\n",
			wantErr: true,
		},
		{
			// A supplied failed terminal ends the later turn directly, while a bare
			// error ending at EOF selects synthesis and retains an ordinary error result.
			name: "turn2_bare_response_failed",
			turn: 2,
			body: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_shed\",\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: tt.turn, WriteClient: func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			}})

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, writes, 1)
			require.Contains(t, string(writes[0]), `"code":"server_error"`)
			require.NotContains(t, string(writes[0]), "server_is_overloaded")
			require.Contains(t, string(writes[0]), "Our servers are currently overloaded")
		})
	}
}

// TestProxyOpenAIWSHTTPBridgeTurnBareErrorUsesAuthoritativeFailed sends
// partial text, a structured 403 error and its failed terminal. The result
// retains terminal usage with nil ordinary error, the client receives text
// then failure, and provider error writes occur once.
func TestProxyOpenAIWSHTTPBridgeTurnBareErrorUsesAuthoritativeFailed(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","response_id":"resp_failed","delta":"partial"}`,
		``,
		`data: {"type":"error","error":{"status_code":403,"code":"workspace_suspended","message":"workspace is suspended"}}`,
		``,
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","usage":{"input_tokens":9,"output_tokens":2},"error":{"status_code":403,"code":"workspace_suspended","message":"workspace is suspended"}}}`,
		``,
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	repo := &openAIStream403ProviderRepo{}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream, health: newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 111, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 2, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Len(t, writes, 2)
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "response.failed", gjson.GetBytes(writes[1], "type").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnBareErrorEOFSynthesizesFailed ends after
// created and a bare invalid-request error. The adapter returns bad request
// alongside a failed result and synthesizes a failed terminal using the
// already observed response ID.
func TestProxyOpenAIWSHTTPBridgeTurnBareErrorEOFSynthesizesFailed(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_eof\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n"
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 112, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 2, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.EqualError(t, err, "bad request")
	require.NotNil(t, result)
	require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
	require.Len(t, writes, 2)
	require.Equal(t, "response.failed", gjson.GetBytes(writes[1], "type").String())
	require.Equal(t, "failed", gjson.GetBytes(writes[1], "response.status").String())
	require.Equal(t, "resp_eof", gjson.GetBytes(writes[1], "response.id").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnBareErrorFollowedByCompletedUsesCompleted
// places a recoverable bare error before successful completion. Only created
// and completed reach the writer, and the result records response.completed
// with eight input tokens and four output tokens.
func TestProxyOpenAIWSHTTPBridgeTurnBareErrorFollowedByCompletedUsesCompleted(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_recovered","status":"in_progress"}}`,
		``,
		`data: {"type":"error","error":{"code":"transient","message":"retrying"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_recovered","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":4}}}`,
		``,
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 113, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 2, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Len(t, writes, 2)
	require.Equal(t, "response.created", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(writes[1], "type").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnStagesMetadataBeforeCapacityFailover supplies
// created and in-progress metadata before an overload terminal. The first
// attempt returns request-scoped retryable failover with nil result and leaves
// every staged event outside client delivery.
func TestProxyOpenAIWSHTTPBridgeTurnStagesMetadataBeforeCapacityFailover(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_shed"}}`,
		"",
		`data: {"type":"response.in_progress","response":{"id":"resp_shed"}}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_shed","status":"failed","error":{"message":"Our servers are currently overloaded. Please try again later."}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-ws-bridge-capacity"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.True(t, failoverErr.RequestScopedTransient)
	require.Empty(t, writes)
}

// TestProxyOpenAIWSHTTPBridgeTurnDoesNotReplayCapacityAfterSemanticOutput
// delivers text before overload failure. Three events reach the writer with
// rewritten server_error, result remains present and error nil, and the
// captured log identifies suppressed failover on ws_http_bridge.
func TestProxyOpenAIWSHTTPBridgeTurnDoesNotReplayCapacityAfterSemanticOutput(t *testing.T) {
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_partial"}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_partial","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-ws-bridge-post-output"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 13, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}})

	require.NotNil(t, result)
	require.NoError(t, err)
	require.Len(t, writes, 3)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, string(writes[2]), `"code":"server_error"`)
	require.NotContains(t, string(writes[2]), "server_is_overloaded")
	require.True(t, logSink.ContainsMessage("gateway.failover_suppressed_after_semantic_output"))
	require.True(t, logSink.ContainsFieldValue("path", "ws_http_bridge"))
}

// TestProxyOpenAIWSHTTPBridgeTurnRequiresTerminalEvent ends first-turn streams
// with DONE alone or after created progress. Both return nil result and
// upstream failover while the writer receives no staged events, so DONE cannot
// substitute for response completion.
func TestProxyOpenAIWSHTTPBridgeTurnRequiresTerminalEvent(t *testing.T) {
	tests := []struct {
		// body ends at DONE without a response terminal. Both exercised rows expect
		// wantFailover with no result, while wantWrites checks that progress buffered
		// before this failure remains outside client delivery.
		name         string // bridge scenario diagnostic label
		body         string // scripted upstream SSE failure bytes
		wantFailover bool   // expected upstream failure marker presence
		wantWrites   int    // expected client event write count
	}{
		{name: "done_without_events_fails_over", body: "data: [DONE]\n\n", wantFailover: true},
		{
			name: "created_then_done_fails_over_before_semantic_output",
			body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_truncated\"}}\n\n" +
				"data: [DONE]\n\n",
			wantFailover: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: 1, WriteClient: func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			}})

			var failoverErr *forwardcore.UpstreamFailoverError
			if tt.wantFailover {
				require.Nil(t, result)
				require.ErrorAs(t, err, &failoverErr)
			} else {
				require.NotNil(t, result)
				require.Error(t, err)
				require.False(t, errors.As(err, &failoverErr))
			}
			require.Len(t, writes, tt.wantWrites)
		})
	}
}

// TestOpenAIWSHTTPBridgeRelaysSSEFramesAsWebSocketMessages sends created, text
// and completion through an actual client socket. Client reads check event
// types, while the result records response ID and usage. The captured HTTP
// request forces stream, removes WS controls and applies Lite header and
// parallel-tool rules.
func TestOpenAIWSHTTPBridgeRelaysSSEFramesAsWebSocketMessages(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_bridge","model":"gpt-5"}}`,
		"",
		`data: {"type":"response.output_text.delta","response":{"id":"resp_bridge"},"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_bridge","model":"gpt-5","usage":{"input_tokens":3,"output_tokens":2}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid_bridge"},
		},
		Body: io.NopCloser(strings.NewReader(sseBody)),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{WS: OpenAIWSOptions{HTTPBridgeThresholdBytes: 1}, Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "api-key",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Status:      billing.StatusActive,
		},
	}
	payload := []byte(`{"type":"response.create","generate":true,"model":"gpt-5","stream":true,"parallel_tool_calls":true,"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"},"input":"hi"}`)

	// bridgeResult keeps the HTTP turn result and worker error in one channel value.
	type bridgeResult struct {
		// result and err are the outcome of the same bridge invocation. The buffered
		// send lets the handler publish its outcome while client reads events before inspecting
		// these values; a nonnil result can coexist with an error.
		result *forwardcore.OpenAIResult // observed bridge result metadata
		err    error                     // final bridge invocation error
	}
	// Buffer the one bridge outcome while the main test reads three socket events;
	// reporting the result therefore does not depend on the main test selecting it first.
	resultCh := make(chan bridgeResult, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			resultCh <- bridgeResult{err: err}
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		ginCtx.Request = req

		writeClient := func(message []byte) error {
			writeCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			return conn.Write(writeCtx, coderws.MessageText, message)
		}
		result, bridgeErr := svc.proxyOpenAIWSHTTPBridgeTurn(r.Context(), ginCtx, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: 1, WriteClient: writeClient})
		resultCh <- bridgeResult{result: result, err: bridgeErr}
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	readEvent := func() []byte {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		msgType, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return event
	}

	created := readEvent()
	delta := readEvent()
	completed := readEvent()

	require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta, "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())

	select {
	case bridge := <-resultCh:
		require.NoError(t, bridge.err)
		require.NotNil(t, bridge.result)
		require.Equal(t, "resp_bridge", bridge.result.RequestID)
		require.Equal(t, 3, bridge.result.Usage.InputTokens)
		require.Equal(t, 2, bridge.result.Usage.OutputTokens)
		require.True(t, bridge.result.OpenAIWSMode)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for bridge result")
	}

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	require.Equal(t, "true", upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())
}

// TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultsEmptyModelTo45 executes a Grok
// request that omits model. The HTTP body and result UpstreamModel select the
// configured code default, BillingModel remains empty, and two upstream events
// reach the writer.
func TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultsEmptyModelTo45(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_default","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_default","model":"grok-4.5","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 72,
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL},
		},
	}
	payload := []byte(`{"type":"response.create","generate":true,"stream":true,"input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Empty(t, result.BillingModel)
	require.Equal(t, xai.DefaultResponsesModel, result.UpstreamModel)
	require.Len(t, events, 2)
}

// TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultModelErrorUsesCanonicalKey
// supplies model_not_found after a model-less Grok request. The adapter
// returns nil result and failover, and exactly one model cooldown uses the
// default model actually selected for sending.
func TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultModelErrorUsesCanonicalKey(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"model_not_found","message":"model not found"}}`)),
	}}
	repo := &grokModelStateProviderRepo{}
	svc := newWSFixture(wsFixtureInputs{providers: repo, health: newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil), options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 73,
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL},
		},
	}
	payload := []byte(`{"type":"response.create","generate":true,"stream":true,"input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, xai.DefaultResponsesModel, repo.modelRateLimitCalls[0].scope)
}

// TestProxyOpenAIWSHTTPBridgeTurnForGrokFreeFunctionToolsUsesMixedRoute
// submits client lookup and search declarations for a Free Grok provider. The
// recorded HTTP body contains lookup plus native web and X search tools and
// retains auto choice.
func TestProxyOpenAIWSHTTPBridgeTurnForGrokFreeFunctionToolsUsesMixedRoute(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_tools","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_tools","model":"grok-4.5","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 73,
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"base_url":          xai.DefaultCLIBaseURL,
				"subscription_tier": "free",
			},
		},
	}
	payload := []byte(`{
		"type":"response.create","generate":true,"stream":true,"input":"look up alpha",
		"tools":[
			{"type":"function","name":"lookup","parameters":{"type":"object"}},
			{"type":"function","name":"web_search","parameters":{"type":"object"}}
		],
		"tool_choice":"auto"
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "", GrokCacheIdentity: "cache-id", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.NoError(t, err)
	require.NotNil(t, result)
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
}

// TestProxyOpenAIWSHTTPBridgeTurnPromotesCodexAdditionalToolsForMixedCache
// supplies Codex additional_tools and a cache preference header. The body
// promotes supported declarations, removes additional_tools, custom and
// namespace forms, and shares the supplied cache identity with the Grok
// conversation header while consuming the local preference header.
func TestProxyOpenAIWSHTTPBridgeTurnPromotesCodexAdditionalToolsForMixedCache(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_codex_lite","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_codex_lite","model":"grok-4.5","usage":{"input_tokens":4,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 73,
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"base_url":          xai.DefaultCLIBaseURL,
				"subscription_tier": "free",
			},
		},
	}
	payload := []byte(`{
		"type":"response.create","generate":true,"model":"grok","stream":true,
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"function","name":"lookup","parameters":{"type":"object"}},
				{"type":"function","name":"web_search","parameters":{"type":"object"}},
				{"type":"custom","name":"apply_patch"},
				{"type":"namespace","name":"collaboration"}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("X-TokenRouter-Grok-Client-Tool-Cache", "prefer-cache")
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: "grok", RoutingModel: "grok", GrokCacheIdentity: "isolated-ws-cache-id", Turn: 1, WriteClient: func(message []byte) error {
		events = append(events, append([]byte(nil), message...))
		return nil
	}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, events, 2)
	require.False(t, gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools")`).Exists())
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 4)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "function", tools[2].Get("type").String())
	require.Equal(t, "apply_patch", tools[2].Get("name").String())
	require.Equal(t, "x_search", tools[3].Get("type").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="namespace")`).Exists())
	require.Equal(t, "isolated-ws-cache-id", gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, "isolated-ws-cache-id", upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Empty(t, upstream.lastReq.Header.Get("X-TokenRouter-Grok-Client-Tool-Cache"))
}

// TestProxyResponsesWebSocketFromClientForGrokUsesXAIHTTPBridge executes three
// socket turns with changing model and temperature capability. HTTP requests
// use Grok headers, retain model spelling, enable temperature only for turn
// two and reuse cache identity until the model changes, with WS-only controls
// removed.
func TestProxyResponsesWebSocketFromClientForGrokUsesXAIHTTPBridge(t *testing.T) {
	bridgeResponse := func(responseID, requestID string, cachedTokens int) *http.Response {
		sseBody := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"` + responseID + `","model":"grok-4.3"}}`,
			"",
			`data: {"type":"response.output_text.delta","response":{"id":"` + responseID + `"},"delta":"ok"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"` + responseID + `","model":"grok-4.3","usage":{"input_tokens":4,"output_tokens":2,"input_tokens_details":{"cached_tokens":` + fmt.Sprintf("%d", cachedTokens) + `}}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":   []string{"text/event-stream"},
				"Xai-Request-Id": []string{requestID},
			},
			Body: io.NopCloser(strings.NewReader(sseBody)),
		}
	}
	upstream := &auxiliaryHTTPRecorder{checkContext: true, responses: []*http.Response{
		bridgeResponse("resp_grok_ws_1", "xai-ws-req-1", 0),
		bridgeResponse("resp_grok_ws_2", "xai-ws-req-2", 3),
		bridgeResponse("resp_grok_ws_3", "xai-ws-req-3", 0),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 71,
			Name:        "grok",
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Status:      billing.StatusActive,
			Credentials: map[string]any{
				"base_url": xai.DefaultCLIBaseURL,
			},
		},
	}

	// Buffer final session return while three client turns run, then inspect
	// HTTP bodies after their sequential context and cache-identity decisions complete.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		if msgType != coderws.MessageText {
			errCh <- errors.New("first message was not text")
			return
		}

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		ginCtx.Request = req
		ginCtx.Set("api_key", &apikey.APIKey{ID: 7101})

		// Keep the server-abort signal while bridge request construction detaches client
		// cancellation; each cached turn context supplies its own attribute capability.
		requestCtx, abort := requestcontext.WithAbort(r.Context())
		defer abort()
		turnContexts := map[int]context.Context{}
		hooks := &ws.OpenAIIngressHooks{ContextForTurn: func(turn int) context.Context {
			if current, exists := turnContexts[turn]; exists {
				return current
			}
			// Change capability for only the middle turn, making refreshed logical-turn
			// context visible as temperature presence in that HTTP body alone.
			enabled := turn == 2
			attributes := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes {
				return modelcatalog.Attributes{Temperature: &enabled}
			}}}
			current := routing.WithModelAttributes(requestCtx, attributes)
			turnContexts[turn] = current
			return current
		}}
		errCh <- svc.ProxyResponsesWebSocketFromClient(requestCtx, ginCtx, conn, provider, "access-token", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok","stream":true,"temperature":0.4,"input":"hi","prompt_cache_retention":"24h"}`))
	cancelWrite()
	require.NoError(t, err)

	readEvent := func() []byte {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		msgType, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return event
	}

	created := readEvent()
	delta := readEvent()
	completed := readEvent()
	require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta, "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, "resp_grok_ws_1", gjson.GetBytes(completed, "response.id").String())

	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok","stream":true,"temperature":0.4,"previous_response_id":"resp_grok_ws_1","input":"second turn"}`))
	cancelWrite()
	require.NoError(t, err)

	created = readEvent()
	delta = readEvent()
	completed = readEvent()
	require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta, "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, "resp_grok_ws_2", gjson.GetBytes(completed, "response.id").String())

	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok-4.3","stream":true,"temperature":0.4,"previous_response_id":"resp_grok_ws_2","input":"third turn with a different model"}`))
	cancelWrite()
	require.NoError(t, err)

	created = readEvent()
	delta = readEvent()
	completed = readEvent()
	require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta, "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, "resp_grok_ws_3", gjson.GetBytes(completed, "response.id").String())

	_ = clientConn.Close(coderws.StatusNormalClosure, "done")
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(3 * time.Second):
		require.Fail(t, "proxy did not finish after client close")
	}

	require.Len(t, upstream.requests, 3)
	require.Len(t, upstream.bodies, 3)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "temperature").Exists())
	require.Equal(t, 0.4, gjson.GetBytes(upstream.bodies[1], "temperature").Float())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "temperature").Exists())
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "grok", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "grok", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.Equal(t, "grok-4.3", gjson.GetBytes(upstream.bodies[2], "model").String())
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	thirdIdentity := gjson.GetBytes(upstream.bodies[2], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.NotEmpty(t, thirdIdentity)
	require.NotEqual(t, firstIdentity, thirdIdentity)
	require.Equal(t, firstIdentity, upstream.requests[0].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, secondIdentity, upstream.requests[1].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, thirdIdentity, upstream.requests[2].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_retention").Exists())
}

// TestOpenAIWSHTTPBridgeAcceptsFirstFrameAboveLegacy16MiB sends a padded
// seventeen-MiB input below the client read limit and above the bridge
// threshold. The client receives completion and AfterTurn records usage; the
// captured HTTP body remains above sixteen MiB but below the bridge threshold,
// loses WS controls and uses the configured provider endpoint.
func TestOpenAIWSHTTPBridgeAcceptsFirstFrameAboveLegacy16MiB(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_large_bridge","model":"gpt-5"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_large_bridge","model":"gpt-5","usage":{"input_tokens":9,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid_large_bridge"},
		},
		Body: io.NopCloser(strings.NewReader(sseBody)),
	}}
	options := &wsFixtureOptions{WS: OpenAIWSOptions{ClientReadLimitBytes: 64 * 1024 * 1024, HTTPBridgeThresholdBytes: 17*1024*1024 + 512}, Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}
	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9,
			Name:     "api-key",
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":        "sk-upstream",
				"base_url":       "https://env-openai.example/v1",
				"model_provider": "env-openai",
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"responses_ws_connection_mode":                  "per_session",
			},
			Concurrency: 1,
			Status:      billing.StatusActive,
		},
	}

	// Leading whitespace raises the incoming size over the chosen threshold.
	// The recorded HTTP body is checked below that threshold and above sixteen MiB,
	// separating incoming frame sizing from the converted HTTP request size.
	payload := []byte(strings.Repeat(" ", 1024) + `{"type":"response.create","generate":true,"model":"gpt-5","stream":true,"input":"` + strings.Repeat("x", 17*1024*1024) + `"}`)
	require.Greater(t, len(payload), 16*1024*1024)
	require.GreaterOrEqual(t, int64(len(payload)), options.WS.HTTPBridgeThresholdBytes)
	require.Less(t, int64(len(payload)), ResolveOpenAIWSClientReadLimitBytes(wsFixtureRuntimeOptions(options)))

	// turnOutcome retains the logical turn and result reported by AfterTurn.
	type turnOutcome struct {
		// turn, result and err are captured together by AfterTurn before channel
		// publication. The main test receives this outcome after the client terminal
		// and server exit, keeping the result associated with its local turn.
		turn   int                       // local bridge execution turn number
		result *forwardcore.OpenAIResult // observed bridge result metadata
		err    error                     // final bridge invocation error
	}
	// Keep AfterTurn nonblocking until the large client frame has completed and
	// the main test can inspect the captured turn ID, usage and terminal outcome.
	turnOutcomeCh := make(chan turnOutcome, 1)
	hooks := &ws.OpenAIIngressHooks{
		AfterTurn: func(capture ws.OpenAITurnCapture) {
			turnOutcomeCh <- turnOutcome{turn: capture.Turn, result: capture.Result, err: capture.Err}
		},
	}

	// Keep session return buffered while the client consumes its large-frame
	// response; that outcome puts worker completion before the retained usage check.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		// Apply the fixture client limit before reading so the padded large frame
		// reaches ingress transport selection rather than failing WebSocket admission.
		conn.SetReadLimit(ResolveOpenAIWSClientReadLimitBytes(wsFixtureRuntimeOptions(options)))

		readCtx, cancelRead := context.WithTimeout(r.Context(), 10*time.Second)
		msgType, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			errCh <- gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "unexpected client websocket message type", nil)
			return
		}

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "codex_cli_rs/0.135.0")
		ginCtx.Request = req

		proxyCtx, cancelProxy := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancelProxy()
		errCh <- svc.ProxyResponsesWebSocketFromClient(proxyCtx, ginCtx, conn, provider, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 20*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, payload)
	cancelWrite()
	require.NoError(t, err)

	var eventTypes []string
	for {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
		msgType, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)

		eventType := gjson.GetBytes(event, "type").String()
		eventTypes = append(eventTypes, eventType)
		if eventType == "response.completed" {
			break
		}
	}
	require.Contains(t, eventTypes, "response.created")
	require.Contains(t, eventTypes, "response.completed")

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for websocket bridge proxy to finish")
	}

	select {
	case outcome := <-turnOutcomeCh:
		require.NoError(t, outcome.err)
		require.Equal(t, 1, outcome.turn)
		require.NotNil(t, outcome.result)
		require.Equal(t, 9, outcome.result.Usage.InputTokens)
		require.Equal(t, 1, outcome.result.Usage.OutputTokens)
		require.Equal(t, "response.completed", outcome.result.UpstreamTerminalEvent)
	default:
		t.Fatal("AfterTurn was not called for websocket HTTP bridge turn")
	}

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	require.Equal(t, "https://env-openai.example/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "env-openai", provider.View().GetCredential("model_provider"))
	require.Less(t, int64(len(upstream.lastBody)), options.WS.HTTPBridgeThresholdBytes)
	require.Greater(t, len(upstream.lastBody), 16*1024*1024)
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "gpt-5", gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestOpenAIWSHTTPBridgeKeepsContinuationFramesOnHTTPWithoutPreviousResponseID
// completes an HTTP bridge turn that returns a function call, then sends its
// output continuation. Both HTTP bodies omit previous_response_id, the second
// input contains prior input/call/output, and the configured WS dialer and
// writer stay unused.
func TestOpenAIWSHTTPBridgeKeepsContinuationFramesOnHTTPWithoutPreviousResponseID(t *testing.T) {
	firstSSEBody := `data: {"type":"response.completed","response":{"id":"resp_bridge_first","model":"gpt-5.1","output":[{"type":"function_call","id":"fc_bridge_1","call_id":"call_bridge_1","name":"shell","arguments":"{}"}],"usage":{"input_tokens":9,"output_tokens":1}}}` + "\n" + ""
	secondSSEBody := `data: {"type":"response.completed","response":{"id":"resp_bridge_second","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" + ""
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(firstSSEBody)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(secondSSEBody)),
		},
	}}
	options := &wsFixtureOptions{}
	options.Request.URLPolicy.Enabled = false
	options.Request.URLPolicy.AllowInsecureHTTP = true

	options.WS.HTTPBridgeThresholdBytes = 1
	options.Pool.MaxConnsPerProvider = 1
	options.Pool.MinIdlePerProvider = 0
	options.Pool.MaxIdlePerProvider = 1
	options.Pool.QueueLimitPerConn = 8
	options.WS.DialTimeoutSeconds = 3
	options.WS.ReadTimeoutSeconds = 3
	options.WS.WriteTimeoutSeconds = 3

	captureConn := &openAIWSCaptureConn{}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(options)
	pool.SetClientDialerForTest(captureDialer)

	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector(), pool: pool})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 19,
			Name:        "api-key-bridge-handoff",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-upstream"},
			Extra: map[string]any{
				"responses_websockets_v2_enabled": true,
			},
			Concurrency: 1,
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	// Retain server return while the client reads both bridged turns, then
	// inspect HTTP replay and unused WS captures after session execution has finished.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			errCh <- gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "unexpected client websocket message type", nil)
			return
		}

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "codex_cli_rs/0.135.0")
		ginCtx.Request = req

		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, provider, "sk-test", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeMessage := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelWrite()
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
	}
	readMessage := func() []byte {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelRead()
		msgType, event, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return event
	}

	writeMessage(`{"type":"response.create","model":"gpt-5.1","stream":true,"input":"first"}`)
	firstTurnEvent := readMessage()
	require.Equal(t, "response.completed", gjson.GetBytes(firstTurnEvent, "type").String())
	require.Equal(t, "resp_bridge_first", gjson.GetBytes(firstTurnEvent, "response.id").String())

	writeMessage(`{"type":"response.create","model":"gpt-5.1","stream":false,"previous_response_id":"resp_bridge_first","input":[{"type":"function_call_output","call_id":"call_bridge_1","output":"ok"}]}`)
	secondTurnEvent := readMessage()
	require.Equal(t, "response.completed", gjson.GetBytes(secondTurnEvent, "type").String())
	require.Equal(t, "resp_bridge_second", gjson.GetBytes(secondTurnEvent, "response.id").String())

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket bridge proxy to finish")
	}

	require.Len(t, upstream.bodies, 2, "进入 HTTP bridge 后同一客户端 WS 连接内应保持 HTTP/SSE bridge")
	require.False(t, gjson.GetBytes(upstream.bodies[0], "previous_response_id").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "previous_response_id").Exists())
	secondInput := gjson.GetBytes(upstream.bodies[1], "input").Array()
	require.Len(t, secondInput, 3)
	require.Equal(t, "first", secondInput[0].String())
	require.Equal(t, "function_call", secondInput[1].Get("type").String())
	require.Equal(t, "call_bridge_1", secondInput[1].Get("call_id").String())
	require.Equal(t, "function_call_output", secondInput[2].Get("type").String())
	require.Equal(t, "call_bridge_1", secondInput[2].Get("call_id").String())
	require.Equal(t, 0, captureDialer.DialCount())
	require.Empty(t, captureConn.writes)
}

// TestOpenAIWSHTTPBridge_IdleTimeoutClosesClientSession completes one bridged
// request then leaves the client idle. Client and server observe normal
// closure with websocket idle timeout, and exactly one HTTP body shows no
// later continuation was submitted.
func TestOpenAIWSHTTPBridge_IdleTimeoutClosesClientSession(t *testing.T) {
	sseBody := `data: {"type":"response.completed","response":{"id":"resp_bridge_idle","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" + ""
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sseBody)),
	}}
	options := &wsFixtureOptions{}
	options.Request.URLPolicy.Enabled = false
	options.Request.URLPolicy.AllowInsecureHTTP = true

	options.WS.HTTPBridgeThresholdBytes = 1
	options.WS.IngressInterTurnIdleTimeoutSeconds = 1
	options.Pool.MaxConnsPerProvider = 1
	options.Pool.MinIdlePerProvider = 0
	options.Pool.MaxIdlePerProvider = 1
	options.Pool.QueueLimitPerConn = 8

	svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}, corrector: openai.NewCodexToolCorrector()})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 20,
			Name:        "api-key-bridge-idle-timeout",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-upstream"},
			Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			Concurrency: 1,
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	// Buffer idle-session return while the client reads completion and close,
	// then inspect the final HTTP count after the server confirms normal timeout.
	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, provider, "sk-test", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false,"input":"hello"}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

	closeReadCtx, cancelCloseRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(closeReadCtx)
	cancelCloseRead()
	var clientClose coderws.CloseError
	require.ErrorAs(t, err, &clientClose)
	require.Equal(t, coderws.StatusNormalClosure, clientClose.Code)
	require.Equal(t, "websocket idle timeout", clientClose.Reason)

	select {
	case proxyErr := <-errCh:
		var closeErr *gatewayhttp.OpenAIWSClientCloseError
		require.ErrorAs(t, proxyErr, &closeErr)
		require.Equal(t, coderws.StatusNormalClosure, closeErr.StatusCode())
		require.Equal(t, "websocket idle timeout", closeErr.Reason())
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for idle HTTP bridge session to close")
	}
	require.Len(t, upstream.bodies, 1, "an idle client must not leave a continuation request running")
}

// TestHTTPBridgeWarmupDoesNotGenerate executes generate=false against a
// prepared HTTP fixture. No HTTP request is recorded; usage and output stay
// empty, and two local events include completion correlated to the result's
// response ID.
func TestHTTPBridgeWarmupDoesNotGenerate(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_remote\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n")),
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 999, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","generate":false,"input":"hello"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5.5", Turn: 1, WriteClient: func(b []byte) error { events = append(events, append([]byte(nil), b...)); return nil }})
	require.NoError(t, err)
	require.Empty(t, upstream.requests)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Len(t, events, 2)
	require.Equal(t, "response.created", gjson.GetBytes(events[0], "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(events[1], "type").String())
	require.Equal(t, result.RequestID, gjson.GetBytes(events[1], "response.id").String())
	require.Empty(t, gjson.GetBytes(events[1], "response.output").Array())
}

// TestHTTPBridgeWarmupContinuation warms OpenAI and Grok API Key bridge
// sessions with custom declarations before an anchored generating request.
// Warmup stays local; one later HTTP call inherits input and lowered tools,
// restores custom terminal output and records generated usage.
func TestHTTPBridgeWarmupContinuation(t *testing.T) {
	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_generated","model":"test-model","status":"completed","output":[{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}],"usage":{"input_tokens":5,"output_tokens":3}}}` + "\n\n")),
			}}
			options := &wsFixtureOptions{Output: gatewayhttp.OpenAIResponseOptions{MaxLineSize: gatewayhttp.OpenAIResponseDefaultMaxLineSize}}
			options.Request.URLPolicy.Enabled = false
			svc := newWSFixture(wsFixtureInputs{options: options, transport: upstream, cache: &sessiontestkit.StickyCache{}})
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 7001, Platform: platform, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", providercore.UpstreamProtocolsKey: []string{"openai_responses"}},
			}}
			// Buffer both turn captures and final session return so warmup and generating
			// callbacks can complete while the client reads their corresponding terminal events.
			captures := make(chan ws.OpenAITurnCapture, 2)
			finished := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					finished <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				_, first, err := conn.Read(ctx)
				if err != nil {
					finished <- err
					return
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r
				finished <- svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, provider, "test", first, &ws.OpenAIIngressHooks{AfterTurn: func(capture ws.OpenAITurnCapture) { captures <- capture }})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"test-model","generate":false,"tools":[{"type":"custom","name":"exec","description":"Run command"}],"input":[{"role":"user","content":"hello"}]}`)))
			var warmID string
			for range 2 {
				_, event, err := client.Read(ctx)
				require.NoError(t, err)
				if gjson.GetBytes(event, "type").String() == "response.completed" {
					warmID = gjson.GetBytes(event, "response.id").String()
				}
			}
			require.NotEmpty(t, warmID)
			// Observe local warmup completion before sending its continuation, so the
			// second turn can inherit retained input and tools without an earlier HTTP call.
			first := <-captures
			require.NoError(t, first.Err)
			require.True(t, first.Result.LocalWarmup)
			require.Empty(t, upstream.requests)
			request, err := json.Marshal(map[string]any{"type": "response.create", "model": "test-model", "previous_response_id": warmID, "input": []any{}})
			require.NoError(t, err)
			require.NoError(t, client.Write(ctx, coderws.MessageText, request))
			_, event, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "resp_generated", gjson.GetBytes(event, "response.id").String())
			require.Equal(t, "custom_tool_call", gjson.GetBytes(event, "response.output.0.type").String())
			require.Equal(t, "exec", gjson.GetBytes(event, "response.output.0.name").String())
			require.Equal(t, "pwd", gjson.GetBytes(event, "response.output.0.input").String())
			second := <-captures
			require.NoError(t, second.Err)
			require.False(t, second.Result.LocalWarmup)
			require.Equal(t, 3, second.Result.Usage.OutputTokens)
			require.Len(t, upstream.requests, 1)
			require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
			require.Len(t, gjson.GetBytes(upstream.lastBody, "input").Array(), 1)
			require.Contains(t, gjson.GetBytes(upstream.lastBody, "input").Raw, "hello")
			require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
			require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
			require.NoError(t, client.Close(coderws.StatusNormalClosure, ""))
			require.NoError(t, <-finished)
		})
	}
}

// TestHTTPBridgeWarmupWriteFailure injects failure on each of the two local
// warmup event writes. The helper returns the same cause and nil result
// immediately, with its write count ending at the failing event.
func TestHTTPBridgeWarmupWriteFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		writes := 0
		rejected := errors.New("client disconnected")
		result, err := completeHTTPBridgeWarmup("model", func([]byte) error {
			writes++
			if writes == failAt {
				return rejected
			}
			return nil
		})
		require.ErrorIs(t, err, rejected)
		require.Nil(t, result)
		require.Equal(t, failAt, writes)
	}
}

// TestOpenAIWSHTTPBridgeGrok429PersistsRateLimit supplies a Grok 429 with
// Retry-After 45. The result is nil with error, one provider cooldown expires
// near forty-five seconds, temporary blocks remain absent and runtime blocking
// is observed.
func TestOpenAIWSHTTPBridgeGrok429PersistsRateLimit(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := newWSFixture(wsFixtureInputs{providers: repo, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 68, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
	before := time.Now()

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), nil, wsHTTPBridgeTurnInput{Provider: provider, Token: "token", Payload: []byte(`{"type":"response.create","model":"grok-4.3","input":"hi"}`), PayloadBytes: 64, OriginalModel: "grok-4.3", RoutingModel: "grok-4.3", GrokCacheIdentity: "cache-id", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, wsFixtureProviderBlocked(svc, provider))
}

// TestOpenAIWSHTTPBridgeSSEErrorSideEffectsRunOncePerPlatform supplies a
// rate-limit SSE error to OpenAI and Grok OAuth fixtures. Each returns nil
// result and 429 failover before writes, while exactly one repository
// rate-limit call records provider effects.
func TestOpenAIWSHTTPBridgeSSEErrorSideEffectsRunOncePerPlatform(t *testing.T) {
	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			repo := &grokQuotaProviderRepo{}
			options := &wsFixtureOptions{}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"limited\"}}\n\n",
				)),
			}}
			svc := newWSFixture(wsFixtureInputs{options: options, providers: repo, transport: upstream})
			if platform == capability.PlatformOpenAI {
				setWSFixtureHealth(svc, newUpstreamHealthForTest(repo, options, nil, providercore.HealthOptions{}, nil))
			}
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 70, Platform: platform, Type: capability.ProviderTypeOAuth, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			writes := 0

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: 1, WriteClient: func([]byte) error {
				writes++
				return nil
			}})

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Zero(t, writes)
			require.Equal(t, 1, repo.rateLimitedCalls)
		})
	}
}

// TestOpenAIWSHTTPBridgeTerminalSideEffectsWithOptionalRequest sends both
// failure event forms before output. It preserves request-scoped effect reuse
// and the independent health evaluation used when no Gin request is present.
func TestOpenAIWSHTTPBridgeTerminalSideEffectsWithOptionalRequest(t *testing.T) {
	for _, event := range []string{"error", "response.failed"} {
		for _, tt := range []struct {
			name       string // request context scenario name
			withGin    bool   // request context is available
			wantHealth int    // expected provider health writes
		}{
			{name: "request", withGin: true, wantHealth: 1},
			{name: "standalone", wantHealth: 2},
		} {
			t.Run(event+"/"+tt.name, func(t *testing.T) {
				errorJSON := `"error":{"type":"authentication_error","code":"invalid_api_key","message":"credential rejected"}`
				eventJSON := `{"type":"error",` + errorJSON + `}`
				if event == "response.failed" {
					eventJSON = `{"type":"response.failed","response":{` + errorJSON + `}}`
				}
				repo := &openAIStream403ProviderRepo{}
				options := &wsFixtureOptions{}
				upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("data: " + eventJSON + "\n\n")),
				}}
				svc := newWSFixture(wsFixtureInputs{options: options, providers: repo, transport: upstream})
				setWSFixtureHealth(svc, newUpstreamHealthForTest(repo, options, nil, providercore.HealthOptions{}, nil))
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           71,
					Platform:     capability.PlatformOpenAI,
					Type:         capability.ProviderTypeAPIKey,
					Concurrency:  1,
					Credentials: map[string]any{
						"custom_error_codes_enabled": true,
						"custom_error_codes":         []any{float64(http.StatusUnauthorized)},
					},
				}}
				var c *gin.Context
				if tt.withGin {
					c, _ = gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
				}
				payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
				writes := 0

				result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: provider, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", RoutingModel: "gpt-5", Turn: 1, WriteClient: func([]byte) error {
					writes++
					return nil
				}})

				require.Nil(t, result)
				var failure *forwardcore.UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, http.StatusUnauthorized, failure.StatusCode)
				require.Zero(t, writes)
				require.Equal(t, tt.wantHealth, repo.setErrorCalls)
			})
		}
	}
}

// TestOpenAIWSHTTPBridgeGrokExhaustedSuccessPersistsRateLimit completes an
// HTTP 200 stream whose headers report zero remaining requests. The result
// succeeds, but one cooldown retains the supplied reset timestamp and the
// provider becomes runtime blocked.
func TestOpenAIWSHTTPBridgeGrokExhaustedSuccessPersistsRateLimit(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	// Match integer-second rate headers so the expected reset has no fractional
	// part before provider health reads it and records the exhausted request window.
	resetAt := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	resp := grokMessagesSSECompletedResponse("resp_ws_limited", 0)
	resp.Header.Set("X-Ratelimit-Limit-Requests", "10")
	resp.Header.Set("X-Ratelimit-Remaining-Requests", "0")
	resp.Header.Set("X-Ratelimit-Reset-Requests", fmt.Sprintf("%d", resetAt.Unix()))
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newWSFixture(wsFixtureInputs{providers: repo, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 69, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1}}

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), nil, wsHTTPBridgeTurnInput{Provider: provider, Token: "token", Payload: []byte(`{"type":"response.create","model":"grok-4.3","input":"hi"}`), PayloadBytes: 64, OriginalModel: "grok-4.3", RoutingModel: "grok-4.3", GrokCacheIdentity: "cache-id", Turn: 1, WriteClient: func([]byte) error { return nil }})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.True(t, wsFixtureProviderBlocked(svc, provider))
}

// grokMessagesSSECompletedResponse returns a fresh HTTP 200 SSE response
// ending in completion and DONE. Supplied response ID and cached-token count
// are encoded into fixed usage; callers own closing the newly constructed
// response body.
func grokMessagesSSECompletedResponse(responseID string, cachedTokens int) *http.Response {
	body := strings.Join([]string{
		fmt.Sprintf(`data: {"type":"response.completed","response":{"id":%q,"object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":%d}}}}`, responseID, cachedTokens),
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestProxyOpenAIWSHTTPBridgeGrokReasoningOptions applies offline attributes
// to known and private Grok models. Reading reasoning.effort from captured
// bodies yields empty for Build, xhigh for the known reasoning model and max
// for the private model with no catalog metadata.
func TestProxyOpenAIWSHTTPBridgeGrokReasoningOptions(t *testing.T) {
	// model selects offline attribute rules, effort supplies client intent, and
	// want records the sent parameter after preparation. Empty want marks the
	// known unsupported knob; unknown model metadata retains its supplied level.
	for _, tc := range []struct {
		model, effort, want string /* requested model identifier; supplied client effort; expected outbound effort */
	}{{"grok-build-0.1", "high", ""}, {"grok-4.7", "xhigh", "xhigh"}, {"grok-private-route", "max", "max"}} {
		t.Run(tc.model, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_options","model":"` + tc.model + `","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))}}
			svc := newWSFixture(wsFixtureInputs{transport: upstream})
			svc.Requests.Readers = newExecutionReadersFixture(nil, nil)
			svc.Requests.Readers.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
			target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 72, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Concurrency: 1, Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}}}
			payload := []byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":"hi","reasoning":{"effort":%q}}`, tc.model, tc.effort))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, wsHTTPBridgeTurnInput{Provider: target, Token: "access-token", Payload: payload, PayloadBytes: len(payload), OriginalModel: tc.model, RoutingModel: tc.model, Turn: 1, WriteClient: func([]byte) error { return nil }})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

// RoundTrip delegates one local response fixture to its configured handler.
func (f bridgeDiagnosticRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Do sends one fixture exchange through production attempt and header observation.
func (p *bridgeDiagnosticTransport) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	return p.pool.Do(req, httpclient.UpstreamRequestOptions{ProviderID: id, PrepareClient: func(c *http.Client) *http.Client {
		c.Transport = bridgeDiagnosticRoundTrip(func(r *http.Request) (*http.Response, error) {
			response := p.responses[0]
			p.responses = p.responses[1:]
			response.Request = r
			return response, nil
		})
		return c
	}})
}

// DoWithTLS reuses fixture dispatch while leaving fingerprint behavior outside this probe.
func (p *bridgeDiagnosticTransport) DoWithTLS(req *http.Request, proxy string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return p.Do(req, proxy, id, n)
}

// TestHTTPBridgeUsesTransportAttemptForDiagnostics runs the real bridge and HTTP
// pool through success, rejection, and rejected-field retry. Header provenance,
// response identity, and completion must agree on the transport's dispatch number.
func TestHTTPBridgeUsesTransportAttemptForDiagnostics(t *testing.T) {
	for _, mode := range []string{"success", "retry", "failure", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			var saved telemetry.RequestRecord
			ctx := context.WithValue(t.Context(), telemetry.RequestID, "turn")
			ctx = telemetry.WithRequestCapture(ctx, telemetry.RequestRecord{RequestID: "turn", StartedAt: time.Now()}, func(r telemetry.RequestRecord) { saved = r })
			ctx = telemetry.WithRequestProvider(ctx, 7, "X-Custom")
			target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 7, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1, Extra: map[string]any{providercore.ProviderExtraUpstreamRequestIDHeader: "X-Custom"}}}
			responses := []*http.Response{}
			if mode == "retry" {
				responses = append(responses, &http.Response{StatusCode: 400, Header: http.Header{"X-Custom": {"rejected"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"truncation","message":"Unsupported parameter: truncation"}}`))})
			}
			status := 200
			body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bridge\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			if mode == "failure" {
				status = 503
				body = `{"error":{"message":"unavailable"}}`
			}
			if mode == "interrupted" {
				body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bridge\"}}\n\n"
			}
			responses = append(responses, &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Custom": {"selected"}, "X-Request-Id": {"unselected"}}, Body: io.NopCloser(strings.NewReader(body))})
			count := len(responses)
			transport := &bridgeDiagnosticTransport{pool: httpclient.NewUpstreamPool(), responses: responses}
			svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: transport})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)
			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(ctx, c, wsHTTPBridgeTurnInput{Provider: target, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func([]byte) error { return nil }})
			require.Len(t, saved.Attempts, count)
			require.Equal(t, "selected", saved.Attempts[count-1].RequestID)
			if mode == "failure" {
				require.Error(t, err)
				require.Equal(t, "failed", saved.Attempts[0].Outcome)
				return
			}
			if mode == "interrupted" {
				require.Error(t, err)
				require.Contains(t, saved.Aliases, telemetry.RequestAlias{Kind: "response", Value: "resp_bridge", Source: "response.id", AttemptNumber: count, ProviderID: 7})
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, count, result.RequestAttempt.Number)
			require.Equal(t, int64(count), result.Timings.Values["attempt_number"])
			input := gatewayprovider.CaptureOpenAI(ctx, &gatewayprovider.OpenAICapture{Result: result, Provider: target.View()})
			require.Nil(t, input.Result.UpstreamRequestID)
			require.Equal(t, "completed", saved.Attempts[count-1].Outcome)
			if mode == "retry" {
				require.Equal(t, "failed", saved.Attempts[0].Outcome)
			}
			require.Contains(t, saved.Aliases, telemetry.RequestAlias{Kind: "response", Value: "resp_bridge", Source: "response.id", AttemptNumber: count, ProviderID: 7})
			for _, alias := range saved.Aliases {
				require.NotEqual(t, "unselected", alias.Value)
			}
		})
	}
}

// Read delays the first fixture read before delegating to its stored reader.
func (r *bridgeRetryDelayBody) Read(p []byte) (int, error) {
	if !r.delayed {
		r.delayed = true
		time.Sleep(50 * time.Millisecond)
		r.released = time.Now()
	}
	return r.reader.Read(p)
}

// TestHTTPBridgeRetryTimingBelongsToAcceptedDispatch retries a rejected field
// after a measurable first-attempt delay. The performance labeled with the second
// dispatch must start at that dispatch rather than include its rejected predecessor.
func TestHTTPBridgeRetryTimingBelongsToAcceptedDispatch(t *testing.T) {
	var saved telemetry.RequestRecord
	origin := time.Now()
	ctx := context.WithValue(t.Context(), telemetry.RequestID, "turn")
	ctx = telemetry.WithRequestCapture(ctx, telemetry.RequestRecord{RequestID: "turn", StartedAt: origin}, func(r telemetry.RequestRecord) { saved = r })
	ctx = telemetry.WithRequestProvider(ctx, 7, "X-Custom")
	target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 7, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1}}
	delayed := &bridgeRetryDelayBody{reader: strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"truncation","message":"Unsupported parameter: truncation"}}`)}
	transport := &bridgeDiagnosticTransport{pool: httpclient.NewUpstreamPool(), responses: []*http.Response{
		{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(delayed)},
		{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))},
	}}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}, transport: transport})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(ctx, c, wsHTTPBridgeTurnInput{Provider: target, Token: "sk-test", Payload: payload, PayloadBytes: len(payload), OriginalModel: "gpt-5", Turn: 1, WriteClient: func([]byte) error { return nil }})
	require.NoError(t, err)
	require.Len(t, saved.Attempts, 2)
	require.Equal(t, int64(2), result.Timings.Values["attempt_number"])
	secondStart := saved.Attempts[1].StartedAt.Sub(origin).Milliseconds()
	t.Logf("accepted attempt starts at %d ms; attributed performance=%+v", secondStart, result.Timings.Values)
	require.GreaterOrEqual(t, result.Timings.Values["upstream_started_ms"], delayed.released.Sub(origin).Milliseconds())
	require.GreaterOrEqual(t, result.Duration.Milliseconds()-int64(*result.Timings.DurationMs()), int64(49))
}
