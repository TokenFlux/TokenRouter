// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	time "time"
)

// OpenAIUsageOptions 提供供应商探测及影子账号的报文转换端口。
type OpenAIUsageOptions struct {
	Probe  func(context.Context, *Record) (map[string]any, error)
	Shadow func(context.Context, int64, time.Time) (map[string]any, error)
}

func (s *OAuthUsageService) GetOpenAIUsage(ctx context.Context, account *Record, force bool) (*UsageInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	now := s.options.Now()
	usage := &UsageInfo{UpdatedAt: &now}

	if account == nil {
		return usage, nil
	}

	ApplyExtraToUsage(usage, account.Extra, now, s.options.Now)

	if (force || ShouldRefreshOpenAICodexSnapshot(account, usage, now)) && s.ShouldProbeOpenAICodexSnapshot(account.ID, now, force) {
		if account.IsShadow() {
			// 影子账号沿用母账号凭据查询专有窗口，快照仍只写影子行自身。
			if s.options.OpenAI.Shadow != nil {
				if updates, err := s.options.OpenAI.Shadow(ctx, account.ID, now); err == nil && len(updates) > 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					account.Extra = MergeUsageExtra(account.Extra, updates)
					s.PersistOpenAICodexProbeSnapshot(account, updates)
					if usage.UpdatedAt == nil {
						usage.UpdatedAt = &now
					}
					ApplyExtraToUsage(usage, account.Extra, now, s.options.Now)
				}
			}
		} else {
			if updates, err := s.ProbeOpenAICodexSnapshot(ctx, account); err == nil && len(updates) > 0 {
				account.Extra = MergeUsageExtra(account.Extra, updates)
				if usage.UpdatedAt == nil {
					usage.UpdatedAt = &now
				}
				ApplyExtraToUsage(usage, account.Extra, now, s.options.Now)
			}
		}
	}

	if s.stats == nil || s.stats.usageLogRepo == nil {
		return usage, nil
	}

	if stats, err := s.stats.usageLogRepo.GetAccountWindowStats(ctx, account.ID, CodexWindowStatsStart(usage.FiveHour, 5*time.Hour, now)); err == nil {
		if usage.FiveHour == nil {
			usage.FiveHour = &UsageProgress{Utilization: 0}
		}
		usage.FiveHour.WindowStats = normalizedLocalWindowStats(stats)
	}

	if stats, err := s.stats.usageLogRepo.GetAccountWindowStats(ctx, account.ID, CodexWindowStatsStart(usage.SevenDay, 7*24*time.Hour, now)); err == nil {
		if usage.SevenDay == nil {
			usage.SevenDay = &UsageProgress{Utilization: 0}
		}
		usage.SevenDay.WindowStats = normalizedLocalWindowStats(stats)
	}

	return usage, nil
}
func ShouldRefreshOpenAICodexSnapshot(account *Record, usage *UsageInfo, now time.Time) bool {
	if account == nil {
		return false
	}
	if usage == nil {
		return true
	}
	if usage.FiveHour == nil || usage.SevenDay == nil {
		return true
	}
	if account.IsRateLimited() {
		return true
	}
	return isOpenAICodexSnapshotStale(account, now)
}
func isOpenAICodexSnapshotStale(account *Record, now time.Time) bool {
	if account == nil || !account.IsOpenAIOAuth() {
		return false
	}
	// 普通账号的 codex 刷新走 probe(/responses 头),要求 WSv2;但 spark 影子走 QueryUsage
	// (/wham/usage body 的 codex_bengalfox),与 WSv2 无关——不能用 WSv2 门控其 staleness,否则首刷后
	// codex_5h/7d 已存在→staleness 恒 false→spark 窗口永久冻结(外审第9轮 P1)。影子改按
	// codex_usage_updated_at TTL 判定;实际查询频率仍由 ShouldProbeOpenAICodexSnapshot 的缓存 TTL 节流。
	if !account.IsShadow() && !account.IsOpenAIResponsesWebSocketV2Enabled() {
		return false
	}
	if account.Extra == nil {
		return true
	}
	raw, ok := account.Extra["codex_usage_updated_at"]
	if !ok {
		return true
	}
	ts, err := ParseUsageTime(fmt.Sprint(raw))
	if err != nil {
		return true
	}
	return now.Sub(ts) >= OAuthUsageOpenAIProbeCacheTTL
}
func (s *OAuthUsageService) ShouldProbeOpenAICodexSnapshot(accountID int64, now time.Time, force ...bool) bool {
	if s == nil || s.cache == nil || accountID <= 0 {
		return true
	}
	forceProbe := len(force) > 0 && force[0]
	if !forceProbe {
		if cached, ok := s.cache.LoadOpenAIProbe(accountID); ok {
			if ts, ok := cached.(time.Time); ok && now.Sub(ts) < OAuthUsageOpenAIProbeCacheTTL {
				return false
			}
		}
	}
	s.cache.StoreOpenAIProbe(accountID, now)
	return true
}

// ProbeOpenAICodexSnapshot 保留响应 Header 探测和尽力写回，不把额度展示升级为限流状态。
func (s *OAuthUsageService) ProbeOpenAICodexSnapshot(ctx context.Context, value *Record) (map[string]any, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	if s.options.OpenAI.Probe == nil {
		return nil, nil
	}
	updates, err := s.options.OpenAI.Probe(ctx, value)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(updates) > 0 {
		s.PersistOpenAICodexProbeSnapshot(value, updates)
	}
	return CloneValues(updates), nil
}

// PersistOpenAICodexProbeSnapshot 在派发前登记独立五秒写回，关闭时等待且拒绝新增。
func (s *OAuthUsageService) PersistOpenAICodexProbeSnapshot(value *Record, updates map[string]any) {
	if s == nil || s.accountRepo == nil || value == nil || value.ID <= 0 || len(updates) == 0 {
		return
	}
	version := ObserveUsageVersion(value)
	updates = CloneValues(updates)
	writer, ok := s.accountRepo.(UsageExtraWriter)
	if !ok {
		s.options.Warn("openai usage conditional writer is missing")
		return
	}
	ctx, finish, err := s.BeginDetached(context.Background(), 5*time.Second)
	if err != nil {
		return
	}
	go func() {
		defer finish()
		if ctx.Err() == nil {
			_, _ = writer.UpdateUsageExtraIfUnchanged(ctx, version, updates)
		}
	}()
}
