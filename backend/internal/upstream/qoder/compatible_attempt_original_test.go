package qoder

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 原兼容入口的未知错误边界不得被 Chat 策略覆盖。
func TestQoderGatewayShouldFailoverRetryableUpstreamErrors(t *testing.T) {
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusTooManyRequests}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusBadGateway, Code: "115"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusForbidden, Code: "115"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusForbidden, Code: "112"}))
	require.True(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusInternalServerError}))
	require.False(t, MaySwitchCompatibleAttempt(&APIError{StatusCode: http.StatusUnauthorized}))
	require.False(t, MaySwitchCompatibleAttempt(fmt.Errorf("plain error")))
}
