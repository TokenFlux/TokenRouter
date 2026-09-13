// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	usageview "github.com/TokenFlux/TokenRouter/internal/account/usageview"
	strings "strings"
	time "time"
)

type GrokUsageProbe struct {
	Billing                                        *usageview.BillingSummary
	LocalUsage24h, LocalUsage7d, LocalUsageMonthly *WindowStats
}

// GrokUsageOptions 只提供供应商观测/报文解析和依赖可用性；快照新鲜度与统计组合由核心拥有。
type GrokUsageOptions struct {
	Available      func() bool
	StatsAvailable func() bool
	Probe          func(context.Context, int64) (*GrokUsageProbe, error)
	Build          func(*Record) *UsageInfo
	Enrich         func(*UsageInfo, *Record)
}

const GrokUsageBillingExtraKey = "grok_billing_snapshot"

func (s *OAuthUsageService) GetGrokUsage(ctx context.Context, account *Record, force bool) (*UsageInfo, error) {
	if s.options.Grok.Available == nil || !s.options.Grok.Available() {
		now := s.options.Now()
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	var billingProbeResult *GrokUsageProbe
	if account != nil && account.IsGrokOAuth() && s.options.Grok.Probe != nil && (force || GrokBillingSnapshotNeedsRefresh(account, s.options.Now())) && s.ShouldProbeGrokBilling(account.ID, s.options.Now(), force) {
		result, err := s.options.Grok.Probe(ctx, account.ID)
		if err == nil && result != nil && result.Billing != nil {
			result = cloneGrokUsageProbe(result)
			billingProbeResult = result
			account.Extra = MergeUsageExtra(account.Extra, map[string]any{GrokUsageBillingExtraKey: usageview.CloneBillingSummary(result.Billing)})
		} else if err != nil && force {
			return nil, err
		}
	}
	usage := CloneUsageInfo(s.options.Grok.Build(account))
	if usage.GrokQuotaSnapshotState == "" {
		if usage.ErrorCode == "quota_unknown" {
			usage.GrokQuotaSnapshotState = "unknown_until_first_response"
		} else {
			usage.GrokQuotaSnapshotState = "observed"
		}
	}

	if account != nil {
		if s.options.Grok.StatsAvailable != nil && s.options.Grok.StatsAvailable() {
			if stats, err := s.stats.usageLogRepo.GetAccountTodayStats(ctx, account.ID); err == nil && stats != nil {
				usage.GrokLocalUsage = normalizedLocalWindowStats(stats)
			}
		}
		if billingProbeResult != nil {
			usage.GrokLocalUsage24h = billingProbeResult.LocalUsage24h
			usage.GrokLocalUsage7d = billingProbeResult.LocalUsage7d
			usage.GrokLocalUsageMonthly = billingProbeResult.LocalUsageMonthly
		} else if s.options.Grok.StatsAvailable != nil && s.options.Grok.StatsAvailable() {
			usage.GrokLocalUsage24h, usage.GrokLocalUsage7d, usage.GrokLocalUsageMonthly = GrokLocalUsageForQuota(
				ctx, s.stats.usageLogRepo, account.ID, usage.GrokBilling, s.options.Now().UTC(), s.options.Warn,
			)
		}
		// 将本地窗口统计附加到官方 7 天和 30 天进度条。
		if usage.SevenDay != nil && usage.GrokLocalUsage7d != nil {
			usage.SevenDay.WindowStats = usage.GrokLocalUsage7d
		}
		if usage.ThirtyDay != nil && usage.GrokLocalUsageMonthly != nil {
			usage.ThirtyDay.WindowStats = usage.GrokLocalUsageMonthly
		}
	}

	s.options.Grok.Enrich(usage, account)
	return usage, nil
}

// GrokLocalUsageForQuota 根据 billing 是否提供权威额度选择 Free 或付费统计窗口。
func GrokLocalUsageForQuota(
	ctx context.Context,
	repo LocalUsageStats,
	accountID int64,
	billing *usageview.BillingSummary,
	now time.Time,
	warn func(string, ...any),
) (*WindowStats, *WindowStats, *WindowStats) {
	if GrokBillingHasAuthoritativeQuota(billing) {
		weekly, monthly := GrokLocalUsageForBilling(ctx, repo, accountID, billing, now, warn)
		return nil, weekly, monthly
	}
	return GrokLocalUsage24h(ctx, repo, accountID, now, warn), nil, nil
}

// GrokLocalUsage24h 查询以当前时间为结束点的滚动 24 小时本地用量。
func GrokLocalUsage24h(ctx context.Context, repo LocalUsageStats, accountID int64, now time.Time, warn func(string, ...any)) *WindowStats {
	if repo == nil || accountID <= 0 {
		return nil
	}
	start := now.UTC().Add(-OAuthUsageGrokFreeQuotaWindow)
	stats, err := repo.GetAccountWindowStats(ctx, accountID, start)
	if err != nil {
		warn("grok_rolling_24h_usage_query_failed", "account_id", accountID, "window_start", start, "error", err)
		return nil
	}
	return normalizedLocalWindowStats(stats)
}

func GrokLocalUsageForBilling(
	ctx context.Context,
	repo LocalUsageStats,
	accountID int64,
	billing *usageview.BillingSummary,
	now time.Time,
	warn func(string, ...any),
) (*WindowStats, *WindowStats) {
	var weekly *WindowStats
	var monthly *WindowStats
	if repo == nil || accountID <= 0 {
		return weekly, monthly
	}
	if start, ok := CurrentGrokBillingWindow(billing, true, now); ok {
		if stats, err := repo.GetAccountWindowStats(ctx, accountID, start); err == nil {
			weekly = normalizedLocalWindowStats(stats)
		} else {
			warn("grok_window_usage_query_failed", "account_id", accountID, "window_start", start, "error", err)
		}
	}
	if start, ok := CurrentGrokBillingWindow(billing, false, now); ok {
		if stats, err := repo.GetAccountWindowStats(ctx, accountID, start); err == nil {
			monthly = normalizedLocalWindowStats(stats)
		} else {
			warn("grok_monthly_usage_query_failed", "account_id", accountID, "window_start", start, "error", err)
		}
	}
	return weekly, monthly
}

func CurrentGrokBillingWindow(billing *usageview.BillingSummary, weekly bool, now time.Time) (time.Time, bool) {
	if billing == nil {
		return time.Time{}, false
	}
	startRaw, endRaw := billing.BillingPeriodStart, billing.BillingPeriodEnd
	if weekly {
		if billing.PeriodType != "weekly" {
			return time.Time{}, false
		}
		startRaw, endRaw = billing.PeriodStart, billing.PeriodEnd
	}
	start, startErr := ParseUsageTime(strings.TrimSpace(startRaw))
	end, endErr := ParseUsageTime(strings.TrimSpace(endRaw))
	if startErr != nil || endErr != nil || now.Before(start) || !now.Before(end) {
		return time.Time{}, false
	}
	return start, true
}

func GrokBillingSnapshotNeedsRefresh(account *Record, now time.Time) bool {
	if account == nil {
		return false
	}
	billing, err := ParseGrokBillingSnapshot(account.Extra)
	if err != nil || billing == nil || billing.Partial || len(billing.FailedWindows) > 0 {
		return true
	}
	stamp := strings.TrimSpace(billing.UpdatedAt)
	if stamp == "" {
		stamp = strings.TrimSpace(billing.FetchedAt)
	}
	updatedAt, err := ParseUsageTime(stamp)
	return err != nil || now.Sub(updatedAt) >= OAuthUsageOpenAIProbeCacheTTL
}

func (s *OAuthUsageService) ShouldProbeGrokBilling(accountID int64, now time.Time, force bool) bool {
	if force || s == nil || s.cache == nil || accountID <= 0 {
		return true
	}
	if cached, ok := s.cache.LoadGrokProbe(accountID); ok {
		if ts, ok := cached.(time.Time); ok && now.Sub(ts) < OAuthUsageGrokProbeRetryTTL {
			return false
		}
	}
	s.cache.StoreGrokProbe(accountID, now)
	return true
}

// cloneGrokUsageProbe 隔离供应商共享 flight 的返回值，保持 nil 统计和账单字段。
func cloneGrokUsageProbe(value *GrokUsageProbe) *GrokUsageProbe {
	if value == nil {
		return nil
	}
	out := *value
	out.Billing = usageview.CloneBillingSummary(value.Billing)
	out.LocalUsage24h = clonePointer(value.LocalUsage24h)
	out.LocalUsage7d = clonePointer(value.LocalUsage7d)
	out.LocalUsageMonthly = clonePointer(value.LocalUsageMonthly)
	return &out
}
