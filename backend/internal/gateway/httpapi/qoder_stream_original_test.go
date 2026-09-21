package httpapi

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

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const qoderXMLToolCallFixture = `<tool_call>Read<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>`

const qoderJSONShellToolCallFixture = `<tool_call>{"name":"shell","arguments":{"command":"pwd","description":"Print working directory"}}</tool_call>`

const qoderDSMLToolCallFixture = `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="Bash">
<｜｜DSML｜｜parameter name="command" string="true">ls -la</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="description" string="true">List root files</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

type qoderTrackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *qoderTrackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func qoderNoIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`},
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"ls -la","description":"List files in current directory"}`},
		{Type: "tool_call_delta", ToolName: "glob", Arguments: `{"pattern":"**/*.md"}`},
		{IsDone: true},
	}
}

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

func qoderRepeatedIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"pwd","description":"Print working directory"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "glob", Arguments: `{"pattern":"docs/*.md"}`},
		{IsDone: true},
	}
}

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

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "deepseek-v4-pro", resp)
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

	_, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, body.closed)
}

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

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), `"usage"`)
}

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

	_, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp, qoder.QoderOpenAIStreamIncludeUsage(true))
	require.NoError(t, err)
	body := rec.Body.String()
	require.Contains(t, body, `"usage"`)
	require.Contains(t, body, `"choices":[]`)
	require.Contains(t, body, `"prompt_tokens":12`)
	require.Contains(t, body, `"completion_tokens":3`)
}

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
		name  string
		write func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error)
	}{
		{
			name: "openai chat completions",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderOpenAIStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
			},
		},
		{
			name: "anthropic messages",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderAnthropicStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
			},
		},
		{
			name: "responses",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderResponsesStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
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

func TestQoderGatewayStreamWritersDoNotWriteBeforeUpstreamError(t *testing.T) {

	errorLine := qoderWrappedErrorSSELineForTest(t, http.StatusTooManyRequests, map[string]any{
		"code":                "115",
		"message":             "agent limit",
		"agentLimitResetTime": time.Date(2026, 7, 12, 7, 28, 9, 0, time.UTC).UnixMilli(),
	})

	tests := []struct {
		name  string
		write func(context.Context, *gin.Context, *http.Response) (*qoder.QoderStreamResult, error)
	}{
		{
			name: "openai chat completions",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderOpenAIStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "qwen3.7-plus", resp)
			},
		},
		{
			name: "anthropic messages",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderAnthropicStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "qwen3.7-plus", resp)
			},
		},
		{
			name: "responses",
			write: func(ctx context.Context, c *gin.Context, resp *http.Response) (*qoder.QoderStreamResult, error) {
				return qoder.WriteQoderResponsesStreamResponse(ctx, &upstream.OutputContext{Writer: c.Writer}, "qwen3.7-plus", resp)
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

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "deepseek-v4-pro", resp)
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

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "deepseek-v4-pro", resp)
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

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "deepseek-v4-pro", resp, qoder.QoderResponsesStreamToolNameMapper(qoder.QoderDeclaredToolNameMapper([]any{map[string]any{"type": "function", "name": "bash"}})))
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

func TestQoderGatewayWritesResponsesStreamKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

func TestQoderGatewayWritesResponsesStreamKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

	result, err := qoder.WriteQoderResponsesStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayWritesOpenAIToolCallsStream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsEmptyArgumentPlaceholder(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"arguments":"{}"`)
	require.NotContains(t, body, `"arguments":"{}{\"command\"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsTypeOnlyPlaceholderChunk(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

func TestQoderGatewayWritesOpenAIToolCallsStreamMergesIndexDriftForSameCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

func TestQoderGatewayWritesOpenAIToolCallsStreamDropsAmbiguousParallelArgumentDelta(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"id":"call_2"`)
	require.NotContains(t, body, "lost")
	require.NotContains(t, body, `"index":2`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIStreamParsesXMLTextToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

func TestQoderGatewayWritesOpenAIStreamParsesDSMLTextToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := qoder.WriteQoderOpenAIStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

func TestQoderGatewayWritesAnthropicStream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
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

func TestQoderGatewayWritesAnthropicStreamParsesXMLTextToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
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

func TestQoderGatewayWritesAnthropicStreamParsesDSMLTextToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

func TestQoderGatewayWritesAnthropicStreamParsesJSONTextToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
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

func TestQoderGatewayWritesAnthropicToolUseStream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`))
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`))
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamDoesNotFinalizeEmptyObjectPlaceholder(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "toolu_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolType: "function", Arguments: `{"command":"printf cc-single-20260617","description":"Print single nonce"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
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

func TestQoderGatewayWritesAnthropicToolUseStreamSkipsTypeOnlyPlaceholder(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.NotContains(t, body, `"type":"tool_use"`)
	require.NotContains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"stop_reason":"end_turn"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsNoIndexNamedParallelToolCalls(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
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

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
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

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", events)
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

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
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

func TestQoderGatewayNonStreamingKeepaliveKeepsJSONParseable(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	keepalive := qoder.QoderNonStreamingKeepalive(&upstream.OutputContext{Writer: c.Writer})
	require.NotNil(t, keepalive)
	require.NoError(t, keepalive())
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(`{"ok":true}`))

	require.True(t, json.Valid(rec.Body.Bytes()))
	require.Equal(t, "\n{\"ok\":true}", rec.Body.String())
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
}

func TestQoderGatewayStreamKeepaliveDoesNotCommitBeforeStart(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	require.NoError(t, qoder.WriteQoderStreamKeepalive(&upstream.OutputContext{Writer: c.Writer}, false))
	require.Equal(t, -1, c.Writer.Size())
	require.Empty(t, rec.Body.String())

	c.Writer.WriteHeader(http.StatusOK)
	require.NoError(t, qoder.WriteQoderStreamKeepalive(&upstream.OutputContext{Writer: c.Writer}, true))
	require.Equal(t, ": keep-alive\n\n", rec.Body.String())
}

func TestQoderGatewayStreamsResponseWithoutPrebuffering(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

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

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"arguments":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

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

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp, qoder.QoderOpenAIStreamToolNameMapper(qoder.QoderDeclaredToolNameMapper(tools)))
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"bash"`)
	require.NotContains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)
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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"partial_json":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)

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

func TestQoderGatewayWritesAnthropicResponseKeepsNoIndexNamedParallelToolCalls(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, upstream.TokenUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"Print working directory\"}"`)
	require.NotContains(t, body, `"partial_json":"{}{\"command\"`)
}

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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

func TestQoderGatewayStreamsAnthropicResponseCompletesEmptyContentBlock(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

func TestQoderGatewayStreamsAnthropicResponseCompletesOnEOFWithoutDone(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n",
		)),
	}

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)
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

func TestQoderGatewayWritesAnthropicStreamNormalizesExecuteBashToolCall(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "execute_bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := qoder.WriteQoderAnthropicStream(&upstream.OutputContext{Writer: c.Writer}, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"name":"execute_bash"`)
}

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

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp, qoder.QoderOpenAIStreamIncludeUsage(true))

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

	result, err := qoder.WriteQoderOpenAIStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "auto", resp)

	require.NoError(t, err)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.NotContains(t, body, `"usage":`)
	require.Contains(t, body, `"delta":{"content":"Hi"}`)
	require.Contains(t, body, "data: [DONE]\n\n")
}

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

	result, err := qoder.WriteQoderAnthropicStreamResponse(context.Background(), &upstream.OutputContext{Writer: c.Writer}, "claude-opus-4-6", resp)

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

func qoderWrappedSSELineForTest(t *testing.T, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	wrapper, err := json.Marshal(map[string]string{"body": string(body)})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}

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

func qoderOpenAIUsageChunkForTest(t *testing.T, body string) gjson.Result {
	t.Helper()
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
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

func qoderResponsesStreamEventsForTest(t *testing.T, body string) []gjson.Result {
	t.Helper()
	var events []gjson.Result
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" || strings.HasPrefix(frame, ":") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
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

// qoderFailingHTTPWriter 保留原测试的同步写失败边界，验证断开后的尾部用量。
type qoderFailingHTTPWriter struct {
	gin.ResponseWriter
	failAfter int
	writes    int
}

func (w *qoderFailingHTTPWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

// qoderFixtureValue 明确验证解码夹具的类型，保留原字段断言失败语义。
func qoderFixtureValue[T any](t *testing.T, raw any) T {
	t.Helper()
	value, ok := raw.(T)
	require.True(t, ok, "unexpected decoded fixture type: %T", raw)
	return value
}
