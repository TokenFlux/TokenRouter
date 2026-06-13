package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/service"
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
