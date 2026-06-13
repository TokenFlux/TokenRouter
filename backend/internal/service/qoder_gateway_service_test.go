package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type qoderRateLimitRepoStub struct {
	stubOpenAIAccountRepo
	rateLimitedID int64
	resetAt       time.Time
}

func (r *qoderRateLimitRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.resetAt = resetAt
	return nil
}

type qoderRefreshAccountRepoStub struct {
	stubOpenAIAccountRepo
	updatedCredentials map[string]any
	updateCalls        int
}

func (r *qoderRefreshAccountRepoStub) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updatedCredentials = cloneCredentials(credentials)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Credentials = cloneCredentials(credentials)
			return nil
		}
	}
	return nil
}

func TestBuildQoderPayloadFromChatCompletions(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":123,
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"assistant","content":"hi"},
			{"role":"tool","tool_call_id":"call_1","content":"tool output"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "auto", modelKey)
	require.Equal(t, true, payload["stream"])
	require.Equal(t, "personal_standard", payload["aliyun_user_type"])
	require.Equal(t, 123, payload["parameters"].(map[string]any)["max_tokens"])
	require.Equal(t, "auto", payload["model_config"].(map[string]any)["key"])
	require.Equal(t, "hello", payload["chat_context"].(map[string]any)["text"].(map[string]any)["text"])

	messages := payload["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "be terse", messages[0].(map[string]any)["content"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "", messages[1].(map[string]any)["content"])
	userContents := messages[1].(map[string]any)["contents"].([]any)
	require.Equal(t, "hello", userContents[0].(map[string]any)["text"])
	require.Equal(t, "tool", messages[3].(map[string]any)["role"])
	require.Equal(t, "tool output", messages[3].(map[string]any)["content"])
	require.Len(t, payload["tools"].([]any), 1)
}

func TestBuildQoderPayloadFromChatCompletionsPreservesToolHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"run ls"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"ls"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"file.txt"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	assistant := messages[1].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "call_1", toolCall["id"])
	require.Equal(t, "function", toolCall["type"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, function["arguments"].(string))

	tool := messages[2].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "bash", tool["name"])
}

func TestBuildQoderPayloadFromAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"max_tokens":456,
		"system":[{"type":"text","text":"system one"},{"type":"text","text":"system two"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"tool result"}]}]}
		]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "ultimate", modelKey)
	require.Equal(t, 456, payload["parameters"].(map[string]any)["max_tokens"])
	require.Equal(t, "ultimate", payload["model_config"].(map[string]any)["key"])

	messages := payload["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "system one\nsystem two", messages[0].(map[string]any)["content"])
	require.Equal(t, "", messages[1].(map[string]any)["content"])
	userContents := messages[1].(map[string]any)["contents"].([]any)
	require.Equal(t, "hello", userContents[0].(map[string]any)["text"])
	require.Equal(t, "tool", messages[2].(map[string]any)["role"])
	require.Equal(t, "t1", messages[2].(map[string]any)["tool_call_id"])
	require.Equal(t, "tool result", messages[2].(map[string]any)["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesPreservesToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"file.txt"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	assistant := messages[0].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "call_1", toolCall["id"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, function["arguments"].(string))

	tool := messages[1].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "file.txt", tool["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesIgnoresThinkingToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"messages":[
			{"role":"user","content":"inspect files"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"need to inspect","signature":"sig"},
				{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"README.md"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"contents"}
			]}
		],
		"tools":[{"name":"Read","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	require.Len(t, messages, 3)
	assistant := messages[1].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.Equal(t, "", assistant["content"])
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "toolu_1", toolCall["id"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "Read", function["name"])
	require.JSONEq(t, `{"file_path":"README.md"}`, function["arguments"].(string))
	toolResult := messages[2].(map[string]any)
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "toolu_1", toolResult["tool_call_id"])
	require.Equal(t, "contents", toolResult["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesDoesNotInventMissingToolResultID(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"tool_result","content":"file.txt"}]}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	require.Len(t, messages, 1)
	tool := messages[0].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.NotContains(t, tool, "tool_calls")
	require.NotContains(t, tool, "tool_call_id")
}

func TestBuildQoderPayloadFromAnthropicMessagesConvertsTools(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"bash",
			"description":"run command",
			"input_schema":{
				"type":"object",
				"properties":{"cmd":{"type":"string"}},
				"required":["cmd"]
			}
		}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	tools := payload["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	require.NotContains(t, tool, "input_schema")
	function := tool["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.Equal(t, "run command", function["description"])
	parameters := function["parameters"].(map[string]any)
	require.Equal(t, "object", parameters["type"])
	require.Contains(t, parameters, "properties")
}

func TestResolveQoderModelUsesOpus46AliasForUltimate(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	info := resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "system", info.Source)

	legacy := resolveQoderModel("claude-opus-4-5")
	require.Equal(t, "claude-opus-4-5", legacy.Key)

	codex := resolveQoderModel("gpt-5-codex")
	require.Equal(t, "gpt-5-codex", codex.Key)
}

func TestQoderGatewayAppliesAccountModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &Account{
		ID:       88,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
			"model_whitelist": []any{},
		},
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &QoderGatewayService{
		tokenProvider: &QoderTokenProvider{},
		client:        client,
	}
	svc.tokenProvider.sessions = map[int64]qoderSessionCacheEntry{
		account.ID: {
			credentialsHash: qoderCredentialsHash(account.Credentials),
			session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	result, err := svc.ForwardChatCompletions(context.Background(), c, account, body)

	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", result.Model)
	require.Equal(t, "ultimate", result.UpstreamModel)
	require.Equal(t, "ultimate", client.headers["x-model-key"])
	require.Contains(t, rec.Body.String(), `"model":"claude-opus-4-6"`)
}

func TestQoderGatewayWritesOpenAIStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayWritesOpenAIToolCallsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
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

func TestQoderGatewayWritesOpenAIToolCallsStreamMergesIndexDriftForSameCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, `"arguments":"{\"cmd\":"`)
	require.Contains(t, body, `"arguments":"\"pwd\"}"`)
	require.NotContains(t, body, `"index":1`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamKeepsParallelCallIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
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
	require.Equal(t, `{"path":"a"}`, toolDeltas[2]["function"].(map[string]any)["arguments"])
	require.Equal(t, float64(1), toolDeltas[3]["index"])
	require.Equal(t, `{"path":"b"}`, toolDeltas[3]["function"].(map[string]any)["arguments"])
}

func TestQoderGatewayWritesAnthropicStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.NotContains(t, body, `"type":"thinking"`)
	require.NotContains(t, body, `"type":"thinking_delta"`)
	require.NotContains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayWritesAnthropicToolUseStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
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

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsSplitArgumentsInOneBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`))
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`))
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsParallelCallIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
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
				openToolBlocks[int(event.Data["index"].(float64))] = true
			}
			continue
		}
		if event.Event == "content_block_stop" {
			delete(openToolBlocks, int(event.Data["index"].(float64)))
			continue
		}
		if event.Event != "content_block_delta" {
			continue
		}
		delta, _ := event.Data["delta"].(map[string]any)
		if delta["type"] != "input_json_delta" {
			continue
		}
		index := int(event.Data["index"].(float64))
		require.True(t, openToolBlocks[index], "input delta must be inside an open tool_use block")
		inputDeltas[index] = delta["partial_json"].(string)
	}
	require.Equal(t, `{"path":"a"}`, inputDeltas[0])
	require.Equal(t, `{"path":"b"}`, inputDeltas[1])
	require.Empty(t, openToolBlocks)
}

func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	choices := decoded["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "Hello", message["content"])
	usage := decoded["usage"].(map[string]any)
	require.Equal(t, float64(12), usage["prompt_tokens"])
	require.Equal(t, float64(34), usage["completion_tokens"])
	require.Equal(t, float64(46), usage["total_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionWithToolCalls(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionMergesIndexDriftForSameCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(1), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsParallelCallIndexes(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"path":"a"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.JSONEq(t, `{"path":"b"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageIgnoresThinking(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	content := decoded["content"].([]any)
	require.Len(t, content, 1)
	textBlock := content[0].(map[string]any)
	require.Equal(t, "Hi", textBlock["text"])
	usage := decoded["usage"].(map[string]any)
	require.Equal(t, float64(12), usage["input_tokens"])
	require.Equal(t, float64(34), usage["output_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithToolUse(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "content.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.cmd").String())
}

func TestQoderGatewayReadsWrappedSSE(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":3,\\\"completion_tokens\\\":4,\\\"total_tokens\\\":7}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Equal(t, "reasoning_delta", events[0].Type)
	require.Equal(t, "hidden thought", events[0].Text)
	require.Equal(t, "text_delta", events[1].Type)
	require.Equal(t, "Hi", events[1].Text)
	require.True(t, events[2].HasUsage)
	require.Equal(t, 3, events[2].PromptTokens)
	require.Equal(t, 4, events[2].CompletionTokens)
	require.True(t, events[3].IsDone)
}

func TestQoderGatewayReadsWrappedSSEUpstreamError(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	require.Error(t, err)
	require.Empty(t, events)
	var apiErr *qoder.APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 403, apiErr.StatusCode)
	require.Equal(t, "101", apiErr.Code)
	require.Equal(t, "Signature invalid", apiErr.Message)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", apiErr.Error())
}

func TestQoderGatewayAgentLimitSetsRateLimitedUntilReset(t *testing.T) {
	repo := &qoderRateLimitRepoStub{}
	svc := &QoderGatewayService{accountRepo: repo}
	account := &Account{ID: 77}
	err := &qoder.APIError{
		StatusCode:          http.StatusTooManyRequests,
		Code:                "115",
		AgentLimitResetTime: 1783841289162,
	}

	svc.applyUpstreamErrorPolicy(context.Background(), account, err)

	require.Equal(t, int64(77), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

func TestQoderGatewayNonStreamingSSEAgentLimitSetsRateLimited(t *testing.T) {
	account := &Account{ID: 78}
	repo := &qoderRateLimitRepoStub{}
	svc := &QoderGatewayService{accountRepo: repo}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"code\\\":\\\"115\\\",\\\"message\\\":\\\"{\\\\\\\"agentLimitResetTime\\\\\\\":1783841289162}\\\"}\",\"statusCodeValue\":429,\"statusCode\":\"TOO_MANY_REQUESTS\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	if err != nil {
		svc.applyUpstreamErrorPolicy(context.Background(), account, err)
	}

	require.Error(t, err)
	require.Empty(t, events)
	require.Equal(t, int64(78), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

func TestQoderGatewayRequestDoerUsesHTTPUpstreamProxyAndTLS(t *testing.T) {
	proxyID := int64(11)
	account := &Account{
		ID:          66,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 3,
		ProxyID:     &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	upstream := &qoderHTTPUpstreamRecorder{
		body: "data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &QoderGatewayService{
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	doer := svc.qoderRequestDoer(account)
	require.NotNil(t, doer)
	req := httptest.NewRequest(http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader("{}"))

	resp, err := doer(req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(66), upstream.accountID)
	require.True(t, upstream.profileSet)
}

func TestQoderGatewayRefreshAccountSessionPersistsCredentialsAndInvalidatesCache(t *testing.T) {
	account := Account{
		ID:       91,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
		},
	}
	repo := &qoderRefreshAccountRepoStub{
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
	}
	provider := &QoderTokenProvider{
		sessions: map[int64]qoderSessionCacheEntry{
			account.ID: {
				credentialsHash: "old-hash",
				session: &qoder.SessionContext{
					Identity: &qoder.AuthIdentity{SecurityOauthToken: "old-token"},
					Machine:  &qoder.MachineIdentity{MachineID: "machine-1"},
				},
			},
		},
	}
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(_ context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		require.Equal(t, "old-refresh", refreshToken)
		require.Equal(t, "old-token", securityOauthToken)
		require.Equal(t, "machine-1", machine.MachineID)
		return &qoder.AuthIdentity{
			SecurityOauthToken: "new-token",
			RefreshToken:       "new-refresh",
			UID:                "user-1",
			AID:                "user-1",
			UserType:           "personal_standard",
		}, nil
	}
	svc := &QoderGatewayService{
		tokenProvider: provider,
		accountRepo:   repo,
		newRefresher:  func() *QoderTokenRefresher { return refresher },
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, "new-token", repo.updatedCredentials["security_oauth_token"])
	require.Equal(t, "new-refresh", repo.updatedCredentials["refresh_token"])
	require.NotNil(t, repo.updatedCredentials["_token_version"])
	_, cached := provider.sessions[account.ID]
	require.False(t, cached)
}

func TestQoderGatewayStreamsResponseWithoutPrebuffering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderOpenAIStreamResponse(c, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayStreamsAnthropicResponseIgnoresThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.NotContains(t, body, `"type":"thinking"`)
	require.NotContains(t, body, `"type":"thinking_delta"`)
	require.NotContains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayStreamsOpenAIUsageForBillingAndClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":11}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderOpenAIStreamResponse(c, "auto", resp)

	require.NoError(t, err)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"prompt_tokens":5`)
	require.Contains(t, body, `"completion_tokens":6`)
	require.Contains(t, body, `"total_tokens":11`)
	require.Contains(t, body, "data: [DONE]\n\n")
}

func TestQoderGatewayStreamsAnthropicUsageForBillingAndClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":9,\\\"total_tokens\\\":17}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(c, "claude-opus-4-6", resp)

	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"input_tokens":8`)
	require.Contains(t, body, `"output_tokens":9`)
	require.Contains(t, body, "event: message_stop")
}

func qoderOpenAIStreamChunksForTest(t *testing.T, stream string) [][]byte {
	t.Helper()
	chunks := make([][]byte, 0)
	for _, block := range strings.Split(stream, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
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

type qoderAnthropicStreamEventForTest struct {
	Event string
	Data  map[string]any
}

func qoderAnthropicStreamEventsForTest(t *testing.T, stream string) []qoderAnthropicStreamEventForTest {
	t.Helper()
	events := make([]qoderAnthropicStreamEventForTest, 0)
	for _, block := range strings.Split(stream, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		event := qoderAnthropicStreamEventForTest{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event.Data))
			}
		}
		if event.Event != "" {
			events = append(events, event)
		}
	}
	return events
}
