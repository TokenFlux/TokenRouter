//go:build unit

package usageprovider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCNParseF64 兼容 JSON 数值与字符串（cc-switch 与上游字段类型不一致）。
func TestCNParseF64(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  any
		want float64
		ok   bool
	}{
		{"float64", float64(12.5), 12.5, true},
		{"int", 100, 100, true},
		{"numeric string", "33.3", 33.3, true},
		{"trim string", "  7  ", 7, true},
		{"non-numeric string", "abc", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := CnParseF64(tc.raw)
			require.Equal(t, tc.ok, ok)
			if ok {
				require.InDelta(t, tc.want, got, 1e-9)
			}
		})
	}
}

// TestCNMillisToRFC3339 秒级（<1e12）按秒、毫秒级按毫秒处理；非正返回空串。
func TestCNMillisToRFC3339(t *testing.T) {
	t.Parallel()
	// 1700000000 秒 = 1700000000000 毫秒
	want := time.UnixMilli(1700000000000).UTC().Format(time.RFC3339)
	require.Equal(t, want, CnMillisToRFC3339(1700000000))    // 秒级
	require.Equal(t, want, CnMillisToRFC3339(1700000000000)) // 毫秒级
	require.Equal(t, "", CnMillisToRFC3339(0))               // 非正
	require.Equal(t, "", CnMillisToRFC3339(-1))
}

// TestCnnormalizeResetTime 覆盖 ISO8601 字符串 / 数字（秒、毫秒）/ 非法输入。
func TestCnnormalizeResetTime(t *testing.T) {
	t.Parallel()
	// ISO8601 字符串归一化为 RFC3339（UTC）。
	require.Equal(t, "2026-08-14T10:00:00Z", CnNormalizeResetTime("2026-08-14T10:00:00Z"))
	// 毫秒级 float64。
	require.Equal(t,
		time.UnixMilli(1700000000000).UTC().Format(time.RFC3339),
		CnNormalizeResetTime(float64(1700000000000)))
	// 非法字符串。
	require.Equal(t, "", CnNormalizeResetTime("not-a-time"))
	require.Equal(t, "", CnNormalizeResetTime(""))
}
