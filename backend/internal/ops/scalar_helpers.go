package ops

import (
	"math"
	"unicode/utf8"
)

func float64Ptr(v float64) *float64 {
	out := v
	return &out
}
func CompatFloat64Ptr(v float64) *float64 { return float64Ptr(v) }
func intPtr(v int) *int {
	out := v
	return &out
}
func CompatIntPtr(v int) *int { return intPtr(v) }
func boolPtr(v bool) *bool {
	out := v
	return &out
}
func CompatBoolPtr(v bool) *bool { return boolPtr(v) }
func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
func CompatTruncateString(s string, max int) string { return truncateString(s, max) }
func roundTo1DP(v float64) float64 {
	return math.Round(v*10) / 10
}
func CompatRoundTo1DP(v float64) float64 { return roundTo1DP(v) }
