package ops

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseOpsErrorResponseAndInferStreamStatus checks SSE precedence over JSON,
// response.failed precedence over generic error frames, and status inference.
func TestParseOpsErrorResponseAndInferStreamStatus(t *testing.T) {
	sse := "event: error\n" +
		`data: {"type":"error","error":{"code":"rate_limit_exceeded","message":"first warning"}}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"capacity exhausted"}}}` + "\n\n"

	parsed := ParseOpsErrorResponse([]byte(sse))
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "overloaded_error", parsed.ErrorType)
	require.Equal(t, "server_is_overloaded", parsed.Code)
	require.Equal(t, "capacity exhausted", parsed.Message)
	require.Equal(t, 503, InferStreamFailureStatus(parsed))

	jsonErr := ParseOpsErrorResponse([]byte(`{"error":{"type":"permission_error","code":"GROUP_DELETED","message":"deleted"}}`))
	require.False(t, jsonErr.StreamFailure)
	require.Equal(t, "permission_error", jsonErr.ErrorType)
	require.Equal(t, "GROUP_DELETED", jsonErr.Code)
	require.Equal(t, "deleted", jsonErr.Message)
}

// TestSanitizeOpsSSEDataForPersistenceRedactsAndMarksTruncatedJSON checks that
// complete JSON frames are redacted while truncated JSON fragments are replaced.
func TestSanitizeOpsSSEDataForPersistenceRedactsAndMarksTruncatedJSON(t *testing.T) {
	truncated := []byte("event: response.failed\r\ndata: {\"api_key\":\"sk-secret-value")
	got := SanitizeOpsSSEDataForPersistence(truncated)
	require.Contains(t, got, `data: {"payload_truncated":true}`)
	require.NotContains(t, got, "sk-secret-value")
}

// TestShouldSkipMonitoringAndAdvancedErrorFilters checks final-event skip
// precedence and category-specific advanced log filters.
func TestShouldSkipMonitoringAndAdvancedErrorFilters(t *testing.T) {
	events := []*OpsUpstreamErrorEvent{
		{ProviderID: 1, SkipMonitoring: true},
		{ProviderID: 2, SkipMonitoring: false},
	}
	require.False(t, ShouldSkipMonitoringFromUpstreamEvents(false, events))
	require.True(t, ShouldSkipMonitoringFromUpstreamEvents(true, events))
	require.True(t, ShouldSkipMonitoringFromUpstreamEvents(false, events[:1]))

	settings := OpsAdvancedSettings{
		IgnoreCountTokensErrors:         true,
		IgnoreContextCanceled:           true,
		IgnoreInsufficientBalanceErrors: true,
	}
	require.True(t, ShouldSkipOpsErrorLog(settings, "failed", "", "/v1/messages/count_tokens"))
	require.True(t, ShouldSkipOpsErrorLog(settings, "Context Canceled", "", "/v1/responses"))
	require.True(t, ShouldSkipOpsErrorLog(settings, "", `{"error":"insufficient_quota"}`, "/v1/responses"))
	require.False(t, ShouldSkipOpsErrorLog(settings, "upstream 502", "bad gateway", "/v1/responses"))
}
