package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildQoderPayloadFromChatCompletions(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5-codex",
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
	require.Equal(t, "lite", modelKey)
	require.Equal(t, true, payload["stream"])
	require.Equal(t, "personal_standard", payload["aliyun_user_type"])
	require.Equal(t, 123, payload["parameters"].(map[string]any)["max_tokens"])
	require.Equal(t, "lite", payload["model_config"].(map[string]any)["key"])
	require.Equal(t, "hello", payload["chat_context"].(map[string]any)["text"].(map[string]any)["text"])

	messages := payload["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "be terse", messages[0].(map[string]any)["content"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "hello", messages[1].(map[string]any)["content"])
	require.Equal(t, "tool", messages[3].(map[string]any)["role"])
	require.Equal(t, "tool output", messages[3].(map[string]any)["content"])
	require.Len(t, payload["tools"].([]any), 1)
}

func TestBuildQoderPayloadFromAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"qwen3.7-max",
		"max_tokens":456,
		"system":[{"type":"text","text":"system one"},{"type":"text","text":"system two"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"tool result"}]}]}
		]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "qmodel_latest", modelKey)
	require.Equal(t, 456, payload["parameters"].(map[string]any)["max_tokens"])
	require.Equal(t, "qmodel_latest", payload["model_config"].(map[string]any)["key"])

	messages := payload["messages"].([]any)
	require.Len(t, messages, 2)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "system one\nsystem two", messages[0].(map[string]any)["content"])
	require.Equal(t, "hello\ntool result", messages[1].(map[string]any)["content"])
}

func TestQoderGatewayWritesOpenAIStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "gpt-5-codex", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayWritesAnthropicStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "Hi"},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-sonnet-4-5", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("gpt-5-codex", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	choices := decoded["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "Hello", message["content"])
}

func TestQoderGatewayReadsWrappedSSE(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "Hi", events[0].Text)
	require.True(t, events[1].IsDone)
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

	err := WriteQoderOpenAIStreamResponse(c, "gpt-5-codex", resp)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}
