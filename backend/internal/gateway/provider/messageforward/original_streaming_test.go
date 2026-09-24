//go:build unit

package messageforward_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- parseSSEUsage 测试 ---

func TestHandleStreamingResponse_CacheTokens(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":30}}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":15}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
	}()

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 15, result.Usage.OutputTokens)
	require.Equal(t, 20, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 30, result.Usage.CacheReadInputTokens)
}

func TestHandleStreamingResponse_EmptyStream(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		// 直接关闭，不发送任何事件
		_ = pw.Close()
	}()

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
}

func TestHandleStreamingResponse_SpecialCharactersInJSON(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		// 包含特殊字符的 content_block_delta（引号、换行、Unicode）
		_, _ = pw.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \\\"world\\\"\\n你好\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
	}()

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)

	// 验证响应中包含转发的数据
	body := rec.Body.String()
	require.Contains(t, body, "content_block_delta", "响应应包含转发的 SSE 事件")
}

// 上游中途读错误（如 HTTP/2 GOAWAY 触发的 unexpected EOF）发生在向客户端写入任何字节前：
// 网关应返回 *UpstreamFailoverError 触发账号 failover/重试，而不是把错误事件直接发给客户端。
func TestHandleStreamingResponse_StreamReadErrorBeforeOutput_TriggersFailover(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &streamReadCloser{err: io.ErrUnexpectedEOF},
	}

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)

	require.Error(t, err)
	require.Nil(t, result, "失败移交场景下不应返回 streamingResult")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "未输出过字节时 stream read error 必须包成 UpstreamFailoverError，期望: %v", err)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount, "GOAWAY 类错误应允许同账号重试")

	// ResponseBody 必须是 Anthropic 标准 error 格式：
	// 1) ExtractUpstreamErrorMessage 能正确从 error.message 提取消息（被 handleFailoverExhausted / ops 日志依赖）
	// 2) error.type 标记为 upstream_disconnected
	extractedMsg := upstream.ExtractErrorMessage(failoverErr.ResponseBody)
	require.NotEmpty(t, extractedMsg, "ExtractUpstreamErrorMessage 必须从 ResponseBody 取到非空 message，否则 ops 日志会丢失诊断信息")
	require.Contains(t, extractedMsg, "upstream stream disconnected")
	require.Contains(t, string(failoverErr.ResponseBody), `"type":"error"`)
	require.Contains(t, string(failoverErr.ResponseBody), `"upstream_disconnected"`)

	// 客户端应收不到任何 stream_read_error 事件，由 handler 层根据 failover 结果再决定
	require.NotContains(t, rec.Body.String(), "stream_read_error")
}

// 上游已经发送过事件（c.Writer 已写过字节）后再发生读错误：
// SSE 协议无 resume，网关只能透传 stream_read_error 错误事件给客户端，不能 failover。
func TestHandleStreamingResponse_StreamReadErrorAfterOutput_PassesThrough(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	// 第一次 Read 返回完整 SSE 事件让网关向 client 写入字节，第二次 Read 返回 EOF
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			payload: []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n"),
			err:     io.ErrUnexpectedEOF,
		},
	}

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)

	require.Error(t, err)
	require.Contains(t, err.Error(), "stream read error", "已开始流后应透传普通 stream read error")
	require.NotNil(t, result, "透传场景下应返回已收集的 streamingResult")

	// 不应被错误地包成 failover error
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "已经向客户端写过字节时不能再 failover")

	// 客户端必须收到 Anthropic 标准格式的 SSE error 事件，error.type=stream_read_error，
	// error.message 含具体根因（让 SDK 能解析、UI 能显示具体错误）
	body := rec.Body.String()
	require.Contains(t, body, "event: error\n", "必须按 Anthropic SSE 标准发送 error 事件帧")
	require.Contains(t, body, `"type":"error"`, "data 必须含 type:error 顶层字段（Anthropic 标准）")
	require.Contains(t, body, `"stream_read_error"`, "error.type 必须为 stream_read_error")
	require.Contains(t, body, "upstream stream disconnected", "error.message 必须包含具体根因，Claude Code 等客户端才能显示有效错误文案")
}

// 默认 (*net.OpError).Error() 会拼接 Source/Addr 字段，泄露内部 IP/端口与上游
// 服务器地址。sanitizeStreamError 必须剥离这些信息，避免基础设施拓扑通过
// failover ResponseBody 或 SSE error 帧返回给客户端。
func TestHandleStreamingResponse_FailoverBodyDoesNotLeakAddresses(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	src, _ := net.ResolveTCPAddr("tcp", "10.0.0.1:54321")
	dst, _ := net.ResolveTCPAddr("tcp", "52.1.2.3:443")
	netErr := &net.OpError{
		Op:     "read",
		Net:    "tcp",
		Source: src,
		Addr:   dst,
		Err:    syscall.ECONNRESET,
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &streamReadCloser{err: netErr},
	}

	_, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	require.Error(t, err)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))

	body := string(failoverErr.ResponseBody)
	require.NotContains(t, body, "10.0.0.1", "failover ResponseBody 不得泄露内部源 IP")
	require.NotContains(t, body, "54321")
	require.NotContains(t, body, "52.1.2.3", "failover ResponseBody 不得泄露上游 IP")
	require.NotContains(t, body, "443")
	// 仍然包含可诊断的根因
	require.Contains(t, body, "connection reset by peer")
	require.Contains(t, body, "upstream stream disconnected")
}

// 上游 HTTP 200 + SSE 流体内 event:error 帧应保留 data 行原文，
// 这是 Forward 后续补全 UpstreamFailoverError.ResponseBody 与 Ops 日志的前提。
func TestHandleStreamingResponse_SSEErrorEvent_ReturnsTypedErrorWithRawData(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const errorJSON = `{"type":"error","error":{"type":"overloaded_error","message":"Anthropic upstream is overloaded"}}`

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: error\ndata: " + errorJSON + "\n\n"))
	}()

	result, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()

	require.Error(t, err)
	require.Nil(t, result)

	var sseErr *anthropic.StreamErrorEventError
	require.True(t, errors.As(err, &sseErr), "SSE event:error 必须包成 *sseStreamErrorEventError，期望: %v", err)
	require.Equal(t, errorJSON, sseErr.RawData)
	require.Equal(t, "have error in stream", err.Error())
	require.Equal(t, "Anthropic upstream is overloaded", upstream.ExtractErrorMessage([]byte(sseErr.RawData)))
}

// 上游只发 event:error 而没有 data 行时，也要返回 typed error，避免上层走不到 stream_error 分支。
func TestHandleStreamingResponse_SSEErrorEvent_EmptyDataLine(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: error\n\n"))
	}()

	_, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()

	require.Error(t, err)
	var sseErr *anthropic.StreamErrorEventError
	require.True(t, errors.As(err, &sseErr), "即使 data 行为空，也必须返回 typed error")
	require.Equal(t, "", sseErr.RawData)
}

// 上游先发部分流输出再发 event:error 时，仍要保留真实错误体；
// handler 层会因已写客户端响应而停止继续换号。
func TestHandleStreamingResponse_SSEErrorEvent_AfterPartialStreamOutput(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const errorJSON = `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited"}}`

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}` + "\n\n"))
		_, _ = pw.Write([]byte("event: error\ndata: " + errorJSON + "\n\n"))
	}()

	_, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()

	require.Error(t, err)
	var sseErr *anthropic.StreamErrorEventError
	require.True(t, errors.As(err, &sseErr), "已发数据后再来的 SSE event:error 必须仍包成 typed error，期望: %v", err)
	require.Equal(t, errorJSON, sseErr.RawData)
	require.Greater(t, rec.Body.Len(), 0, "message_start 应被转发到客户端")
	require.Contains(t, rec.Body.String(), "message_start")
}

// 上游 event:error 的 data 行不是合法 JSON 时，也要保留原始内容，供 Ops detail 排查。
func TestHandleStreamingResponse_SSEErrorEvent_NonJSONDataLine(t *testing.T) {

	svc := newStreamingRuntimeFixture(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: error\ndata: not-a-json-payload\n\n"))
	}()

	_, err := streamResponseFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", false)
	_ = pr.Close()

	require.Error(t, err)
	var sseErr *anthropic.StreamErrorEventError
	require.True(t, errors.As(err, &sseErr))
	require.Equal(t, "not-a-json-payload", sseErr.RawData)
	require.NotPanics(t, func() {
		_ = upstream.ExtractErrorMessage([]byte(sseErr.RawData))
	})
	require.Equal(t, "", upstream.ExtractErrorMessage([]byte(sseErr.RawData)))
}
