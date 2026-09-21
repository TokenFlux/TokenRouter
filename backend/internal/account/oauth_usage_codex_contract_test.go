package account

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestShouldRefreshOpenAICodexSnapshot(t *testing.T) {
	t.Parallel()

	rateLimitedUntil := time.Now().Add(5 * time.Minute)
	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}

	if !ShouldRefreshOpenAICodexSnapshot(&Record{RateLimitResetAt: &rateLimitedUntil}, usage, now) {
		t.Fatal("expected rate-limited account to force codex snapshot refresh")
	}

	if ShouldRefreshOpenAICodexSnapshot(&Record{}, usage, now) {
		t.Fatal("expected complete non-rate-limited usage to skip codex snapshot refresh")
	}

	if !ShouldRefreshOpenAICodexSnapshot(&Record{}, &UsageInfo{FiveHour: nil, SevenDay: &UsageProgress{}}, now) {
		t.Fatal("expected missing 5h snapshot to require refresh")
	}

	staleAt := now.Add(-(OAuthUsageOpenAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	if !ShouldRefreshOpenAICodexSnapshot(&Record{
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"codex_usage_updated_at":                       staleAt,
		},
	}, usage, now) {
		t.Fatal("expected stale ws snapshot to trigger refresh")
	}
}

// TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2 外审第9轮 P1:spark 影子用量走
// QueryUsage(/wham/usage,与 WSv2 无关),staleness 不得被 WSv2 门控,否则首刷后窗口永久冻结。
func TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2(t *testing.T) {
	t.Parallel()

	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}
	staleAt := now.Add(-(OAuthUsageOpenAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	freshAt := now.Add(-time.Minute).Format(time.RFC3339)
	parentID := int64(7001)

	// 影子无 WSv2,但首刷后窗口已存在;过期 codex_usage_updated_at 必须触发再刷新。
	shadowStale := &Record{
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": staleAt},
	}
	if !ShouldRefreshOpenAICodexSnapshot(shadowStale, usage, now) {
		t.Fatal("expected stale spark shadow (no WSv2) to trigger refresh")
	}

	// 影子时间戳仍新鲜则不刷新，确保 TTL 生效。
	shadowFresh := &Record{
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": freshAt},
	}
	if ShouldRefreshOpenAICodexSnapshot(shadowFresh, usage, now) {
		t.Fatal("expected fresh spark shadow to skip refresh (TTL not elapsed)")
	}

	// 反向对照:普通账号无 WSv2 + 过期时间戳仍不刷新，WSv2 仅门控普通账号的 probe 刷新。
	normalNoWS := &Record{
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra:    map[string]any{"codex_usage_updated_at": staleAt},
	}
	if ShouldRefreshOpenAICodexSnapshot(normalNoWS, usage, now) {
		t.Fatal("expected non-WSv2 normal account to skip codex probe refresh")
	}
}

func TestBuildCodexUsageProgressFromExtra_ZerosExpiredWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)

	t.Run("expired 5h window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     "2026-03-16T10:00:00Z", // 2h ago
		}
		progress := BuildCodexUsageProgressFromExtra(extra, "5h", now, time.Now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired window, got %v", progress.Utilization)
		}
		if progress.RemainingSeconds != 0 {
			t.Fatalf("expected RemainingSeconds=0, got %v", progress.RemainingSeconds)
		}
	})

	t.Run("active 5h window keeps utilization", func(t *testing.T) {
		resetAt := now.Add(2 * time.Hour).Format(time.RFC3339)
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     resetAt,
		}
		progress := BuildCodexUsageProgressFromExtra(extra, "5h", now, time.Now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 42.0 {
			t.Fatalf("expected Utilization=42, got %v", progress.Utilization)
		}
	})

	t.Run("expired 7d window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_7d_used_percent": 88.0,
			"codex_7d_reset_at":     "2026-03-15T00:00:00Z", // yesterday
		}
		progress := BuildCodexUsageProgressFromExtra(extra, "7d", now, time.Now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired 7d window, got %v", progress.Utilization)
		}
	})
}

func TestCodexWindowStatsStart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	window := 5 * time.Hour
	activeReset := now.Add(2 * time.Hour)

	tests := []struct {
		name     string
		progress *UsageProgress
		want     time.Time
	}{
		{
			name:     "active reset window",
			progress: &UsageProgress{ResetsAt: &activeReset},
			want:     activeReset.Add(-window),
		},
		{
			name:     "missing reset falls back",
			progress: &UsageProgress{},
			want:     now.Add(-window),
		},
		{
			name:     "nil progress falls back",
			progress: nil,
			want:     now.Add(-window),
		},
	}

	expiredReset := now.Add(-time.Minute)
	tests = append(tests, struct {
		name     string
		progress *UsageProgress
		want     time.Time
	}{
		name:     "expired reset falls back",
		progress: &UsageProgress{ResetsAt: &expiredReset},
		want:     now.Add(-window),
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodexWindowStatsStart(tt.progress, window, now); !got.Equal(tt.want) {
				t.Fatalf("codexWindowStatsStart() = %v, want %v", got, tt.want)
			}
		})
	}
}
