//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// B04：Redis 丢失脚本缓存后，批量读仍须返回真实活动会话，不能静默漏报容量。
func TestS07SessionBatchAfterScriptFlush(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	cache := NewSessionLimitCache(rdb, 5)
	allowed, err := cache.RegisterSession(ctx, 91001, "session-a", 2, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, rdb.ScriptFlush(ctx).Err())

	counts, err := cache.GetActiveSessionCountBatch(ctx, []int64{91001, 91002}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, counts[91001])
	require.Equal(t, 0, counts[91002])

	// 后续单次活动刷新仍使用同一注册项，不新增会话或改变空闲窗口。
	require.NoError(t, cache.RefreshSession(ctx, 91001, "session-a", time.Minute))
	count, err := cache.GetActiveSessionCount(ctx, 91001)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
