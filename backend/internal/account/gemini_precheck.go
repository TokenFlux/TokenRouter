// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	sync "sync"
	time "time"
)

// GeminiQuotaUsageReader 只读账号配额预检所需的本地模型用量，SQL 归 S08。
type GeminiQuotaUsageReader interface {
	GetModelUsage(context.Context, int64, time.Time, time.Time) ([]GeminiModelUsage, error)
}
type GeminiPrecheckOptions struct {
	Now      func() time.Time
	Location *time.Location
	Info     func(string, ...any)
}

// GeminiPrecheck 保留独立的日统计短缓存；预检不写账号限流或用户资金。
type GeminiPrecheck struct {
	usageRepo          GeminiQuotaUsageReader
	geminiQuotaService *GeminiQuotaService
	options            GeminiPrecheckOptions
	usageCacheMu       sync.RWMutex
	usageCache         map[int64]*geminiUsageCacheEntry
}

func NewGeminiPrecheck(policy *GeminiQuotaService, usage GeminiQuotaUsageReader, options GeminiPrecheckOptions) *GeminiPrecheck {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Info == nil {
		options.Info = func(string, ...any) {}
	}
	if options.Location == nil {
		options.Location = time.FixedZone("PST", -8*3600)
	}
	return &GeminiPrecheck{geminiQuotaService: policy, usageRepo: usage, options: options, usageCache: make(map[int64]*geminiUsageCacheEntry)}
}

type geminiUsageCacheEntry struct {
	windowStart time.Time
	cachedAt    time.Time
	totals      GeminiUsageTotals
}

type GeminiUsageTotalsBatchReader interface {
	GetGeminiUsageTotalsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[int64]GeminiUsageTotals, error)
}

const geminiPrecheckCacheTTL = time.Minute

// PreCheckUsage 在派发前执行原本地配额预检。
// 返回 false 表示当前账号应被跳过。
func (s *GeminiPrecheck) PreCheckUsage(ctx context.Context, account *Record, requestedModel string) (bool, error) {
	if account == nil || account.Platform != PlatformGemini {
		return true, nil
	}
	if s.usageRepo == nil || s.geminiQuotaService == nil {
		return true, nil
	}

	quota, ok := s.geminiQuotaService.QuotaForAccount(ctx, account)
	if !ok {
		return true, nil
	}

	now := s.options.Now()
	modelClass := GeminiModelClassFromName(requestedModel)

	// 日配额按洛杉矶当地日界检查。
	{
		var limit int64
		if quota.SharedRPD > 0 {
			limit = quota.SharedRPD
		} else {
			switch modelClass {
			case GeminiModelFlash:
				limit = quota.FlashRPD
			default:
				limit = quota.ProRPD
			}
		}

		if limit > 0 {
			start := GeminiDailyWindowStart(now, s.options.Location)
			totals, ok := s.getGeminiUsageTotals(account.ID, start, now)
			if !ok {
				stats, err := s.usageRepo.GetModelUsage(ctx, account.ID, start, now)
				if err != nil {
					return true, err
				}
				totals = AggregateGeminiUsage(stats)
				s.setGeminiUsageTotals(account.ID, start, now, totals)
			}

			var used int64
			if quota.SharedRPD > 0 {
				used = totals.ProRequests + totals.FlashRequests
			} else {
				switch modelClass {
				case GeminiModelFlash:
					used = totals.FlashRequests
				default:
					used = totals.ProRequests
				}
			}

			if used >= limit {
				resetAt := GeminiDailyResetTime(now, s.options.Location)
				// 保留预检与真实上游限流的边界：
				// 本地预检只减少上游 429。
				// 不写账号限流，rate_limit_reset_at 仍只反映真实上游 429。
				s.options.Info("gemini_precheck_daily_quota_reached", "account_id", account.ID, "used", used, "limit", limit, "reset_at", resetAt)
				return false, nil
			}
		}
	}

	// 分钟配额使用当前固定分钟窗口。
	{
		var limit int64
		if quota.SharedRPM > 0 {
			limit = quota.SharedRPM
		} else {
			switch modelClass {
			case GeminiModelFlash:
				limit = quota.FlashRPM
			default:
				limit = quota.ProRPM
			}
		}

		if limit > 0 {
			start := now.Truncate(time.Minute)
			stats, err := s.usageRepo.GetModelUsage(ctx, account.ID, start, now)
			if err != nil {
				return true, err
			}
			totals := AggregateGeminiUsage(stats)

			var used int64
			if quota.SharedRPM > 0 {
				used = totals.ProRequests + totals.FlashRequests
			} else {
				switch modelClass {
				case GeminiModelFlash:
					used = totals.FlashRequests
				default:
					used = totals.ProRequests
				}
			}

			if used >= limit {
				resetAt := start.Add(time.Minute)
				// 本地分钟预检同样不持久化账号限流状态。
				s.options.Info("gemini_precheck_minute_quota_reached", "account_id", account.ID, "used", used, "limit", limit, "reset_at", resetAt)
				return false, nil
			}
		}
	}

	return true, nil
}

// PreCheckUsageBatch 保留单次请求的批量账号预检。
// 返回表中的 false 表示跳过该账号。
func (s *GeminiPrecheck) PreCheckUsageBatch(ctx context.Context, accounts []*Record, requestedModel string) (map[int64]bool, error) {
	result := make(map[int64]bool, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		result[account.ID] = true
	}

	if len(accounts) == 0 || requestedModel == "" {
		return result, nil
	}
	if s.usageRepo == nil || s.geminiQuotaService == nil {
		return result, nil
	}

	modelClass := GeminiModelClassFromName(requestedModel)
	now := s.options.Now()
	dailyStart := GeminiDailyWindowStart(now, s.options.Location)
	minuteStart := now.Truncate(time.Minute)

	type quotaAccount struct {
		account *Record
		quota   GeminiQuota
	}
	quotaAccounts := make([]quotaAccount, 0, len(accounts))
	for _, account := range accounts {
		if account == nil || account.Platform != PlatformGemini {
			continue
		}
		quota, ok := s.geminiQuotaService.QuotaForAccount(ctx, account)
		if !ok {
			continue
		}
		quotaAccounts = append(quotaAccounts, quotaAccount{
			account: account,
			quota:   quota,
		})
	}
	if len(quotaAccounts) == 0 {
		return result, nil
	}

	// 日配额优先短缓存，未命中时批量回源。
	dailyTotalsByID := make(map[int64]GeminiUsageTotals, len(quotaAccounts))
	dailyMissIDs := make([]int64, 0, len(quotaAccounts))
	for _, item := range quotaAccounts {
		limit := geminiDailyLimit(item.quota, modelClass)
		if limit <= 0 {
			continue
		}
		accountID := item.account.ID
		if totals, ok := s.getGeminiUsageTotals(accountID, dailyStart, now); ok {
			dailyTotalsByID[accountID] = totals
			continue
		}
		dailyMissIDs = append(dailyMissIDs, accountID)
	}
	if len(dailyMissIDs) > 0 {
		totalsBatch, err := s.getGeminiUsageTotalsBatch(ctx, dailyMissIDs, dailyStart, now)
		if err != nil {
			return result, err
		}
		for _, accountID := range dailyMissIDs {
			totals := totalsBatch[accountID]
			dailyTotalsByID[accountID] = totals
			s.setGeminiUsageTotals(accountID, dailyStart, now, totals)
		}
	}
	for _, item := range quotaAccounts {
		limit := geminiDailyLimit(item.quota, modelClass)
		if limit <= 0 {
			continue
		}
		accountID := item.account.ID
		used := geminiUsedRequests(item.quota, modelClass, dailyTotalsByID[accountID], true)
		if used >= limit {
			resetAt := GeminiDailyResetTime(now, s.options.Location)
			s.options.Info("gemini_precheck_daily_quota_reached_batch", "account_id", accountID, "used", used, "limit", limit, "reset_at", resetAt)
			result[accountID] = false
		}
	}

	// 2) Minute precheck (batch DB)
	minuteIDs := make([]int64, 0, len(quotaAccounts))
	for _, item := range quotaAccounts {
		accountID := item.account.ID
		if !result[accountID] {
			continue
		}
		if geminiMinuteLimit(item.quota, modelClass) <= 0 {
			continue
		}
		minuteIDs = append(minuteIDs, accountID)
	}
	if len(minuteIDs) == 0 {
		return result, nil
	}

	minuteTotalsByID, err := s.getGeminiUsageTotalsBatch(ctx, minuteIDs, minuteStart, now)
	if err != nil {
		return result, err
	}
	for _, item := range quotaAccounts {
		accountID := item.account.ID
		if !result[accountID] {
			continue
		}

		limit := geminiMinuteLimit(item.quota, modelClass)
		if limit <= 0 {
			continue
		}

		used := geminiUsedRequests(item.quota, modelClass, minuteTotalsByID[accountID], false)
		if used >= limit {
			resetAt := minuteStart.Add(time.Minute)
			s.options.Info("gemini_precheck_minute_quota_reached_batch", "account_id", accountID, "used", used, "limit", limit, "reset_at", resetAt)
			result[accountID] = false
		}
	}

	return result, nil
}

func (s *GeminiPrecheck) getGeminiUsageTotalsBatch(ctx context.Context, accountIDs []int64, start, end time.Time) (map[int64]GeminiUsageTotals, error) {
	result := make(map[int64]GeminiUsageTotals, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}

	ids := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		ids = append(ids, accountID)
	}
	if len(ids) == 0 {
		return result, nil
	}

	if batchReader, ok := s.usageRepo.(GeminiUsageTotalsBatchReader); ok {
		stats, err := batchReader.GetGeminiUsageTotalsBatch(ctx, ids, start, end)
		if err != nil {
			return nil, err
		}
		for _, accountID := range ids {
			result[accountID] = stats[accountID]
		}
		return result, nil
	}

	for _, accountID := range ids {
		stats, err := s.usageRepo.GetModelUsage(ctx, accountID, start, end)
		if err != nil {
			return nil, err
		}
		result[accountID] = AggregateGeminiUsage(stats)
	}
	return result, nil
}

func geminiDailyLimit(quota GeminiQuota, modelClass GeminiModelClass) int64 {
	if quota.SharedRPD > 0 {
		return quota.SharedRPD
	}
	switch modelClass {
	case GeminiModelFlash:
		return quota.FlashRPD
	default:
		return quota.ProRPD
	}
}

func geminiMinuteLimit(quota GeminiQuota, modelClass GeminiModelClass) int64 {
	if quota.SharedRPM > 0 {
		return quota.SharedRPM
	}
	switch modelClass {
	case GeminiModelFlash:
		return quota.FlashRPM
	default:
		return quota.ProRPM
	}
}

func geminiUsedRequests(quota GeminiQuota, modelClass GeminiModelClass, totals GeminiUsageTotals, daily bool) int64 {
	if daily {
		if quota.SharedRPD > 0 {
			return totals.ProRequests + totals.FlashRequests
		}
	} else {
		if quota.SharedRPM > 0 {
			return totals.ProRequests + totals.FlashRequests
		}
	}
	switch modelClass {
	case GeminiModelFlash:
		return totals.FlashRequests
	default:
		return totals.ProRequests
	}
}

func (s *GeminiPrecheck) getGeminiUsageTotals(accountID int64, windowStart, now time.Time) (GeminiUsageTotals, bool) {
	s.usageCacheMu.RLock()
	defer s.usageCacheMu.RUnlock()

	if s.usageCache == nil {
		return GeminiUsageTotals{}, false
	}

	entry, ok := s.usageCache[accountID]
	if !ok || entry == nil {
		return GeminiUsageTotals{}, false
	}
	if !entry.windowStart.Equal(windowStart) {
		return GeminiUsageTotals{}, false
	}
	if now.Sub(entry.cachedAt) >= geminiPrecheckCacheTTL {
		return GeminiUsageTotals{}, false
	}
	return entry.totals, true
}

func (s *GeminiPrecheck) setGeminiUsageTotals(accountID int64, windowStart, now time.Time, totals GeminiUsageTotals) {
	s.usageCacheMu.Lock()
	defer s.usageCacheMu.Unlock()
	if s.usageCache == nil {
		s.usageCache = make(map[int64]*geminiUsageCacheEntry)
	}
	s.usageCache[accountID] = &geminiUsageCacheEntry{
		windowStart: windowStart,
		cachedAt:    now,
		totals:      totals,
	}
}

// GeminiCooldown 根据等级保留 Gemini 429 的原回退冷却。
func (s *GeminiPrecheck) GeminiCooldown(ctx context.Context, account *Record) time.Duration {
	if account == nil {
		return 5 * time.Minute
	}
	if s.geminiQuotaService == nil {
		return 5 * time.Minute
	}
	return s.geminiQuotaService.CooldownForAccount(ctx, account)
}
