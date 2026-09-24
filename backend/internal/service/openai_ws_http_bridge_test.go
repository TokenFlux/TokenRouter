package service

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

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveOpenAIWSClientFirstMessageTimeout(t *testing.T) {
	defaultTimeout := time.Duration(config.DefaultOpenAIWSClientFirstMessageTimeoutSeconds) * time.Second
	require.Equal(t, defaultTimeout, ResolveOpenAIWSClientFirstMessageTimeout(nil))

	cfg := &config.Config{}
	require.Equal(t, defaultTimeout, ResolveOpenAIWSClientFirstMessageTimeout(cfg))

	cfg.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds = 120
	require.Equal(t, 120*time.Second, ResolveOpenAIWSClientFirstMessageTimeout(cfg))
}

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

func TestPrepareOpenAIWSHTTPBridgeBodyStripsNoneReasoningForCompatibleEndpoint(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"company-coding-model","reasoning":{"effort":"none"},"input":"hi"}`)
	compatible := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://compat.example/v1",
	}}}

	body, err := prepareOpenAIWSHTTPBridgeBody(compatible, payload)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "reasoning.effort").Exists())
	require.False(t, gjson.GetBytes(body, "reasoning").Exists())

	officialBody, err := prepareOpenAIWSHTTPBridgeBody(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}, payload)
	require.NoError(t, err)
	require.Equal(t, "none", gjson.GetBytes(officialBody, "reasoning.effort").String())
}

func TestProxyOpenAIWSHTTPBridgeTurn_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {

	// proxyOpenAIWSHTTPBridgeTurn 是 client WS→HTTP bridge，本身不 canonicalize
	// fast→priority；生产入口的归一化在 openai_ws_forwarder_ingress.go 的 fast
	// policy. This test covers the local observer while preserving the canonical
	// outbound priority tier independently.
	sse := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_tier","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5881, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","stream":true,"service_tier":"priority","input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", payload, len(payload),
		"gpt-5.5", "", "", "", "", 1,
		func([]byte) error { return nil },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String())
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
}

func TestProxyOpenAIWSHTTPBridgeTurn_NormalizesFastWithoutLosingObservedDefault(t *testing.T) {

	// The client alias fast is canonicalized to priority while the local observer
	// independently captures the upstream default declaration.
	sse := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_tier2","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5882, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","stream":true,"service_tier":"fast","input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", payload, len(payload),
		"gpt-5.5", "", "", "", "", 1,
		func([]byte) error { return nil },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
}

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
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5659, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
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

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", payload, len(payload),
		"gpt-5", "", "", "", "", "", 1,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

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

func TestProxyOpenAIWSHTTPBridgeTurnAPIKeyRestoresClientToolsInResponseDone(t *testing.T) {

	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","status":"in_progress"}}`,
		``,
		`data: {"type":"response.function_call_arguments.done","sequence_number":1,"output_index":0,"item_id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}"}`,
		``,
		`data: {"type":"response.done","sequence_number":2,"response":{"id":"resp_tools","status":"completed","output":[{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5764, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	payload := []byte(`{
		"type":"response.create","model":"gpt-5","stream":true,
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}],
		"input":"run pwd"
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", payload, len(payload),
		"gpt-5", "", "", "", "", "", 1,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

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
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5765, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}},
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

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"grok-4.5", "", "", "", "", "", 1,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

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

func TestProxyOpenAIWSHTTPBridgeTurnGrokInheritsToolSearchAndPromotesFollowupDiscovery(t *testing.T) {

	firstSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_first\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	secondSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_second\",\"output\":[{\"type\":\"function_call\",\"id\":\"item_spawn\",\"call_id\":\"call_spawn\",\"name\":\"multi_agent_v1__spawn_agent\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(firstSSE))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(secondSSE))},
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5766, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL, "subscription_tier": "free"}},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	first := []byte(`{"type":"response.create","model":"grok-4.5","stream":true,"tools":[{"type":"tool_search"}],"input":"discover tools"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", first, len(first),
		"grok-4.5", "", "", "", "", "grok-ws-cache", 1, func([]byte) error { return nil },
	)
	require.NoError(t, err)

	second := []byte(`{
		"type":"response.create","model":"grok-4.5","stream":true,
		"input":[{"type":"tool_search_output","call_id":"call_search","status":"completed","tools":[
			{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
		]}]
	}`)
	var events [][]byte
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", second, len(second),
		"grok-4.5", "", "", "", "", "grok-ws-cache", 2,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "tool_search", gjson.GetBytes(upstream.bodies[1], "tools.0.name").String())
	require.Equal(t, "multi_agent_v1__spawn_agent", gjson.GetBytes(upstream.bodies[1], "tools.1.name").String())
	require.NotEqual(t, grokFreeCacheDisabledToolChoice, gjson.GetBytes(upstream.bodies[1], "tool_choice").String())
	require.Equal(t, "grok-ws-cache", gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.Len(t, events, 1)
	require.Equal(t, "spawn_agent", gjson.GetBytes(events[0], "response.output.0.name").String())
	require.Equal(t, "multi_agent_v1", gjson.GetBytes(events[0], "response.output.0.namespace").String())
}

func TestOpenAIWSHTTPBridgeAPIKeyReusesClientToolMappingWhenFollowupOmitsTools(t *testing.T) {

	response := func(id string) *http.Response {
		body := `data: {"type":"response.completed","response":{"id":"` + id + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		response("resp_custom_first"),
		response("resp_custom_second"),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5822, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	write := func([]byte) error { return nil }

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.6","stream":true,"tools":[{"type":"custom","name":"exec"}],"input":"run pwd"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", firstPayload, len(firstPayload),
		"gpt-5.6", "", "", "", "", "", 1, write,
	)
	require.NoError(t, err)

	secondPayload := []byte(`{"type":"response.create","model":"gpt-5.6","stream":true,"previous_response_id":"resp_custom_first","input":[{"type":"custom_tool_call_output","id":"ctco_client_output_1","call_id":"call_custom_1","output":"ok"}]}`)
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", secondPayload, len(secondPayload),
		"gpt-5.6", "", "", "", "", "", 2, write,
	)
	require.NoError(t, err)

	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "function", gjson.GetBytes(upstream.bodies[0], "tools.0.type").String())
	require.Equal(t, "exec", gjson.GetBytes(upstream.bodies[0], "tools.0.name").String())
	require.Equal(t, "function", gjson.GetBytes(upstream.bodies[1], "tools.0.type").String())
	require.Equal(t, "exec", gjson.GetBytes(upstream.bodies[1], "tools.0.name").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.id").Exists())
}

func TestOpenAIWSHTTPBridgeFullCustomToolHistoryWithoutPreviousResponseIDDoesNotReplay(t *testing.T) {

	completed := func(responseID string, output string) string {
		return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + responseID + "\",\"model\":\"gpt-5.1\",\"output\":" + output + ",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_1", `[{"type":"custom_tool_call","id":"item_1","call_id":"call_1","name":"exec","input":"pwd"}]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_2", `[]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_3", `[]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_4", `[]`)))},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9002, Name: "oauth-full-context", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"responses_websockets_v2_enabled": true},
		Concurrency: 1, Status: billing.StatusActive, Schedulable: true},
	}

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
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "test-token", firstMessage, nil)
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

func TestOpenAIWSHTTPBridgeObjectToolOutputWithoutPreviousResponseIDReplaysMatchingCall(t *testing.T) {

	completed := func(responseID string, output string) string {
		return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + responseID + "\",\"model\":\"gpt-5.1\",\"output\":" + output + ",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_1", `[{"type":"custom_tool_call","id":"item_1","call_id":"call_1","name":"exec","input":"pwd"}]`)))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed("resp_2", `[]`)))},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9003, Name: "oauth-output-only", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"responses_websockets_v2_enabled": true},
		Concurrency: 1, Status: billing.StatusActive, Schedulable: true},
	}

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
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "test-token", firstMessage, nil)
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

func TestOpenAIWSHTTPBridgeDecisionKeepsSmallFramesOnWS(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					HTTPBridgeEnabled:        true,
					HTTPBridgeThresholdBytes: 100,
				},
			},
		},
	})

	require.False(t, svc.shouldBridgeOpenAIWSHTTP(nil, 99, ""))
	require.True(t, svc.shouldBridgeOpenAIWSHTTP(nil, 100, ""))
	require.False(t, svc.shouldBridgeOpenAIWSHTTP(nil, 1000, "resp_existing"))

	svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false
	require.False(t, svc.shouldBridgeOpenAIWSHTTP(nil, 1000, ""))
	require.True(t, svc.shouldBridgeOpenAIWSHTTP(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok}}, 1, "resp_existing"))
}

func TestOpenAIWSPassthroughFirstMessageBridgeDecision(t *testing.T) {
	const threshold = 100
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAIWS: config.GatewayOpenAIWSConfig{
			HTTPBridgeEnabled:        true,
			HTTPBridgeThresholdBytes: threshold,
		},
	}}})
	exactThresholdPayload := `{"type":"response.create","input":"` +
		strings.Repeat("x", threshold-len(`{"type":"response.create","input":""}`)) + `"}`

	tests := []struct {
		name    string
		payload string
		want    bool
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

func TestProxyOpenAIWSHTTPBridgeTurnTransportErrorFailoverSafety(t *testing.T) {

	tests := []struct {
		name         string
		turn         int
		wantFailover bool
		wantWrites   int
	}{
		{name: "first_turn_fails_over_before_downstream_event", turn: 1, wantFailover: true},
		{name: "later_turn_does_not_replay_completed_turns", turn: 2, wantWrites: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{err: io.EOF}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{
				cfg:          &config.Config{},
				httpUpstream: upstream,
			})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
				Name:        "api-key",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "gpt-5", "", "", "", "", tt.turn,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			if tt.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
				require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))
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

func TestProxyOpenAIWSHTTPBridgeTurnHTTPStatusFailoverSafety(t *testing.T) {

	tests := []struct {
		name         string
		turn         int
		status       int
		wantFailover bool
		wantWrites   int
	}{
		{name: "first_turn_401", turn: 1, status: http.StatusUnauthorized, wantFailover: true},
		{name: "first_turn_429", turn: 1, status: http.StatusTooManyRequests, wantFailover: true},
		{name: "first_turn_500", turn: 1, status: http.StatusInternalServerError, wantFailover: true},
		{name: "later_turn_500_does_not_replay", turn: 2, status: http.StatusInternalServerError, wantWrites: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: tt.status,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"server_error","message":"temporary upstream failure"}}`)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "gpt-5", "", "", "", "", tt.turn,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)

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
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 91, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "sk-test", payload, len(payload),
		"gpt-5", "", "", "", "", 1,
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, upstream.callCount)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "truncation").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "truncation").Exists())
	require.Len(t, writes, 1)
	require.Equal(t, "response.completed", gjson.GetBytes(writes[0], "type").String())
}

func TestProxyOpenAIWSHTTPBridgeTurnSSEErrorFailoverSafety(t *testing.T) {

	for _, turn := range []int{1, 2} {
		t.Run(fmt.Sprintf("turn_%d", turn), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"limited\"}}\n\n",
				)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 10, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "gpt-5", "", "", "", "", turn,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)

			var failoverErr *forwardcore.UpstreamFailoverError
			require.Nil(t, result)
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Empty(t, writes)
		})
	}
}

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

func TestProxyOpenAIWSHTTPBridgeTurnLaterTurn429FailsOverBeforeClientWrite(t *testing.T) {

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"60"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5845, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_old","input":[{"role":"user","content":"continue"}]}`)
	writes := 0

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"gpt-5.6-sol", "", "", "", "", "", 281,
		func([]byte) error {
			writes++
			return nil
		},
	)

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, writes)
}

func TestOpenAIWSHTTPBridgeLaterTurn429CarriesCurrentTurnReplayPayload(t *testing.T) {

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":          []string{"text/event-stream"},
				openAIWSTurnStateHeader: []string{"old-account-state"},
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
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          cfg,
		httpUpstream: upstream,
		cache:        &stubGatewayCache{},

		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5845, Name: "limited", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Extra: map[string]any{"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModeHTTPBridge}},
	}
	nextAccount := *account
	nextAccount.Record.ID = 5846
	nextAccount.Record.Name = "replacement"

	serverErrCh := make(chan error, 1)
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
		proxyErr := svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "access-token-a", firstMessage, nil)
		var failoverErr *forwardcore.UpstreamFailoverError
		if !errors.As(proxyErr, &failoverErr) {
			serverErrCh <- proxyErr
			return
		}
		retryPayload, retryCurrentTurn := ws.CurrentTurnRetryPayload(proxyErr)
		if !retryCurrentTurn || len(retryPayload) == 0 {
			serverErrCh <- errors.New("missing current-turn retry payload")
			return
		}
		retryPayloadCh <- retryPayload
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(
			r.Context(), ginCtx, conn, &nextAccount, "access-token-b", retryPayload, nil,
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
		t.Fatal("timed out waiting for replacement-account completion")
	}
	require.Len(t, upstream.bodies, 3)
	require.Contains(t, string(upstream.bodies[0]), "first")
	require.NotContains(t, string(upstream.bodies[2]), "previous_response_id")
	require.Contains(t, string(upstream.bodies[2]), "second")
}

// 桥接转发 error / response.failed 给 WS 客户端前必须把容量降载码改写为可重试
// 的 server_error：Codex 对 server_is_overloaded/slow_down 判致命并终止会话。
// 账号状态判定使用改写前的原始事件，不受影响。
func TestProxyOpenAIWSHTTPBridgeTurnRewritesCapacityShedCodeForClient(t *testing.T) {

	tests := []struct {
		name    string
		turn    int
		body    string
		wantErr bool
	}{
		{
			name:    "turn2_error_frame",
			turn:    2,
			body:    "data: {\"type\":\"error\",\"error\":{\"type\":\"service_unavailable_error\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}\n\n",
			wantErr: true,
		},
		{
			// 后续 turn 不允许 replay，容量错误必须改写后交给客户端重试。
			name: "turn2_bare_response_failed",
			turn: 2,
			body: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_shed\",\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			_, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "", "", "", "", "", tt.turn,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)

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

func TestProxyOpenAIWSHTTPBridgeTurnBareErrorUsesAuthoritativeFailed(t *testing.T) {

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","response_id":"resp_failed","delta":"partial"}`,
		``,
		`data: {"type":"error","error":{"status_code":403,"code":"workspace_suspended","message":"workspace is suspended"}}`,
		``,
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","usage":{"input_tokens":9,"output_tokens":2},"error":{"status_code":403,"code":"workspace_suspended","message":"workspace is suspended"}}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	repo := &openAIStream403AccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 111, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "gpt-5", "", "", "", "", 2, func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Len(t, writes, 2)
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "response.failed", gjson.GetBytes(writes[1], "type").String())
}

func TestProxyOpenAIWSHTTPBridgeTurnBareErrorEOFSynthesizesFailed(t *testing.T) {

	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_eof\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 112, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "gpt-5", "", "", "", "", 2, func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	})

	require.EqualError(t, err, "bad request")
	require.NotNil(t, result)
	require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
	require.Len(t, writes, 2)
	require.Equal(t, "response.failed", gjson.GetBytes(writes[1], "type").String())
	require.Equal(t, "failed", gjson.GetBytes(writes[1], "response.status").String())
	require.Equal(t, "resp_eof", gjson.GetBytes(writes[1], "response.id").String())
}

func TestProxyOpenAIWSHTTPBridgeTurnBareErrorFollowedByCompletedUsesCompleted(t *testing.T) {

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_recovered","status":"in_progress"}}`,
		``,
		`data: {"type":"error","error":{"code":"transient","message":"retrying"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_recovered","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":4}}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 113, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "gpt-5", "", "", "", "", 2, func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Len(t, writes, 2)
	require.Equal(t, "response.created", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(writes[1], "type").String())
}

func TestProxyOpenAIWSHTTPBridgeTurnStagesMetadataBeforeCapacityFailover(t *testing.T) {

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_shed"}}`,
		"",
		`data: {"type":"response.in_progress","response":{"id":"resp_shed"}}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_shed","status":"failed","error":{"message":"Our servers are currently overloaded. Please try again later."}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-ws-bridge-capacity"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "sk-test", payload, len(payload),
		"gpt-5", "", "", "", "", "", 1,
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.Empty(t, writes)
}

func TestProxyOpenAIWSHTTPBridgeTurnDoesNotReplayCapacityAfterSemanticOutput(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_partial"}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_partial","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-ws-bridge-post-output"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Concurrency: 1}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var writes [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "sk-test", payload, len(payload),
		"gpt-5", "", "", "", "", "", 1,
		func(message []byte) error {
			writes = append(writes, append([]byte(nil), message...))
			return nil
		},
	)

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

func TestProxyOpenAIWSHTTPBridgeTurnRequiresTerminalEvent(t *testing.T) {

	tests := []struct {
		name         string
		body         string
		wantFailover bool
		wantWrites   int
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
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "gpt-5", "", "", "", "", 1,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)

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

func TestOpenAIWSHTTPBridgeRelaysSSEFramesAsWebSocketMessages(t *testing.T) {

	sseBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_bridge","model":"gpt-5"}}`,
		"",
		`data: {"type":"response.output_text.delta","response":{"id":"resp_bridge"},"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_bridge","model":"gpt-5","usage":{"input_tokens":3,"output_tokens":2}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid_bridge"},
		},
		Body: io.NopCloser(strings.NewReader(sseBody)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				MaxLineSize: defaultMaxLineSize,
				OpenAIWS: config.GatewayOpenAIWSConfig{
					HTTPBridgeEnabled:        true,
					HTTPBridgeThresholdBytes: 1,
				},
			},
		},
		httpUpstream:  upstream,
		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "api-key",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Status:      billing.StatusActive},
	}
	payload := []byte(`{"type":"response.create","generate":true,"model":"gpt-5","stream":true,"parallel_tool_calls":true,"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"},"input":"hi"}`)

	type bridgeResult struct {
		result *forwardcore.OpenAIResult
		err    error
	}
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
		result, bridgeErr := svc.proxyOpenAIWSHTTPBridgeTurn(
			r.Context(),
			ginCtx,
			account,
			"sk-test",
			payload,
			len(payload),
			"gpt-5",
			"gpt-5",
			"",
			"",
			"",
			"",
			1,
			writeClient,
		)
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

func TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultsEmptyModelTo45(t *testing.T) {

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_default","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_default","model":"grok-4.5","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 72,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}},
	}
	payload := []byte(`{"type":"response.create","generate":true,"stream":true,"input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"", "", "", "", "", "", 1,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Empty(t, result.BillingModel)
	require.Equal(t, xai.DefaultResponsesModel, result.UpstreamModel)
	require.Len(t, events, 2)
}

// TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultModelErrorUsesCanonicalKey 验证 WS 空模型旁路
// 在错误策略中复用实际发送给 xAI 的默认模型。
func TestProxyOpenAIWSHTTPBridgeTurnForGrokDefaultModelErrorUsesCanonicalKey(t *testing.T) {

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"model_not_found","message":"model not found"}}`)),
	}}
	repo := &grokModelStateAccountRepo{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil),

		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 73,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}},
	}
	payload := []byte(`{"type":"response.create","generate":true,"stream":true,"input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"", "", "", "", "", "", 1,
		func([]byte) error { return nil },
	)

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, xai.DefaultResponsesModel, repo.modelRateLimitCalls[0].scope)
}

// TestProxyOpenAIWSHTTPBridgeTurnForGrokFreeFunctionToolsUsesMixedRoute 验证 Grok WS HTTP bridge
// 与原生 Responses 使用相同的 Free 函数工具缓存路由。
func TestProxyOpenAIWSHTTPBridgeTurnForGrokFreeFunctionToolsUsesMixedRoute(t *testing.T) {

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_tools","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_tools","model":"grok-4.5","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 73,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"base_url":          xai.DefaultCLIBaseURL,
			"subscription_tier": "free",
		}},
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

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"", "", "", "", "", "cache-id", 1,
		func([]byte) error { return nil },
	)

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

func TestProxyOpenAIWSHTTPBridgeTurnPromotesCodexAdditionalToolsForMixedCache(t *testing.T) {

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_grok_codex_lite","model":"grok-4.5"}}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_grok_codex_lite","model":"grok-4.5","usage":{"input_tokens":4,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 73,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"base_url":          xai.DefaultCLIBaseURL,
			"subscription_tier": "free",
		}},
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
	c.Request.Header.Set(grokClientToolCacheOptInHeader, "prefer-cache")
	var events [][]byte

	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "access-token", payload, len(payload),
		"grok", "grok", "", "", "", "isolated-ws-cache-id", 1,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

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
	require.Empty(t, upstream.lastReq.Header.Get(grokClientToolCacheOptInHeader))
}

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
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		bridgeResponse("resp_grok_ws_1", "xai-ws-req-1", 0),
		bridgeResponse("resp_grok_ws_2", "xai-ws-req-2", 3),
		bridgeResponse("resp_grok_ws_3", "xai-ws-req-3", 0),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				MaxLineSize: defaultMaxLineSize,
			},
		},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 71,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Status:      billing.StatusActive,
		Credentials: map[string]any{
			"base_url": xai.DefaultCLIBaseURL,
		}},
	}

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

		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "access-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok","stream":true,"input":"hi","prompt_cache_retention":"24h"}`))
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
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok","stream":true,"previous_response_id":"resp_grok_ws_1","input":"second turn"}`))
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
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","generate":true,"model":"grok-4.3","stream":true,"previous_response_id":"resp_grok_ws_2","input":"third turn with a different model"}`))
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
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.bodies[1], "model").String())
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

func TestOpenAIWSHTTPBridgeAcceptsFirstFrameAboveLegacy16MiB(t *testing.T) {

	sseBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_large_bridge","model":"gpt-5"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_large_bridge","model":"gpt-5","usage":{"input_tokens":9,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid_large_bridge"},
		},
		Body: io.NopCloser(strings.NewReader(sseBody)),
	}}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				Enabled:                  true,
				APIKeyEnabled:            true,
				ResponsesWebsocketsV2:    true,
				ModeRouterV2Enabled:      true,
				IngressModeDefault:       accountcore.OpenAIWSIngressModeCtxPool,
				ClientReadLimitBytes:     64 * 1024 * 1024,
				HTTPBridgeEnabled:        true,
				HTTPBridgeThresholdBytes: 17*1024*1024 + 512,
			},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:           cfg,
		httpUpstream:  upstream,
		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9,
		Name:     "api-key",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":        "sk-upstream",
			"base_url":       "https://env-openai.example/v1",
			"model_provider": "env-openai",
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    accountcore.OpenAIWSIngressModePassthrough,
		},
		Concurrency: 1,
		Status:      billing.StatusActive},
	}

	payload := []byte(strings.Repeat(" ", 1024) + `{"type":"response.create","generate":true,"model":"gpt-5","stream":true,"input":"` + strings.Repeat("x", 17*1024*1024) + `"}`)
	require.Greater(t, len(payload), 16*1024*1024)
	require.GreaterOrEqual(t, int64(len(payload)), cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes)
	require.Less(t, int64(len(payload)), ResolveOpenAIWSClientReadLimitBytes(cfg))

	type turnOutcome struct {
		turn   int
		result *forwardcore.OpenAIResult
		err    error
	}
	turnOutcomeCh := make(chan turnOutcome, 1)
	hooks := &ws.OpenAIIngressHooks{
		AfterTurn: func(capture ws.OpenAITurnCapture) {
			turnOutcomeCh <- turnOutcome{turn: capture.Turn, result: capture.Result, err: capture.Err}
		},
	}

	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		conn.SetReadLimit(ResolveOpenAIWSClientReadLimitBytes(cfg))

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
		errCh <- svc.ProxyResponsesWebSocketFromClient(proxyCtx, ginCtx, conn, account, "sk-test", firstMessage, hooks)
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
	require.Equal(t, "env-openai", account.View().GetCredential("model_provider"))
	require.Less(t, int64(len(upstream.lastBody)), cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes)
	require.Greater(t, len(upstream.lastBody), 16*1024*1024)
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "gpt-5", gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestOpenAIWSHTTPBridgeKeepsContinuationFramesOnHTTPWithoutPreviousResponseID(t *testing.T) {

	firstSSEBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_bridge_first","model":"gpt-5.1","output":[{"type":"function_call","id":"fc_bridge_1","call_id":"call_bridge_1","name":"shell","arguments":"{}"}],"usage":{"input_tokens":9,"output_tokens":1}}}`,
		"",
	}, "\n")
	secondSSEBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_bridge_second","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
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
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	captureConn := &openAIWSCaptureConn{}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(captureDialer)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          cfg,
		httpUpstream: upstream,
		cache:        &stubGatewayCache{},

		toolCorrector: openai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 19,
		Name:        "api-key-bridge-handoff",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-upstream"},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		},
		Concurrency: 1,
		Status:      billing.StatusActive,
		Schedulable: true},
	}

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

		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
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

func TestOpenAIWSHTTPBridge_IdleTimeoutClosesClientSession(t *testing.T) {

	sseBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_bridge_idle","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sseBody)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          cfg,
		httpUpstream: upstream,
		cache:        &stubGatewayCache{},

		toolCorrector: openai.NewCodexToolCorrector(),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 20,
		Name:        "api-key-bridge-idle-timeout",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-upstream"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
		Concurrency: 1,
		Status:      billing.StatusActive,
		Schedulable: true},
	}

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
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
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
