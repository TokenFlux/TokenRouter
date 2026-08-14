//go:build unit

package service

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestGeminiAggregateUsageUsesAccountCost(t *testing.T) {
	// 用户扣费倍率与账号成本倍率不同时，Gemini 本地用量必须保持账号成本口径。
	stats := []usagestats.ModelStat{
		{
			Model:       "gemini-2.5-pro",
			Requests:    2,
			TotalTokens: 300,
			ActualCost:  500,
			AccountCost: 10,
		},
		{
			Model:       "gemini-2.5-flash",
			Requests:    3,
			TotalTokens: 400,
			ActualCost:  100,
			AccountCost: 2,
		},
	}

	totals := geminiAggregateUsage(stats)

	require.Equal(t, int64(2), totals.ProRequests)
	require.Equal(t, int64(3), totals.FlashRequests)
	require.Equal(t, int64(300), totals.ProTokens)
	require.Equal(t, int64(400), totals.FlashTokens)
	require.InDelta(t, 10, totals.ProCost, 0.000001)
	require.InDelta(t, 2, totals.FlashCost, 0.000001)
}
