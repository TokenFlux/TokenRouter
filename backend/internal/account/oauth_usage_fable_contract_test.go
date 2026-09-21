package account

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildUsageInfo_SevenDayFable(t *testing.T) {
	now := time.Now()

	resetAt := now.Add(72 * time.Hour).UTC().Truncate(time.Second)
	var resp ClaudeUsageResponse
	resp.FiveHour.Utilization = 10
	resp.SevenDayOverageIncluded = ClaudeUsageWindow{
		Utilization: 88,
		ResetsAt:    resetAt.Format(time.RFC3339),
	}

	info := BuildUsageInfo(&resp, &now, time.Now, log.Printf)
	require.NotNil(t, info.SevenDayFable)
	require.Equal(t, 88.0, info.SevenDayFable.Utilization)
	require.NotNil(t, info.SevenDayFable.ResetsAt)
	require.True(t, info.SevenDayFable.ResetsAt.Equal(resetAt))
	require.Greater(t, info.SevenDayFable.RemainingSeconds, 0)

	// 无 Fable 数据时不应创建窗口
	var empty ClaudeUsageResponse
	empty.FiveHour.Utilization = 10
	info = BuildUsageInfo(&empty, &now, time.Now, log.Printf)
	require.Nil(t, info.SevenDayFable)
}

func TestBuildPassiveUsageWindow(t *testing.T) {
	future := time.Now().Add(48 * time.Hour).Unix()

	t.Run("utilization and reset", func(t *testing.T) {
		window := BuildPassiveUsageWindow(map[string]any{
			"passive_usage_7d_oi_utilization": 0.87,
			"passive_usage_7d_oi_reset":       float64(future),
		}, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", time.Now)
		require.NotNil(t, window)
		require.InDelta(t, 87.0, window.Utilization, 1e-9)
		require.NotNil(t, window.ResetsAt)
		require.Equal(t, future, window.ResetsAt.Unix())
		require.Greater(t, window.RemainingSeconds, 0)
	})

	t.Run("no data returns nil", func(t *testing.T) {
		require.Nil(t, BuildPassiveUsageWindow(nil, "u", "r", time.Now))
		require.Nil(t, BuildPassiveUsageWindow(map[string]any{}, "u", "r", time.Now))
	})

	t.Run("expired reset clamps remaining to zero", func(t *testing.T) {
		past := time.Now().Add(-time.Hour).Unix()
		window := BuildPassiveUsageWindow(map[string]any{
			"u": 0.5,
			"r": float64(past),
		}, "u", "r", time.Now)
		require.NotNil(t, window)
		require.Equal(t, 0, window.RemainingSeconds)
	})

	t.Run("utilization only", func(t *testing.T) {
		window := BuildPassiveUsageWindow(map[string]any{"u": 0.25}, "u", "r", time.Now)
		require.NotNil(t, window)
		require.InDelta(t, 25.0, window.Utilization, 1e-9)
		require.Nil(t, window.ResetsAt)
	})
}

func TestSyncActiveToPassive_WritesFableExtras(t *testing.T) {
	repo := &usageFableWriteFixture{updates: make(chan map[string]any, 1)}
	svc := NewOAuthUsageService(repo, nil, nil, OAuthUsageOptions{})

	resetAt := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	usage := &UsageInfo{
		SevenDayFable: &UsageProgress{
			Utilization: 87,
			ResetsAt:    &resetAt,
		},
	}

	svc.SyncActiveToPassive(t.Context(), &Record{ID: 1}, usage)

	select {
	case updates := <-repo.updates:
		require.InDelta(t, 0.87, updates["passive_usage_7d_oi_utilization"], 1e-9)
		require.Equal(t, resetAt.Unix(), updates["passive_usage_7d_oi_reset"])
		require.Contains(t, updates, "passive_usage_sampled_at")
	default:
		t.Fatal("expected UpdateExtra to be called with fable extras")
	}
}

// 写入替身仅记录真实核心输出，不复制窗口计算或同步规则。
type usageFableWriteFixture struct {
	OAuthUsageReader
	updates chan map[string]any
}

func (r *usageFableWriteFixture) UpdateUsageExtraIfUnchanged(_ context.Context, _ UsageObservationVersion, updates map[string]any) (bool, error) {
	r.updates <- CloneValues(updates)
	return true, nil
}
