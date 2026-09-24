//go:build unit

package httpapi_test

import (
	"encoding/json"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func namespaceToolAnthropicStream() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_namespace","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"","usage":{"input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_namespace","name":"codex_app__read_thread","input":{"thread_id":"123"}}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func namespaceToolMapping() bridge.ResponsesClientToolMapping {
	return bridge.ResponsesClientToolMapping{NamespaceTools: map[string]bridge.ResponsesNamespaceName{
		"codex_app__read_thread": {Namespace: "codex_app", Name: "read_thread"},
	}}
}

func TestHandleResponsesBufferedStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	_, err := forward.ResponsesBuffered(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-fable-5", "claude-fable-5", nil, time.Now(), namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `"type":"function_call"`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

func TestHandleResponsesBufferedStreamingResponse_ToolArgumentsAreValidJSON(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(toolAnthropicSSEStream()))}

	_, err := forward.ResponsesBuffered(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-fable-5", "claude-fable-5", nil, time.Now(), bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)

	var body struct {
		Output []struct {
			Type      string `json:"type"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Output, 1)
	require.Equal(t, "function_call", body.Output[0].Type)
	require.JSONEq(t, `{"query":"status"}`, body.Output[0].Arguments)
}

func TestHandleResponsesStreamingResponse_RestoresNamespaceTool(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))}

	_, err := forward.ResponsesStreaming(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-fable-5", "claude-fable-5", nil, time.Now(), namespaceToolMapping())
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `response.output_item.added`)
	require.Contains(t, rec.Body.String(), `"name":"read_thread"`)
	require.Contains(t, rec.Body.String(), `"namespace":"codex_app"`)
	require.NotContains(t, rec.Body.String(), `"name":"codex_app__read_thread"`)
}

func TestHandleResponsesBufferedStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			``,
		}, "\n"))),
	}

	result, err := forward.ResponsesBuffered(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `"cached_tokens":9`)
}

func TestHandleResponsesStreamingResponse_PreservesMessageStartCacheUsage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	result, err := forward.ResponsesStreaming(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.Contains(t, rec.Body.String(), `response.completed`)
}

func TestHandleResponsesBufferedStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// 模拟冒号后没有空格的紧凑 SSE 格式。
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":10}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			``,
		}, "\n"))),
	}

	result, err := forward.ResponsesBuffered(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"OK"`)
}

func TestHandleResponsesStreamingResponse_CompactSSEFormat(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// 流式路径必须使用与缓冲路径相同的紧凑 SSE 解析规则。
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_compact_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event:message_start`,
			`data:{"type":"message_start","message":{"id":"msg_compact_stream","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":15}}}`,
			``,
			`event:content_block_start`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`,
			``,
			`event:message_delta`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
			``,
			`event:message_stop`,
			`data:{"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	result, err := forward.ResponsesStreaming(conversionResponseFixture(resp), gatewayhttp.NewMessageForwardBoundary(c, nil).ConversionOutput(true, &messageforward.AttemptState{}), "claude-sonnet-4.5", "claude-sonnet-4.5", nil, time.Now(), bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 15, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `response.completed`)
}
