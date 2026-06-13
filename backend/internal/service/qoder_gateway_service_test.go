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
	require.Equal(t, "", messages[1].(map[string]any)["content"])
	userContents := messages[1].(map[string]any)["contents"].([]any)
	require.Equal(t, "hello", userContents[0].(map[string]any)["text"])
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
	require.Equal(t, "", messages[1].(map[string]any)["content"])
	userContents := messages[1].(map[string]any)["contents"].([]any)
	require.Equal(t, "hello\ntool result", userContents[0].(map[string]any)["text"])
}

func TestResolveQoderModelUsesOpus46AliasForUltimate(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	info := resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "system", info.Source)

	legacy := resolveQoderModel("claude-opus-4-5")
	require.Equal(t, "claude-opus-4-5", legacy.Key)
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

	err := WriteQoderOpenAIStream(c, "gpt-5-codex", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
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

	err := WriteQoderAnthropicStream(c, "claude-sonnet-4-5", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"text":"Hi"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("gpt-5-codex", events)
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

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithoutReasoning(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-sonnet-4-5", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	content := decoded["content"].([]any)
	textBlock := content[0].(map[string]any)
	require.Equal(t, "Hi", textBlock["text"])
	usage := decoded["usage"].(map[string]any)
	require.Equal(t, float64(12), usage["input_tokens"])
	require.Equal(t, float64(34), usage["output_tokens"])
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

	result, err := WriteQoderOpenAIStreamResponse(c, "gpt-5-codex", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayStreamsAnthropicResponseWithoutReasoning(t *testing.T) {
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

	result, err := WriteQoderAnthropicStreamResponse(c, "claude-sonnet-4-5", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"text":"Hi"`)
	require.NotContains(t, body, "hidden thought")
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

	result, err := WriteQoderOpenAIStreamResponse(c, "gpt-5-codex", resp)

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

	result, err := WriteQoderAnthropicStreamResponse(c, "claude-sonnet-4-5", resp)

	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"input_tokens":8`)
	require.Contains(t, body, `"output_tokens":9`)
	require.Contains(t, body, "event: message_stop")
}
