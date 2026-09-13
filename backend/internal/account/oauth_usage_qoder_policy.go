// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
	time "time"
)

func QoderUsageCacheTTL(info *UsageInfo) time.Duration {
	if info == nil || info.Error != "" || info.ErrorCode != "" {
		return OAuthUsageAntigravityErrorTTL
	}
	return OAuthUsageAPICacheTTL
}

func QoderUsageCacheUsable(account *Record, cache *OAuthQoderUsageCache, now time.Time) bool {
	if cache == nil || cache.UsageInfo == nil || cache.Identity != UsageCacheIdentity(account) {
		return false
	}
	if now.Sub(cache.Timestamp) >= QoderUsageCacheTTL(cache.UsageInfo) {
		return false
	}
	if cache.UsageInfo.Error != "" || cache.UsageInfo.ErrorCode != "" {
		return true
	}
	return !QoderAccountRateLimitMatchesUsageQuota(account, cache.UsageInfo, now)
}

func QoderAccountRateLimitMatchesUsageQuota(account *Record, usage *UsageInfo, now time.Time) bool {
	if account == nil || usage == nil || usage.QoderQuota == nil || account.RateLimitResetAt == nil || !account.RateLimitResetAt.After(now) {
		return false
	}
	return QoderQuotaRateLimitResetMatches(account.RateLimitResetAt, usage.QoderQuota.ExpiresAt)
}

func QoderQuotaShouldClearRateLimit(account *Record, quota *QoderQuotaInfo, now time.Time) bool {
	if account == nil || quota == nil || quota.ExpiresAt == nil || account.RateLimitResetAt == nil || !account.RateLimitResetAt.After(now) {
		return false
	}
	if account.OverloadUntil != nil && account.OverloadUntil.After(now) {
		return false
	}
	if QoderQuotaRateLimitResetMatches(account.RateLimitResetAt, quota.ExpiresAt) {
		_, limited := QoderQuotaRateLimitResetAt(quota, now)
		return !limited
	}
	return false
}

func QoderQuotaRateLimitResetMatches(accountResetAt, quotaExpiresAt *time.Time) bool {
	if accountResetAt == nil || quotaExpiresAt == nil {
		return false
	}
	delta := accountResetAt.Sub(*quotaExpiresAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= 2*time.Second
}

func QoderQuotaRateLimitResetAt(quota *QoderQuotaInfo, now time.Time) (time.Time, bool) {
	if quota == nil || quota.ExpiresAt == nil || !quota.ExpiresAt.After(now) {
		return time.Time{}, false
	}
	if QoderQuotaIsPersonalZeroQuota(quota) {
		return time.Time{}, false
	}
	if remaining, ok := QoderQuotaTotalRemaining(quota); ok {
		if remaining > 0 {
			return time.Time{}, false
		}
		if quota.IsQuotaExceeded || QoderQuotaTotalCapacity(quota) > 0 {
			return *quota.ExpiresAt, true
		}
		return time.Time{}, false
	}
	if quota.IsQuotaExceeded {
		return *quota.ExpiresAt, true
	}
	if quota.UserQuota != nil && quota.UserQuota.Total > 0 && quota.UserQuota.Remaining <= 0 {
		return *quota.ExpiresAt, true
	}
	return time.Time{}, false
}

func QoderQuotaIsPersonalZeroQuota(quota *QoderQuotaInfo) bool {
	if quota == nil || quota.UserQuota == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(quota.UserType), "personal_standard") &&
		quota.UserQuota.Total <= 0 &&
		quota.UserQuota.Remaining <= 0 &&
		QoderQuotaTotalCapacity(quota) <= 0 &&
		QoderQuotaPositiveRemaining(quota) <= 0
}

func QoderQuotaTotalRemaining(quota *QoderQuotaInfo) (float64, bool) {
	if quota == nil {
		return 0, false
	}
	total := 0.0
	known := false
	for _, progress := range []*QoderQuotaProgress{quota.UserQuota, quota.AddOnQuota, quota.OrgResourcePackage} {
		if progress == nil {
			continue
		}
		known = true
		total += progress.Remaining
	}
	return total, known
}

func QoderQuotaPositiveRemaining(quota *QoderQuotaInfo) float64 {
	remaining, ok := QoderQuotaTotalRemaining(quota)
	if !ok || remaining < 0 {
		return 0
	}
	return remaining
}

func QoderQuotaTotalCapacity(quota *QoderQuotaInfo) float64 {
	if quota == nil {
		return 0
	}
	total := 0.0
	for _, progress := range []*QoderQuotaProgress{quota.UserQuota, quota.AddOnQuota, quota.OrgResourcePackage} {
		if progress == nil {
			continue
		}
		if progress.Total > 0 {
			total += progress.Total
			continue
		}
		if progress.Cap > 0 {
			total += progress.Cap
		}
	}
	return total
}
