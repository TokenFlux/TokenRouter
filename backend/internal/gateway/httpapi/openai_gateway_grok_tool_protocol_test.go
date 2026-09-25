//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	groktestkit "github.com/TokenFlux/TokenRouter/internal/upstream/grok/testkit"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClearGrokResponsesClientToolMappingRemovesStaleContextState(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	SetGrokResponsesClientToolMapping(c, bridge.ResponsesClientToolMapping{
		CustomTools: map[string]bool{"stale_tool": true},
	})

	_, seeded := GrokResponsesClientToolMapping(c)
	require.True(t, seeded)
	ClearGrokResponsesClientToolMapping(c)
	_, remains := GrokResponsesClientToolMapping(c)
	require.False(t, remains)
}

func TestForwardGrokResponsesClientToolNameConflictReturns400(t *testing.T) {

	body := []byte(`{
		"model":"grok","stream":false,"input":"hello",
		"tools":[
			{"type":"custom","name":"duplicate"},
			{"type":"function","name":"duplicate","parameters":{"type":"object"}}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &auxiliaryHTTPRecorder{}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	account := grokProtocolAPIKeyAccount(7101)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "tools", gjson.Get(recorder.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "conflicts")
	require.Empty(t, upstream.requests, "an ambiguous request must not reach xAI")
}

func TestForwardGrokResponsesMalformedToolSearchOutputReturns400BeforeUpstream(t *testing.T) {

	body := []byte(`{
		"model":"grok","stream":false,
		"tools":[{"type":"tool_search"}],
		"input":[{"type":"tool_search_output","status":"completed"}]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &auxiliaryHTTPRecorder{}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	account := grokProtocolAPIKeyAccount(7103)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "tools", gjson.Get(recorder.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "call_id")
	require.Empty(t, upstream.requests, "malformed lowered output must not reach xAI")
}

func TestForwardGrokResponsesOAuthRestoresClientToolsNonStreaming(t *testing.T) {

	body := groktestkit.ClientToolsRequest(false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &apikey.APIKey{ID: 7102})

	account := grokProtocolOAuthAccount(7102)
	repo := &grokQuotaAccountRepo{grokFixtureAccounts: &grokFixtureAccounts{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"protocol-oauth"},
		},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_protocol_oauth","object":"response","model":"grok-4.5","status":"completed",
			"output":[
				{"type":"function_call","id":"item_custom","call_id":"call_custom","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}","namespace":"must_not_leak"},
				{"type":"function_call","id":"item_search","call_id":"call_search","name":"tool_search","arguments":"{\"query\":\"github\"}"},
				{"type":"function_call","id":"item_namespace","call_id":"call_namespace","name":"collaboration__send_message","arguments":"{\"target\":\"root\"}"}
			],
			"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}
		}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_protocol_oauth", result.ResponseID)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer oauth-protocol-token", upstream.lastReq.Header.Get("Authorization"))
	assertGrokProtocolRequestLowered(t, upstream.lastBody)

	response := recorder.Body.Bytes()
	require.Equal(t, "custom_tool_call", gjson.GetBytes(response, "output.0.type").String())
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(response, "output.0.input").String())
	require.False(t, gjson.GetBytes(response, "output.0.arguments").Exists())
	require.False(t, gjson.GetBytes(response, "output.0.namespace").Exists())
	require.Equal(t, "tool_search_call", gjson.GetBytes(response, "output.1.type").String())
	require.Equal(t, "client", gjson.GetBytes(response, "output.1.execution").String())
	require.Equal(t, "github", gjson.GetBytes(response, "output.1.arguments.query").String())
	require.False(t, gjson.GetBytes(response, "output.1.name").Exists())
	require.Equal(t, "function_call", gjson.GetBytes(response, "output.2.type").String())
	require.Equal(t, "collaboration", gjson.GetBytes(response, "output.2.namespace").String())
	require.Equal(t, "send_message", gjson.GetBytes(response, "output.2.name").String())
}

func TestForwardGrokResponsesAPIKeyRestoresClientToolsFromSSEForNonStreamingRequest(t *testing.T) {

	body := groktestkit.ClientToolsRequest(false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/event-stream"},
			"Xai-Request-Id": []string{"protocol-api-key-sse-nonstream"},
		},
		Body: io.NopCloser(strings.NewReader(grokProtocolUpstreamSSE())),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	account := grokProtocolAPIKeyAccount(7104)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_protocol_stream", result.ResponseID)
	assertGrokProtocolRequestLowered(t, upstream.lastBody)

	response := recorder.Body.Bytes()
	require.True(t, json.Valid(response))
	require.Equal(t, "custom_tool_call", gjson.GetBytes(response, "output.0.type").String())
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(response, "output.0.input").String())
	require.Equal(t, "tool_search_call", gjson.GetBytes(response, "output.1.type").String())
	require.Equal(t, "client", gjson.GetBytes(response, "output.1.execution").String())
	require.Equal(t, "collaboration", gjson.GetBytes(response, "output.2.namespace").String())
	require.Equal(t, "send_message", gjson.GetBytes(response, "output.2.name").String())
}

func TestForwardGrokResponsesAPIKeyRestoresClientToolsStreaming(t *testing.T) {

	body := groktestkit.ClientToolsRequest(true)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/event-stream"},
			"Xai-Request-Id": []string{"protocol-api-key"},
		},
		Body: io.NopCloser(strings.NewReader(grokProtocolUpstreamSSE())),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	account := grokProtocolAPIKeyAccount(7103)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", true, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, "resp_protocol_stream", result.ResponseID)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer xai-protocol-key", upstream.lastReq.Header.Get("Authorization"))
	assertGrokProtocolRequestLowered(t, upstream.lastBody)

	frames := parseGrokProtocolSSEFrames(t, recorder.Body.String())
	require.NotEmpty(t, frames)
	for index, frame := range frames {
		require.Equal(t, frame.event, gjson.GetBytes(frame.data, "type").String(), "SSE event field must follow the restored data.type")
		require.Equal(t, 40+index, int(gjson.GetBytes(frame.data, "sequence_number").Int()), "sequence_number must be continuous after suppressed and expanded events")
	}

	created := requireGrokProtocolFrame(t, frames, "response.created", "", "")
	require.True(t, gjson.GetBytes(created.data, "upstream_extension.preserved").Bool())
	customAdded := requireGrokProtocolFrame(t, frames, "response.output_item.added", "item.type", "custom_tool_call")
	require.Equal(t, "apply_patch", gjson.GetBytes(customAdded.data, "item.name").String())
	customInputDelta := requireGrokProtocolFrame(t, frames, "response.custom_tool_call_input.delta", "", "")
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(customInputDelta.data, "delta").String())
	customInputDone := requireGrokProtocolFrame(t, frames, "response.custom_tool_call_input.done", "", "")
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(customInputDone.data, "input").String())
	customDone := requireGrokProtocolFrame(t, frames, "response.output_item.done", "item.type", "custom_tool_call")
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(customDone.data, "item.input").String())

	namespaceAdded := requireGrokProtocolFrame(t, frames, "response.output_item.added", "item.namespace", "collaboration")
	require.Equal(t, "send_message", gjson.GetBytes(namespaceAdded.data, "item.name").String())
	namespaceDone := requireGrokProtocolFrame(t, frames, "response.output_item.done", "item.namespace", "collaboration")
	require.Equal(t, "send_message", gjson.GetBytes(namespaceDone.data, "item.name").String())
	namespaceArgumentsDone := requireGrokProtocolFrame(t, frames, "response.function_call_arguments.done", "name", "send_message")
	require.Equal(t, "response.function_call_arguments.done", gjson.GetBytes(namespaceArgumentsDone.data, "type").String())
	require.False(t, gjson.GetBytes(namespaceArgumentsDone.data, "namespace").Exists())

	searchAdded := requireGrokProtocolFrame(t, frames, "response.output_item.added", "item.type", "tool_search_call")
	require.Equal(t, "client", gjson.GetBytes(searchAdded.data, "item.execution").String())
	searchDone := requireGrokProtocolFrame(t, frames, "response.output_item.done", "item.type", "tool_search_call")
	require.Equal(t, "github", gjson.GetBytes(searchDone.data, "item.arguments.query").String())

	for _, frame := range frames {
		itemID := gjson.GetBytes(frame.data, "item_id").String()
		if itemID == "item_custom" || itemID == "item_search" {
			require.NotContains(t, frame.event, "function_call_arguments", "client-only proxy argument events must not leak")
		}
	}
	completed := requireGrokProtocolFrame(t, frames, "response.completed", "", "")
	require.Equal(t, "custom_tool_call", gjson.GetBytes(completed.data, "response.output.0.type").String())
	require.Equal(t, "tool_search_call", gjson.GetBytes(completed.data, "response.output.1.type").String())
	require.Equal(t, "collaboration", gjson.GetBytes(completed.data, "response.output.2.namespace").String())
}

type grokProtocolSSEFrame struct {
	event string
	data  []byte
}

func grokProtocolOAuthAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Name: "grok-oauth-protocol", Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-protocol-token", "refresh_token": "refresh-token",
			"expires_at": time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
			"base_url":   grok.DefaultCLIBaseURL, "subscription_tier": "supergrok",
		}},
	}
}

func grokProtocolAPIKeyAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Name: "grok-api-key-protocol", Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "xai-protocol-key", "base_url": "https://api.x.ai/v1"}},
	}
}

func assertGrokProtocolRequestLowered(t *testing.T, body []byte) {
	t.Helper()
	require.True(t, json.Valid(body))
	require.False(t, gjson.GetBytes(body, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(body, `tools.#(type=="namespace")`).Exists())
	require.False(t, gjson.GetBytes(body, `tools.#(type=="tool_search")`).Exists())
	require.True(t, gjson.GetBytes(body, `tools.#(name=="apply_patch")`).Exists())
	require.True(t, gjson.GetBytes(body, `tools.#(name=="tool_search")`).Exists())
	require.True(t, gjson.GetBytes(body, `tools.#(name=="collaboration__send_message")`).Exists())
	require.Equal(t, "function", gjson.GetBytes(body, "tool_choice.type").String())
	require.Equal(t, "apply_patch", gjson.GetBytes(body, "tool_choice.name").String())
	require.Equal(t, "function_call", gjson.GetBytes(body, "input.0.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.1.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(body, "input.2.type").String())
	require.Equal(t, "tool_search", gjson.GetBytes(body, "input.2.name").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.3.type").String())
	require.Equal(t, "collaboration__send_message", gjson.GetBytes(body, "input.4.name").String())
	require.False(t, gjson.GetBytes(body, "input.4.namespace").Exists())
}

func grokProtocolUpstreamSSE() string {
	events := []string{
		`{"type":"response.created","sequence_number":40,"response":{"id":"resp_protocol_stream","model":"grok-4.5"},"upstream_extension":{"preserved":true}}`,
		`{"type":"response.output_item.added","sequence_number":41,"output_index":0,"item":{"type":"function_call","id":"item_custom","call_id":"call_custom","name":"apply_patch","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":42,"output_index":0,"item_id":"item_custom","delta":"{\"input\":\"*** Begin"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":43,"output_index":0,"item_id":"item_custom","delta":" Patch\"}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":44,"output_index":0,"item_id":"item_custom","call_id":"call_custom","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}`,
		`{"type":"response.output_item.done","sequence_number":45,"output_index":0,"item":{"type":"function_call","id":"item_custom","call_id":"call_custom","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}","status":"completed"}}`,
		`{"type":"response.output_item.added","sequence_number":46,"output_index":1,"item":{"type":"function_call","id":"item_namespace","call_id":"call_namespace","name":"collaboration__send_message","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.done","sequence_number":47,"output_index":1,"item_id":"item_namespace","call_id":"call_namespace","name":"collaboration__send_message","arguments":"{\"target\":\"root\"}"}`,
		`{"type":"response.output_item.done","sequence_number":48,"output_index":1,"item":{"type":"function_call","id":"item_namespace","call_id":"call_namespace","name":"collaboration__send_message","arguments":"{\"target\":\"root\"}","status":"completed"}}`,
		`{"type":"response.output_item.added","sequence_number":49,"output_index":2,"item":{"type":"function_call","id":"item_search","call_id":"call_search","name":"tool_search","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":50,"output_index":2,"item_id":"item_search","delta":"{\"query\":\"github\"}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":51,"output_index":2,"item_id":"item_search","call_id":"call_search","name":"tool_search","arguments":"{\"query\":\"github\"}"}`,
		`{"type":"response.output_item.done","sequence_number":52,"output_index":2,"item":{"type":"function_call","id":"item_search","call_id":"call_search","name":"tool_search","arguments":"{\"query\":\"github\"}","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":53,"response":{"id":"resp_protocol_stream","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"function_call","id":"item_custom","call_id":"call_custom","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"},{"type":"function_call","id":"item_search","call_id":"call_search","name":"tool_search","arguments":"{\"query\":\"github\"}"},{"type":"function_call","id":"item_namespace","call_id":"call_namespace","name":"collaboration__send_message","arguments":"{\"target\":\"root\"}"}],"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`,
	}
	var out strings.Builder
	for _, event := range events {
		typ := gjson.Get(event, "type").String()
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", typ, event)
	}
	return out.String()
}

func parseGrokProtocolSSEFrames(t *testing.T, body string) []grokProtocolSSEFrame {
	t.Helper()
	var frames []grokProtocolSSEFrame
	event := ""
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if value, ok := openai.ExtractSSEEventLine(line); ok {
			event = strings.TrimSpace(value)
			continue
		}
		data, ok := openai.ExtractSSEDataLine(line)
		if !ok || strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		require.NotEmpty(t, event, "every data frame from this upstream should retain an event field")
		require.JSONEq(t, data, data)
		frames = append(frames, grokProtocolSSEFrame{event: event, data: []byte(data)})
		event = ""
	}
	return frames
}

func requireGrokProtocolFrame(t *testing.T, frames []grokProtocolSSEFrame, eventType, path, value string) grokProtocolSSEFrame {
	t.Helper()
	for _, frame := range frames {
		if frame.event != eventType {
			continue
		}
		if path == "" || gjson.GetBytes(frame.data, path).String() == value {
			return frame
		}
	}
	t.Fatalf("missing SSE frame event=%q %s=%q", eventType, path, value)
	return grokProtocolSSEFrame{}
}
