package testkit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ParseCompactSSE 把合成的 SSE 文本拆成 (eventType, dataJSON) 序列。
func ParseCompactSSE(t *testing.T, body string) [][2]string {
	t.Helper()
	var events [][2]string
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		lines := strings.Split(block, "\n")
		require.Len(t, lines, 2, "每个 SSE 事件应为 event+data 两行: %q", block)
		require.True(t, strings.HasPrefix(lines[0], "event: "), "缺少 event 行: %q", block)
		require.True(t, strings.HasPrefix(lines[1], "data: "), "缺少 data 行: %q", block)
		events = append(events, [2]string{
			strings.TrimPrefix(lines[0], "event: "),
			strings.TrimPrefix(lines[1], "data: "),
		})
	}
	return events
}
