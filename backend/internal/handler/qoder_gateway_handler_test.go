package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQoderGatewayErrorDetailsUsesAPIErrorMessage(t *testing.T) {
	err := fmt.Errorf("forward qoder: %w", &qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "101",
		Message:    "Signature invalid",
	})

	status, errType, message, ok := qoderGatewayErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", message)
}

func TestQoderGatewayErrorDetailsMapsRateLimit(t *testing.T) {
	status, errType, message, ok := qoderGatewayErrorDetails(&qoder.APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "Too many requests",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Too many requests", message)
}

func TestQoderGatewayErrorDetailsMapsAgentLimitToRateLimit(t *testing.T) {
	status, errType, message, ok := qoderGatewayErrorDetails(&qoder.APIError{
		StatusCode:          http.StatusBadGateway,
		Code:                "115",
		Message:             `{"agentLimitResetTime":1783841289162}`,
		AgentLimitResetTime: 1783841289162,
	})

	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Qoder agent limit reached; resets at 2026-07-12 15:28:09 Asia/Shanghai", message)
}

func TestQoderGatewayShouldRefreshAccountOnlyForUnwrittenAuthErrors(t *testing.T) {
	handler := &QoderGatewayHandler{
		qoderGatewayService: &service.QoderGatewayService{},
	}

	require.True(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
	require.True(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusForbidden}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusTooManyRequests}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, true))
	require.False(t, (&QoderGatewayHandler{}).shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
}

func TestQoderGatewayStreamingAwareError_ResponsesStreamingEmitsResponseFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpsRequestContext(c, "deepseek-v4-pro", true)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointResponses)

	body := w.Body.String()
	assert.Contains(t, body, "event: response.failed\n")
	assert.NotContains(t, body, `"type":"error"`)
	resp, errObj := parseResponsesFailedSSE(t, body)
	assert.Equal(t, "failed", resp["status"])
	assert.Equal(t, "deepseek-v4-pro", resp["model"])
	assert.Equal(t, "upstream_error", errObj["code"])
	assert.Equal(t, "Upstream request failed", errObj["message"])
}

func TestQoderGatewayStreamingAwareError_MessagesKeepsGenericSSEError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointMessages)

	body := w.Body.String()
	assert.NotContains(t, body, "event: response.failed")
	assert.Contains(t, body, `"type":"error"`)
	assert.Contains(t, body, `"message":"Upstream request failed"`)
}

func TestQoderGatewaySubmitUsageRecordIgnoresRequestCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(reqCtx)

	done := make(chan error, 1)
	h := &QoderGatewayHandler{}
	h.submitUsageRecordTask(c, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			done <- ctx.Err()
		default:
			done <- nil
		}
	})

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("usage task did not run")
	}
}
