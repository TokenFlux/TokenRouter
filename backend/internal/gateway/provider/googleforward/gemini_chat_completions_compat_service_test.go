package googleforward_test

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

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiForwardAsResponsesReturnsResponsesFormat(t *testing.T) {

	upstreamBody := `{
		"candidates":[{"content":{"parts":[
			{"text":"inspect inputs","thought":true},
			{"text":"calling tool"},
			{"functionCall":{"name":"get_weather","args":{"city":"Tokyo"}}}
		]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"thoughtsTokenCount":5}
	}`
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusOK,

		Header: http.Header{"X-Request-Id": []string{"gemini-response-1"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           201,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeAPIKey,

		Concurrency: 1,

		Credentials: map[string]any{
			"api_key": "gemini-key",
			"model_mapping": map[string]any{
				"channel-model": "gemini-2.5-pro",
			},
		},
	},
	}
	body := []byte(`{"model":"channel-model","input":"weather","tools":[{"type":"function","name":"get_weather","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "channel-model", result.Model)
	require.Equal(t, "gemini-2.5-pro", result.UpstreamModel)
	require.Equal(t, "gemini-response-1", result.RequestID)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, "response", gjson.GetBytes(recorder.Body.Bytes(), "object").String())
	require.Equal(t, "channel-model", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
	require.Equal(t, "inspect inputs", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="reasoning").summary.0.text`).String())
	require.Equal(t, "get_weather", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="function_call").name`).String())
	require.Equal(t, "calling tool", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="message").content.0.text`).String())
	require.Contains(t, httpStub.lastReq.URL.String(), "/models/gemini-2.5-pro:generateContent")
}

func TestGeminiForwardAsResponsesOAuthCollectsReasoningTextAndTools(t *testing.T) {

	upstreamBody := strings.Join([]string{

		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"plan ","thought":true}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"thoughtsTokenCount":1}}}`,

		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"carefully","thought":true}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"thoughtsTokenCount":2}}}`,

		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":2}}}`,

		`data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":42}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":2}}}`,

		"data: [DONE]",

		"",
	}, "\n\n")
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{

		tokenProvider: newGeminiTokenSourceForTest(),

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           204,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeOAuth,

		Concurrency: 1,

		Credentials: map[string]any{
			"access_token": "ya29.test-token",
			"project_id":   "project-1",
		},
	},
	}
	body := []byte(`{"model":"gemini-2.5-pro","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "plan carefully", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="reasoning").summary.0.text`).String())
	require.Equal(t, "answer", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="message").content.0.text`).String())
	require.Equal(t, "lookup", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="function_call").name`).String())
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1internal:streamGenerateContent?alt=sse")
}

func TestGeminiForwardAsResponsesStreamsReasoningTextToolAndUsage(t *testing.T) {

	upstreamBody := strings.Join([]string{

		`data: {"candidates":[{"content":{"parts":[{"text":"plan","thought":true}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"thoughtsTokenCount":1}}`,

		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"thoughtsTokenCount":1}}`,

		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":42}}}]} ,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"thoughtsTokenCount":1}}`,

		"data: [DONE]",

		"",
	}, "\n\n")
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           202,
		Platform:     capability.PlatformGemini,
		Type:         capability.AccountTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	streamBody := recorder.Body.String()
	require.Contains(t, streamBody, "event: response.created")
	require.Contains(t, streamBody, "event: response.reasoning_summary_text.delta")
	require.Contains(t, streamBody, `"delta":"plan"`)
	require.Contains(t, streamBody, "event: response.output_text.delta")
	require.Contains(t, streamBody, `"delta":"hello"`)
	require.Contains(t, streamBody, "event: response.function_call_arguments.done")
	require.Contains(t, streamBody, `"name":"lookup"`)
	require.Contains(t, streamBody, "event: response.completed")
	require.NotContains(t, streamBody, "data: [DONE]")
}

type geminiResponsesFailingStream struct {
	read bool
}

func (r *geminiResponsesFailingStream) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("upstream stream failed")
	}
	r.read = true
	return copy(p, []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")), nil
}

func (r *geminiResponsesFailingStream) Close() error { return nil }

func TestGeminiForwardAsResponsesCommitsStreamBeforeReadFailure(t *testing.T) {

	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: &geminiResponsesFailingStream{},
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           203,
		Platform:     capability.PlatformGemini,
		Type:         capability.AccountTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello","stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	_, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.ErrorContains(t, err, "stream read error")
	require.Positive(t, recorder.Body.Len(), "首个字节写出后 handler 必须禁止 failover")
}

func TestGeminiForwardAsResponsesMapsUpstreamError(t *testing.T) {

	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusBadRequest,

		Header: http.Header{"X-Goog-Request-Id": []string{"gemini-error-1"}},

		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":400,"message":"invalid generation request","status":"INVALID_ARGUMENT"}}`,
		)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           205,
		Platform:     capability.PlatformGemini,
		Type:         capability.AccountTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
	require.Equal(t, "Invalid request", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
}

func TestGeminiForwardAsResponsesReturnsFailoverBeforeResponseStarts(t *testing.T) {

	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{

		StatusCode: http.StatusForbidden,

		Header: http.Header{

			"Www-Authenticate": []string{`Bearer error="insufficient_scope"`},

			"X-Goog-Request-Id": []string{"gemini-failover-1"},
		},

		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":403,"message":"insufficient authentication scope","status":"PERMISSION_DENIED"}}`,
		)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           206,
		Platform:     capability.PlatformGemini,
		Type:         capability.AccountTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), account, body, nil)

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.Zero(t, recorder.Body.Len())
}

func TestGeminiResponseToChatCompletionsPreservesInlineData(t *testing.T) {
	tests := []struct {
		name  string
		parts []any
		want  string
	}{

		{

			name: "image only",

			parts: []any{
				map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "aW1hZ2U="}},
			},

			want: "![image](data:image/png;base64,aW1hZ2U=)",
		},

		{

			name: "text and image",

			parts: []any{

				map[string]any{"text": "rendered image:\n"},

				map[string]any{"inlineData": map[string]any{"mimeType": "image/webp", "data": "d2VicA=="}},
			},

			want: "rendered image:\n![image](data:image/webp;base64,d2VicA==)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geminiResp := map[string]any{
				"candidates": []any{map[string]any{
					"content":      map[string]any{"parts": tt.parts},
					"finishReason": "STOP",
				}},
			}
			rawData, err := json.Marshal(geminiResp)
			require.NoError(t, err)

			got, _, err := gemini.GeminiResponseToChatCompletions(geminiResp, "gemini-test", rawData, nil)
			require.NoError(t, err)
			require.Len(t, got.Choices, 1)

			var content string
			require.NoError(t, json.Unmarshal(got.Choices[0].Message.Content, &content))
			require.Equal(t, tt.want, content)
			require.Equal(t, "stop", got.Choices[0].FinishReason)
		})
	}
}

func TestGeminiResponseToChatCompletionsOmitsInvalidInlineData(t *testing.T) {
	tests := []struct {
		name       string
		inlineData map[string]any
	}{

		{
			name:       "unsupported MIME type",
			inlineData: map[string]any{"mimeType": "image/svg+xml", "data": "PHN2Zz48L3N2Zz4="},
		},

		{
			name:       "malformed MIME type",
			inlineData: map[string]any{"mimeType": "image/png; charset=utf-8", "data": "aW1hZ2U="},
		},

		{
			name:       "malformed base64",
			inlineData: map[string]any{"mimeType": "image/png", "data": "not-valid-base64!!!"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geminiResp := map[string]any{
				"candidates": []any{map[string]any{

					"content": map[string]any{"parts": []any{map[string]any{"text": "before"}, map[string]any{"inlineData": tt.inlineData}, map[string]any{"text": "after"}}},

					"finishReason": "STOP",
				}},
			}
			rawData, err := json.Marshal(geminiResp)
			require.NoError(t, err)

			got, _, err := gemini.GeminiResponseToChatCompletions(geminiResp, "gemini-test", rawData, nil)
			require.NoError(t, err)

			var content string
			require.NoError(t, json.Unmarshal(got.Choices[0].Message.Content, &content))
			require.Equal(t, "beforeafter", content)
		})
	}
}

func TestConvertGeminiToClaudeMessageOmitsInlineDataForAnthropicMessages(t *testing.T) {
	geminiResp := map[string]any{
		"candidates": []any{map[string]any{

			"content": map[string]any{"parts": []any{

				map[string]any{"text": "before"},

				map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "aW1hZ2U="}},

				map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "Paris"}}},

				map[string]any{"text": "after"},
			}},

			"finishReason": "STOP",
		}},
	}
	rawData, err := json.Marshal(geminiResp)
	require.NoError(t, err)

	withInlineData, _ := gemini.ConvertGeminiToClaudeMessage(geminiResp, "gemini-test", rawData, true)
	require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, withInlineData["id"])
	contentWithInlineData, ok := withInlineData["content"].([]any)
	require.True(t, ok)
	require.Len(t, contentWithInlineData, 4)
	require.Equal(t, map[string]any{"type": "text", "text": "before"}, contentWithInlineData[0])
	require.Equal(t, map[string]any{"type": "text", "text": "![image](data:image/png;base64,aW1hZ2U=)"}, contentWithInlineData[1])
	toolUse, ok := contentWithInlineData[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_use", toolUse["type"])
	require.Equal(t, "get_weather", toolUse["name"])
	require.Equal(t, map[string]any{"type": "text", "text": "after"}, contentWithInlineData[3])

	withoutInlineData, _ := gemini.ConvertGeminiToClaudeMessage(geminiResp, "gemini-test", rawData, false)
	contentWithoutInlineData, ok := withoutInlineData["content"].([]any)
	require.True(t, ok)
	require.Len(t, contentWithoutInlineData, 3)
	require.Equal(t, map[string]any{"type": "text", "text": "before"}, contentWithoutInlineData[0])
	toolUseWithoutInlineData, ok := contentWithoutInlineData[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_use", toolUseWithoutInlineData["type"])
	require.Equal(t, "get_weather", toolUseWithoutInlineData["name"])
	require.Equal(t, map[string]any{"type": "text", "text": "after"}, contentWithoutInlineData[2])
}

func TestGenerateAnthropicMsgID_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := upstream.GenerateAnthropicMsgID()
		require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, id)
		_, duplicate := seen[id]
		require.False(t, duplicate, "第 %d 次调用生成了重复 ID: %s", i, id)
		seen[id] = struct{}{}
	}
}

func TestGeminiResponseToChatCompletionsRetainsTextAndToolBehavior(t *testing.T) {
	geminiResp := map[string]any{
		"candidates": []any{map[string]any{

			"content": map[string]any{"parts": []any{

				map[string]any{"text": "checking"},

				map[string]any{"functionCall": map[string]any{
					"name": "get_weather",
					"args": map[string]any{"city": "Paris"},
				}},
			}},

			"finishReason": "STOP",
		}},
	}
	rawData, err := json.Marshal(geminiResp)
	require.NoError(t, err)

	got, _, err := gemini.GeminiResponseToChatCompletions(geminiResp, "gemini-test", rawData, nil)
	require.NoError(t, err)
	require.Len(t, got.Choices, 1)

	choice := got.Choices[0]
	var content string
	require.NoError(t, json.Unmarshal(choice.Message.Content, &content))
	require.Equal(t, "checking", content)
	require.Equal(t, "tool_calls", choice.FinishReason)
	require.Len(t, choice.Message.ToolCalls, 1)
	require.Equal(t, "get_weather", choice.Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"city":"Paris"}`, choice.Message.ToolCalls[0].Function.Arguments)
}
