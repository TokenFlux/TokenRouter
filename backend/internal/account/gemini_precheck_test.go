package account

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type geminiBatchUsageFixture struct {
	minute time.Time
	calls  [][]int64
}

func (q *geminiBatchUsageFixture) GetModelUsage(context.Context, int64, time.Time, time.Time) ([]GeminiModelUsage, error) {
	panic("批量能力存在时不能退回逐账号查询")
}
func (q *geminiBatchUsageFixture) GetGeminiUsageTotalsBatch(_ context.Context, ids []int64, start, _ time.Time) (map[int64]GeminiUsageTotals, error) {
	q.calls = append(q.calls, append([]int64(nil), ids...))
	out := map[int64]GeminiUsageTotals{}
	if start.Equal(q.minute) {
		out[2] = GeminiUsageTotals{ProRequests: 1}
	} else {
		out[1] = GeminiUsageTotals{ProRequests: 50}
	}
	return out, nil
}

// 日统计缓存与分钟读取保持独立：满日额度不再查分钟，缓存命中也不能吞掉分钟查询。
func TestGeminiPrecheckBatchKeepsQueriesAndDailyCache(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 30, 0, time.UTC)
	queries := &geminiBatchUsageFixture{minute: now.Truncate(time.Minute)}
	core := NewGeminiPrecheck(NewGeminiQuotaService(GeminiQuotaOptions{}), queries, GeminiPrecheckOptions{Now: func() time.Time { return now }, Location: time.UTC})
	values := []*Record{{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey}, {ID: 2, Platform: PlatformGemini, Type: AccountTypeAPIKey}, nil}
	allowed, err := core.PreCheckUsageBatch(context.Background(), values, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, map[int64]bool{1: false, 2: true}, allowed)
	require.Equal(t, [][]int64{{1, 2}, {2}}, queries.calls)
	_, err = core.PreCheckUsageBatch(context.Background(), values, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, [][]int64{{1, 2}, {2}, {2}}, queries.calls)
}
