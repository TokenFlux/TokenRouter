package provider

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

func TestQoderGatewayErrorDetailsUsesAPIErrorMessage(t *testing.T) {
	err := fmt.Errorf("forward qoder: %w", &qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "101",
		Message:    "Signature invalid",
	})

	status, errType, message, ok := qoderErrorDetailsForContract(err)
	require.True(t, ok)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", message)
}

func TestQoderGatewayErrorDetailsKeepsEntitlementDeniedAsForbidden(t *testing.T) {
	status, errType, message, ok := qoderErrorDetailsForContract(&qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "112",
		Message:    "model not available for this account",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 112: model not available for this account", message)
}

func TestQoderGatewayErrorDetailsMapsRateLimit(t *testing.T) {
	status, errType, message, ok := qoderErrorDetailsForContract(&qoder.APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "Too many requests",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Too many requests", message)
}

func TestQoderGatewayErrorDetailsMapsAgentLimitToRateLimit(t *testing.T) {
	status, errType, message, ok := qoderErrorDetailsForContract(&qoder.APIError{
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

// qoderErrorDetailsForContract 只展开原生投影，保留原业务断言形状。
func qoderErrorDetailsForContract(err error) (int, string, string, bool) {
	value := DescribeQoderError(err)
	return value.Status, value.Kind, value.Message, value.Recognized
}
