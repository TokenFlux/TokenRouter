package httpapi_test

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIHandleStreamingAwareError_JSONEscaping(t *testing.T) {
	tests := []struct {
		name    string
		errType string
		message string
	}{
		{
			name:    "包含双引号的消息",
			errType: "server_error",
			message: `upstream returned "invalid" response`,
		},
		{
			name:    "包含反斜杠的消息",
			errType: "server_error",
			message: `path C:\Users\test\file.txt not found`,
		},
		{
			name:    "包含双引号和反斜杠的消息",
			errType: "upstream_error",
			message: `error parsing "key\value": unexpected token`,
		},
		{
			name:    "包含换行符的消息",
			errType: "server_error",
			message: "line1\nline2\ttab",
		},
		{
			name:    "普通消息",
			errType: "upstream_error",
			message: "Upstream service temporarily unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadGateway, tt.errType, tt.message, true)

			body := w.Body.String()

			// 验证 SSE 格式：event: error\ndata: {JSON}\n\n
			assert.True(t, strings.HasPrefix(body, "event: error\n"), "应以 'event: error\\n' 开头")
			assert.True(t, strings.HasSuffix(body, "\n\n"), "应以 '\\n\\n' 结尾")

			// 提取 data 部分
			lines := strings.Split(strings.TrimSuffix(body, "\n\n"), "\n")
			require.Len(t, lines, 2, "应有 event 行和 data 行")
			dataLine := lines[1]
			require.True(t, strings.HasPrefix(dataLine, "data: "), "第二行应以 'data: ' 开头")
			jsonStr := strings.TrimPrefix(dataLine, "data: ")

			// 验证 JSON 合法性
			var parsed map[string]any
			err := json.Unmarshal([]byte(jsonStr), &parsed)
			require.NoError(t, err, "JSON 应能被成功解析，原始 JSON: %s", jsonStr)

			// 验证结构
			errorObj, ok := parsed["error"].(map[string]any)
			require.True(t, ok, "应包含 error 对象")
			assert.Equal(t, tt.errType, errorObj["type"])
			assert.Equal(t, tt.message, errorObj["message"])
		})
	}
}

func TestOpenAIHandleStreamingAwareErrorWithCode_EmitsStableClassification(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	gatewayhttp.DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(
		c,
		http.StatusBadGateway,
		"upstream_error",
		openai.OpenAIUpstreamHTTP2StreamErrorCode,
		"Upstream HTTP/2 stream failed",
		true,
		true,
	)

	body := w.Body.String()
	require.Contains(t, body, "event: error\n")
	require.Equal(t, "upstream_error", gjson.Get(body[strings.Index(body, "{"):], "error.type").String())
	require.Equal(t, openai.OpenAIUpstreamHTTP2StreamErrorCode, gjson.Get(body[strings.Index(body, "{"):], "error.code").String())
	require.NotContains(t, body, "stream ID")

	streamErr, ok := gatewayhttp.GetOpsStreamError(c)
	require.True(t, ok)
	require.True(t, streamErr.CountTowardsSLA)
	require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
}

func TestOpenAIHandleStreamingAwareError_NonStreaming(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadGateway, "upstream_error", "test error", false)

	// 非流式应返回 JSON 响应
	assert.Equal(t, http.StatusBadGateway, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)
	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errorObj["type"])
	assert.Equal(t, "test error", errorObj["message"])
}

func TestOpenAIEnsureForwardErrorResponse_WritesFallbackWhenNotWritten(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.True(t, wrote)
	require.Equal(t, http.StatusBadGateway, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)
	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errorObj["type"])
	assert.Equal(t, "Upstream request failed", errorObj["message"])
}

// Writer 已写后 ensureForwardErrorResponse 必须仍然把错误信息以 SSE
// 形式追加给客户端（streamStarted 强制 true）。
// 这是 case B 修复：旧实现遇到 Writer.Written 直接 return false，
// 客户端只能拿到 silent EOF；Codex CLI 报 "stream closed before response.completed"。
func TestOpenAIEnsureForwardErrorResponse_AppendsSSEAfterWritten(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.String(http.StatusTeapot, "already written")

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.True(t, wrote, "must attempt to communicate the failure to the client via SSE")
	// 状态码改不了（headers 已 flush），但 body 应该追加 SSE 错误事件。
	require.Equal(t, http.StatusTeapot, w.Code)
	assert.Contains(t, w.Body.String(), "already written")
	// 非 /responses 路径走 legacy event: error 分支。
	assert.Contains(t, w.Body.String(), "event: error\n")
}

// case B 回归测试：/responses 路径，Writer 已被写过（模拟 ping flushed），
// ensureForwardErrorResponse 必须发 response.failed，让 Codex 收到合规终止事件。
func TestOpenAIEnsureForwardErrorResponse_ResponsesRouteAfterWrittenEmitsResponseFailed(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
	// 模拟 ping 已 flush 的状态：Writer 已写过 1 个字节
	_, _ = c.Writer.WriteString(":\n\n")

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.True(t, wrote)
	body := w.Body.String()
	assert.Contains(t, body, ":\n\n", "earlier ping bytes preserved")
	assert.Contains(t, body, "event: response.failed\n", "appended a Responses terminal event")
	assert.Contains(t, body, `"type":"response.failed"`)
	assert.Contains(t, body, `"code":"upstream_error"`)
	assert.Contains(t, body, "Upstream request failed")
}

func TestOpenAIEnsureForwardErrorResponse_CompactKeepaliveOnlyWritesResponseFailed(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
	gatewayhttp.MarkOpenAICompactClientStream(c)

	stop := gatewayhttp.StartOpenAICompactSSEKeepalive(c, 5*time.Millisecond)
	defer stop()
	before := gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c)
	require.Eventually(t, c.Writer.Written, time.Second, time.Millisecond)
	require.Equal(t, before, gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c))
	// 模拟上游错误路径已设置 committed 标记，但未实际写出语义事件。
	gatewayhttp.MarkResponseCommitted(c)

	require.True(t, gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "event: response.failed\n")
	require.NotContains(t, w.Body.String(), "event: error\n")
}

// TestOpenAIEnsureForwardErrorResponse_AfterDeltaAppendsSingleValidResponseFailed 确认流中只追加一个合法失败终态。
func TestOpenAIEnsureForwardErrorResponse_AfterDeltaAppendsSingleValidResponseFailed(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)

	delta := `{"type":"response.output_text.delta","delta":"ok","sequence_number":1}`
	_, err := c.Writer.WriteString("event: response.output_text.delta\ndata: " + delta + "\n\n")
	require.NoError(t, err)

	require.True(t, gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, true))

	frames := strings.Split(strings.TrimSuffix(w.Body.String(), "\n\n"), "\n\n")
	require.Len(t, frames, 2)
	errorEvents := 0
	for _, frame := range frames {
		lines := strings.Split(frame, "\n")
		require.Len(t, lines, 2)
		require.True(t, strings.HasPrefix(lines[0], "event: "))
		require.True(t, strings.HasPrefix(lines[1], "data: "))

		eventType := strings.TrimPrefix(lines[0], "event: ")
		data := strings.TrimPrefix(lines[1], "data: ")
		require.True(t, json.Valid([]byte(data)), "each downstream SSE frame must contain valid JSON")
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal([]byte(data), &event))
		require.Equal(t, eventType, event.Type)
		if eventType == "response.failed" {
			errorEvents++
		}
	}
	require.Equal(t, 1, errorEvents)
}

func TestOpenAIEnsureForwardErrorResponse_ImageJSONKeepaliveWritesSingleJSONFallback(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	stop := gatewayhttp.StartOpenAIImagesJSONKeepalive(c, 5*time.Millisecond)
	defer stop()
	before := gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
	require.Eventually(t, c.Writer.Written, time.Second, time.Millisecond)
	require.Equal(t, before, gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c))
	require.False(t, gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("read upstream response: unexpected EOF")))

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.True(t, wrote)
	require.Equal(t, http.StatusOK, w.Code, "heartbeat already committed the status")
	require.True(t, json.Valid(w.Body.Bytes()), w.Body.String())
	require.NotContains(t, w.Body.String(), "event:")
	require.NotContains(t, w.Body.String(), "data:")

	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	var payload map[string]any
	require.NoError(t, decoder.Decode(&payload))
	require.ErrorIs(t, decoder.Decode(&payload), io.EOF)
	require.Equal(t, "upstream_error", gjson.Get(w.Body.String(), "error.type").String())
	require.Equal(t, "Upstream request failed", gjson.Get(w.Body.String(), "error.message").String())
}

func TestOpenAIEnsureForwardErrorResponse_ImageJSONKeepalivePreservesCompletedJSON(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	stop := gatewayhttp.StartOpenAIImagesJSONKeepalive(c, 5*time.Millisecond)
	defer stop()
	before := gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
	require.Eventually(t, c.Writer.Written, time.Second, time.Millisecond)
	c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": "aW1hZ2U="}}})
	completedBody := w.Body.String()
	require.True(t, json.Valid([]byte(completedBody)), completedBody)
	require.Greater(t, gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c), before)
	require.False(t, gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("read upstream trailer: unexpected EOF")))

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.False(t, wrote, "the completed Images JSON already communicated the response")
	require.Equal(t, completedBody, w.Body.String())
	require.NotContains(t, w.Body.String(), "event:")
	require.NotContains(t, w.Body.String(), "data:")

	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	var payload map[string]any
	require.NoError(t, decoder.Decode(&payload))
	require.ErrorIs(t, decoder.Decode(&payload), io.EOF)
	require.Equal(t, "aW1hZ2U=", gjson.Get(w.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIEnsureForwardErrorResponse_FastImageJSONKeepalivePreservesCompletedJSON(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	stop := gatewayhttp.StartOpenAIImagesJSONKeepalive(c, time.Hour)
	defer stop()
	before := gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
	c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"b64_json": "ZmFzdC1pbWFnZQ=="}}})
	completedBody := w.Body.String()
	require.True(t, json.Valid([]byte(completedBody)), completedBody)
	require.Greater(t, gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c), before)
	require.False(t, gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("read upstream trailer: unexpected EOF")))

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, false)

	require.False(t, wrote, "fast completed Images JSON already communicated the response")
	require.Equal(t, completedBody, w.Body.String())
	require.NotContains(t, w.Body.String(), "event:")
	require.NotContains(t, w.Body.String(), "data:")

	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	var payload map[string]any
	require.NoError(t, decoder.Decode(&payload))
	require.ErrorIs(t, decoder.Decode(&payload), io.EOF)
	require.Equal(t, "ZmFzdC1pbWFnZQ==", gjson.Get(w.Body.String(), "data.0.b64_json").String())
}
