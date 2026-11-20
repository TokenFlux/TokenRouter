package provider

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// oauth_usage_antigravity_test.go verifies Antigravity usage caching,
// cross-credential flight isolation, forced cache-bypass refreshes, and
// family-grouped local WindowStats aggregation.

// TestAntigravitySharedUsageRejectsDifferentIdentity blocks an old-credential
// query while a changed record joins its flight. It checks one fetch,
// successful original completion, and identity-change rejection for the second
// caller.
func TestAntigravitySharedUsageRejectsDifferentIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		entered := make(chan struct{})
		release := make(chan struct{})
		cache := NewOAuthUsageCache()
		core := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{Antigravity: AntigravityUsageOptions{CanFetch: func(*Record) bool { return true }, Enrich: func(*UsageInfo, *Record) {}, Fetch: func(context.Context, *Record) (*UsageInfo, error) {
			calls.Add(1)
			close(entered)
			<-release
			return &UsageInfo{Source: "old"}, nil
		}}})
		old := &Record{ID: 9, Platform: PlatformAntigravity, Type: ProviderTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "old"}}
		first := make(chan error, 1)
		go func() { _, err := core.GetAntigravityUsage(context.Background(), old); first <- err }()
		<-entered
		fresh := CloneRecord(old)
		fresh.Credentials = map[string]any{"access_token": "new"}
		second := make(chan error, 1)
		go func() { _, err := core.GetAntigravityUsage(context.Background(), fresh); second <- err }()
		// Wait for both callers to block before releasing the first fetch. This
		// fixes the cross-credential join so the completed identity check rejects
		// the changed record.
		synctest.Wait()
		require.Equal(t, int32(1), calls.Load())
		close(release)
		require.NoError(t, <-first)
		require.ErrorIs(t, <-second, ErrUsageObservationChanged)
		require.Equal(t, int32(1), calls.Load())
	})
}

// TestGetAntigravityUsage_ForceBypassAndFamilyWindowStats exercises cached
// reads, forced upstream refreshes, and Gemini vs Claude/GPT local WindowStats
// aggregation across shared reset windows.
func TestGetAntigravityUsage_ForceBypassAndFamilyWindowStats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	var fetchCalls atomic.Int32
	var statsCalls atomic.Int32
	cache := NewOAuthUsageCache()

	core := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{
		Now: func() time.Time { return now },
		Antigravity: AntigravityUsageOptions{
			CanFetch: func(*Record) bool { return true },
			Enrich:   func(*UsageInfo, *Record) {},
			Fetch: func(context.Context, *Record) (*UsageInfo, error) {
				n := fetchCalls.Add(1)
				return &UsageInfo{
					UpdatedAt: &now,
					AntigravityQuotaSummary: &AntigravityQuotaSummary{
						Gemini: &AntigravityQuotaGroup{
							Weekly:   &AntigravityModelQuota{Utilization: int(n * 10), ResetTime: "2026-11-25T18:00:00Z"},
							FiveHour: &AntigravityModelQuota{Utilization: int(n * 5), ResetTime: "2026-11-20T22:00:00Z"},
						},
						ClaudeGPT: &AntigravityQuotaGroup{
							Weekly:   &AntigravityModelQuota{Utilization: 50, ResetTime: "2026-11-25T18:00:00Z"},
							FiveHour: &AntigravityModelQuota{Utilization: 0, ResetTime: "2026-11-20T22:00:00Z"},
						},
					},
				}, nil
			},
			ModelTotals: func(_ context.Context, _ int64, start, _ time.Time) ([]AntigravityModelUsage, error) {
				statsCalls.Add(1)
				if now.Sub(start) > 24*time.Hour {
					return []AntigravityModelUsage{
						{Model: "gemini-3.1-pro-high", Requests: 12, TotalTokens: 4500, ProviderCost: 1.25, StandardCost: 1.5, UserCost: 2.0},
						{Model: "claude-sonnet-4-6", Requests: 5, TotalTokens: 2000, ProviderCost: 0.75, StandardCost: 0.9, UserCost: 1.1},
					}, nil
				}
				return []AntigravityModelUsage{
					{Model: "gemini-3-flash", Requests: 3, TotalTokens: 900, ProviderCost: 0.2, StandardCost: 0.25, UserCost: 0.3},
				}, nil
			},
		},
	})

	rec := &Record{
		ID:          42,
		Platform:    PlatformAntigravity,
		Type:        ProviderTypeOAuth,
		Status:      StatusActive,
		Credentials: map[string]any{"access_token": "tok"},
	}

	first, err := core.GetAntigravityUsage(context.Background(), rec)
	require.NoError(t, err)
	require.Equal(t, int32(1), fetchCalls.Load())
	// Shared weekly and 5h reset timestamps deduplicate into two local queries.
	require.Equal(t, int32(2), statsCalls.Load())
	require.Equal(t, 10, first.AntigravityQuotaSummary.Gemini.Weekly.Utilization)
	require.Equal(t, &WindowStats{Requests: 12, Tokens: 4500, Cost: 1.25, StandardCost: 1.5, UserCost: 2.0}, first.AntigravityQuotaSummary.Gemini.Weekly.WindowStats)
	require.Equal(t, &WindowStats{Requests: 3, Tokens: 900, Cost: 0.2, StandardCost: 0.25, UserCost: 0.3}, first.AntigravityQuotaSummary.Gemini.FiveHour.WindowStats)
	require.Equal(t, &WindowStats{Requests: 5, Tokens: 2000, Cost: 0.75, StandardCost: 0.9, UserCost: 1.1}, first.AntigravityQuotaSummary.ClaudeGPT.Weekly.WindowStats)
	require.Equal(t, &WindowStats{}, first.AntigravityQuotaSummary.ClaudeGPT.FiveHour.WindowStats)

	// Unforced read hits upstream cache while still attaching fresh local stats.
	cached, err := core.GetAntigravityUsage(context.Background(), rec, false)
	require.NoError(t, err)
	require.Equal(t, int32(1), fetchCalls.Load())
	require.Equal(t, 10, cached.AntigravityQuotaSummary.Gemini.Weekly.Utilization)
	require.Equal(t, int64(12), cached.AntigravityQuotaSummary.Gemini.Weekly.WindowStats.Requests)

	// Forced read bypasses upstream cache and updates stored utilization.
	forced, err := core.GetAntigravityUsage(context.Background(), rec, true)
	require.NoError(t, err)
	require.Equal(t, int32(2), fetchCalls.Load())
	require.Equal(t, 20, forced.AntigravityQuotaSummary.Gemini.Weekly.Utilization)
}
