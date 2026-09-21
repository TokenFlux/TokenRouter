package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefreshFailureBlocksUseIndependentDeadlines(t *testing.T) {
	var blocks RefreshFailureBlocks
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	require.False(t, blocks.Blocked(1, now, func() string { t.Fatal("空阻断不应读取凭据"); return "" }))
	blocks.Block(1, "a", now.Add(time.Minute), now)
	blocks.Block(1, "b", now.Add(2*time.Minute), now)
	blocks.Block(1, "a", now.Add(10*time.Second), now)
	require.True(t, blocks.Blocked(1, now.Add(30*time.Second), func() string { return "a" }))
	require.False(t, blocks.Blocked(1, now.Add(time.Minute), func() string { return "a" }))
	require.True(t, blocks.Blocked(1, now.Add(time.Minute), func() string { return "b" }))
	require.False(t, blocks.Blocked(2, now, func() string { t.Fatal("其它账号不应读取身份"); return "b" }))
	require.False(t, blocks.Blocked(1, now.Add(2*time.Minute), func() string { t.Fatal("过期清理后不应读取身份"); return "b" }))
	blocks.Block(1, "c", now.Add(time.Minute), now)
	blocks.Clear(1)
	require.False(t, blocks.Blocked(1, now, func() string { return "c" }))
}
