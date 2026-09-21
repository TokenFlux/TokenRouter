package ops

import (
	"math"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

func float64Ptr(v float64) *float64 {
	out := v
	return &out
}

func intPtr(v int) *int {
	out := v
	return &out
}

func boolPtr(v bool) *bool {
	out := v
	return &out
}

func truncateString(s string, max int) string { return logredact.TruncateUTF8(s, max) }

// RoundTo1DP 保持观测指标的一位小数舍入，不参与资金量化。
func RoundTo1DP(v float64) float64 {
	return math.Round(v*10) / 10
}
