//go:build unit

// 本文件验证迁移输出适配器继承已有 HTTP 状态，不改变原重试边界。
package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestS09AnthropicPriorHeartbeatPreservesReadFailureBoundary(t *testing.T) {

	svc := newMinimalGatewayService()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Writer.Header().Set("X-Fixture", "prior")
	_, err := c.Writer.Write([]byte(": ping\n\n"))
	require.NoError(t, err)
	c.Writer.Flush()
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: &streamReadCloser{err: io.ErrUnexpectedEOF}}
	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	require.Error(t, err)
	require.NotNil(t, result)
	var failover *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Contains(t, rec.Body.String(), "stream_read_error")
	require.Equal(t, "prior", c.Writer.Header().Get("X-Fixture"))
}
