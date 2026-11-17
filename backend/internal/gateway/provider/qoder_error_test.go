package provider

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// TestQoderGatewayErrorDetailsUsesAPIErrorMessage wraps a Qoder signature
// rejection in another error. Classification recognizes it as unauthorized and
// retains the Qoder code and message in the gateway error text.
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

// TestQoderGatewayErrorDetailsKeepsEntitlementDeniedAsForbidden supplies a
// Qoder model-entitlement rejection with status 403. The gateway retains
// forbidden status, upstream error kind, and the provider's code and message.
func TestQoderGatewayErrorDetailsKeepsEntitlementDeniedAsForbidden(t *testing.T) {
	status, errType, message, ok := qoderErrorDetailsForContract(&qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "112",
		Message:    "model not available for this provider",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 112: model not available for this provider", message)
}

// TestQoderGatewayErrorDetailsMapsRateLimit supplies a Qoder HTTP 429 with a
// plain message. The gateway recognizes the API error, returns
// rate_limit_error, and retains the supplied text.
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

// TestQoderGatewayErrorDetailsMapsAgentLimitToRateLimit supplies a Qoder
// agent-limit code and millisecond reset timestamp under an upstream 502.
// Classification returns HTTP 429 and a reset message formatted in
// Asia/Shanghai.
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

// qoderErrorDetailsForContract exposes classified status, kind, message, and
// recognition for test assertions.
func qoderErrorDetailsForContract(err error) (int, string, string, bool) {
	value := DescribeQoderError(err)
	return value.Status, value.Kind, value.Message, value.Recognized
}

// TestLocalQoderValidationPreservesClientStatus distinguishes local request failures
// from remote HTTP failures. Neither recovery path should retry these requests
// or attribute the diagnostic to a response from the provider.
func TestLocalQoderValidationPreservesClientStatus(t *testing.T) {
	for _, code := range []string{"invalid_reasoning_parameter", "unsupported_response_state"} {
		err := fmt.Errorf("prepare: %w", &qoder.RequestError{Code: code, Message: "invalid request"})
		view := DescribeQoderError(err)
		require.True(t, view.Recognized)
		require.Equal(t, http.StatusBadRequest, view.Status)
		require.Equal(t, "invalid_request_error", view.Kind)
		require.Zero(t, view.SourceStatus)
		require.False(t, qoder.MaySwitchAttempt(err))
		require.False(t, qoder.MaySwitchCompatibleAttempt(err))
		require.False(t, qoder.MayRefreshAttempt(err))
	}
}
