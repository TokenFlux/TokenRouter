//go:build unit

package account

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"

	"github.com/stretchr/testify/require"
)

type grokQuotaUsageLogRepo struct {
	stats      *WindowStats
	err        error
	calls      int
	startTimes []time.Time
}

func (r *grokQuotaUsageLogRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*WindowStats, error) {
	r.calls++
	r.startTimes = append(r.startTimes, start)
	return r.stats, r.err
}

func (r *grokQuotaUsageLogRepo) GetAccountTodayStats(context.Context, int64) (*WindowStats, error) {
	return nil, nil
}

func TestGrokLocalUsage24hUsesRollingUTCWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 20, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))

	t.Run("returns usage from exact rolling window", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{stats: &WindowStats{Tokens: 1_250_000}}
		stats := GrokLocalUsage24h(context.Background(), repo, 57, now, slog.Warn)

		require.NotNil(t, stats)
		require.EqualValues(t, 1_250_000, stats.Tokens)
		require.Equal(t, []time.Time{now.UTC().Add(-24 * time.Hour)}, repo.startTimes)
	})

	t.Run("query failure returns no stats", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{err: context.DeadlineExceeded}
		stats := GrokLocalUsage24h(context.Background(), repo, 57, now, slog.Warn)

		require.Nil(t, stats)
		require.Equal(t, []time.Time{now.UTC().Add(-24 * time.Hour)}, repo.startTimes)
	})

	t.Run("missing repository returns no stats", func(t *testing.T) {
		require.Nil(t, GrokLocalUsage24h(context.Background(), nil, 57, now, slog.Warn))
	})

	t.Run("invalid account returns no stats without query", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{}
		require.Nil(t, GrokLocalUsage24h(context.Background(), repo, 0, now, slog.Warn))
		require.Zero(t, repo.calls)
	})
}

func TestGrokLocalUsageForQuotaSelectsFreeOrPaidWindows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	billing := &usageview.BillingSummary{
		PeriodType:         "weekly",
		PeriodStart:        now.Add(-4 * 24 * time.Hour).Format(time.RFC3339),
		PeriodEnd:          now.Add(3 * 24 * time.Hour).Format(time.RFC3339),
		BillingPeriodStart: now.Add(-13 * 24 * time.Hour).Format(time.RFC3339),
		BillingPeriodEnd:   now.Add(17 * 24 * time.Hour).Format(time.RFC3339),
	}

	t.Run("free queries only rolling 24h", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{stats: &WindowStats{Tokens: 500_000}}
		rolling, weekly, monthly := GrokLocalUsageForQuota(context.Background(), repo, 57, billing, now, slog.Warn)

		require.NotNil(t, rolling)
		require.Nil(t, weekly)
		require.Nil(t, monthly)
		require.Equal(t, []time.Time{now.Add(-24 * time.Hour)}, repo.startTimes)
	})

	t.Run("paid queries only billing windows", func(t *testing.T) {
		usagePercent := 25.0
		paidBilling := *billing
		paidBilling.UsagePercent = &usagePercent
		repo := &grokQuotaUsageLogRepo{stats: &WindowStats{Tokens: 500_000}}
		rolling, weekly, monthly := GrokLocalUsageForQuota(context.Background(), repo, 57, &paidBilling, now, slog.Warn)

		require.Nil(t, rolling)
		require.NotNil(t, weekly)
		require.NotNil(t, monthly)
		require.Equal(t, []time.Time{
			now.Add(-4 * 24 * time.Hour),
			now.Add(-13 * 24 * time.Hour),
		}, repo.startTimes)
	})
}

func TestGrokLocalUsageForBillingOnlyReturnsAvailableWindows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	billing := &usageview.BillingSummary{
		PeriodType:  "weekly",
		PeriodStart: now.Add(-4 * 24 * time.Hour).Format(time.RFC3339),
		PeriodEnd:   now.Add(3 * 24 * time.Hour).Format(time.RFC3339),
	}

	t.Run("valid weekly window", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{stats: &WindowStats{Tokens: 1_500_000}}
		weekly, monthly := GrokLocalUsageForBilling(context.Background(), repo, 57, billing, now, slog.Warn)
		require.NotNil(t, weekly)
		require.EqualValues(t, 1_500_000, weekly.Tokens)
		require.Nil(t, monthly)
		require.Equal(t, 1, repo.calls)
	})

	t.Run("query failure", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{err: context.DeadlineExceeded}
		weekly, monthly := GrokLocalUsageForBilling(context.Background(), repo, 57, billing, now, slog.Warn)
		require.Nil(t, weekly)
		require.Nil(t, monthly)
		require.Equal(t, 1, repo.calls)
	})

	t.Run("missing billing window", func(t *testing.T) {
		repo := &grokQuotaUsageLogRepo{}
		weekly, monthly := GrokLocalUsageForBilling(context.Background(), repo, 57, nil, now, slog.Warn)
		require.Nil(t, weekly)
		require.Nil(t, monthly)
		require.Zero(t, repo.calls)
	})
}
