package billing

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 非正数与非数值限额保持旧严格正数判断，不触发窗口消费重置。
func TestFixedAccountWindowRequiresStrictlyPositiveLimit(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, limit := range []float64{0, -1, math.NaN()} {
		_, reset := ReconcileAccountFixedWindow(FixedAccountWindow{Mode: "fixed", Limit: limit}, time.Time{}, time.UTC, now)
		require.False(t, reset)
	}
	boundary, reset := ReconcileAccountFixedWindow(FixedAccountWindow{Mode: "fixed", Limit: 1, Hour: 0}, time.Time{}, time.UTC, now)
	require.True(t, reset)
	require.Equal(t, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), boundary)
	_, reset = ReconcileAccountFixedWindow(FixedAccountWindow{Mode: "fixed", Limit: 1}, boundary, time.UTC, now)
	require.False(t, reset, "窗口起点恰在边界时保留消费")
}
