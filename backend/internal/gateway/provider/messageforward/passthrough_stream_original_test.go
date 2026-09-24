package messageforward_test

import (
	"bufio"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"

	"context"

	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingStillCollectsUsageAfterClientDisconnect(t *testing.T) {

	// Use a canceled context recorder to simulate client disconnect behavior.
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":11}}}`,
			"",
			`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_MissingTerminalEventReturnsError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":11}}}`,
			"",
			`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
			"",
		}, "\n"))),
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingErrTooLong(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: 32}, messageforward.

			// Scanner 初始缓冲为 64KB，构造更长单行触发 bufio.ErrTooLong。
			Dependencies{}, nil,
	)

	longLine := "data: " + strings.Repeat("x", 80*1024)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(longLine)),
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.Error(t, err)
	require.ErrorIs(t, err, bufio.ErrTooLong)
	require.NotNil(t, result)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingDataIntervalTimeout(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, StreamInterval: time.Duration(1) * time.Second, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5}}, time.Now(), "claude-3-7-sonnet-20250219")
	_ = pw.Close()
	_ = pr.Close()

	require.Error(t, err)
	require.Contains(t, err.Error(), "stream data interval timeout")
	require.NotNil(t, result)
	require.False(t, result.ClientDisconnect)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingSendsKeepaliveDuringIdle(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, StreamKeepalive: time.Duration(1) * time.Second, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 模拟上游首个 SSE 事件前的空闲时间，验证下游能收到 ping 保活。
		time.Sleep(1200 * time.Millisecond)
		_, _ = pw.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
			"",
			`data: {"type":"message_delta","usage":{"output_tokens":2}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
		_ = pw.Close()
	}()

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8}}, time.Now(), "claude-3-7-sonnet-20250219")
	_ = pr.Close()
	<-done

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "event: ping\ndata: {\"type\": \"ping\"}\n\n")
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingKeepaliveDoesNotInterleavePartialEvent(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, StreamKeepalive: time.Duration(1) * time.Second, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 先写入未闭合的 SSE 事件，确认保活不会插入事件中间。
		_, _ = pw.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":4}}}` + "\n"))
		time.Sleep(1200 * time.Millisecond)
		_, _ = pw.Write([]byte("\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
		_ = pw.Close()
	}()

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9}}, time.Now(), "claude-3-7-sonnet-20250219")
	_ = pr.Close()
	<-done

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, `data: {"type":"message_start","message":{"usage":{"input_tokens":4}}}`+"\n"+"event: ping")
	require.NotContains(t, body, "event: ping")
	require.Contains(t, body, "data: [DONE]")
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingReadError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{}, nil,
	)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			err: io.ErrUnexpectedEOF,
		},
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 6}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream read error")
	require.NotNil(t, result)
	require.False(t, result.ClientDisconnect)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingTimeoutAfterClientDisconnect(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, StreamInterval: time.Duration(1) * time.Second, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Health: newPartialHealthFixture()}, nil,
	)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = pw.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":9}}}` + "\n"))
		// 保持上游连接静默，触发数据间隔超时分支。
		time.Sleep(1500 * time.Millisecond)
		_ = pw.Close()
	}()

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7}}, time.Now(), "claude-3-7-sonnet-20250219")
	_ = pr.Close()
	<-done

	require.Error(t, err)
	require.Contains(t, err.Error(), "stream usage incomplete after timeout")
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 9, result.Usage.InputTokens)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingContextCanceled(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{}, nil,
	)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			err: context.Canceled,
		},
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream usage incomplete")
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
}

func TestGatewayService_AnthropicAPIKeyPassthrough_StreamingUpstreamReadErrorAfterClientDisconnect(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}

	svc := newResponseRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{}, nil,
	)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			payload: []byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":8}}}` + "\n\n"),
			err:     io.ErrUnexpectedEOF,
		},
	}

	result, err := passthroughStreamFixture(svc, context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4}}, time.Now(), "claude-3-7-sonnet-20250219")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream usage incomplete after disconnect")
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 8, result.Usage.InputTokens)
}
