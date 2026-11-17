package provider

import (
	"strings"
	"time"
)

// oauth_usage_qoder_policy.go evaluates Qoder cache reuse and scheduling
// pauses from quota-cycle observations.
// Qoder cached display data is reusable only for matching credentials and a
// still-fresh observation. Success cache is bypassed when a live provider rate
// limit matches the quota cycle, allowing another query to reconsider
// exhaustion; cached error diagnostics instead remain usable during their
// shorter lifetime.
//
// Scheduling decisions combine cycle expiry, quota flags, and
// personal/add-on/organization balances. Positive summed remaining overrides
// an exhausted flag, while nonpositive remaining with capacity or exhaustion
// can pause until cycle expiry. Zero-capacity personal-standard observations
// are exempt. Recovery clears only a matching live cycle and preserves active
// overload. All calculations read supplied scalar quota values; negative pools
// can offset positive pools, and finite numeric input remains the
// responsibility of the observation producer.

// QoderUsageCacheTTL selects one minute for nil or diagnostic error views and
// three minutes for other views. ErrorCode and Error independently shorten
// reuse, leaving their classification to the query producer.
func QoderUsageCacheTTL(info *UsageInfo) time.Duration {
	if info == nil || info.Error != "" || info.ErrorCode != "" {
		return OAuthUsageAntigravityErrorTTL
	}
	return OAuthUsageAPICacheTTL
}

// QoderUsageCacheUsable requires nonnil display, matching identity, and age
// below its status-dependent lifetime. Error diagnostics remain reusable after
// those checks, while successful quota views are bypassed when a live provider
// pause matches their cycle.
//
// That bypass allows fresh quota to reconsider a pause still associated with
// the same expiry. An unrelated provider rate limit leaves the successful
// cache usable. Future observation timestamps also pass the elapsed-age gate,
// and the operation reserves no query work; callers synchronize mutable
// records and cache entries while checking them.
func QoderUsageCacheUsable(provider *Record, cache *OAuthQoderUsageCache, now time.Time) bool {
	if cache == nil || cache.UsageInfo == nil || cache.Identity != UsageCacheIdentity(provider) {
		return false
	}
	if now.Sub(cache.Timestamp) >= QoderUsageCacheTTL(cache.UsageInfo) {
		return false
	}
	if cache.UsageInfo.Error != "" || cache.UsageInfo.ErrorCode != "" {
		return true
	}
	return !QoderProviderRateLimitMatchesUsageQuota(provider, cache.UsageInfo, now)
}

// QoderProviderRateLimitMatchesUsageQuota checks a live provider pause against
// quota expiry. Nil or missing inputs return false; a matching reset within
// the helper’s two-second tolerance associates that pause with this displayed
// quota cycle.
func QoderProviderRateLimitMatchesUsageQuota(provider *Record, usage *UsageInfo, now time.Time) bool {
	if provider == nil || usage == nil || usage.QoderQuota == nil || provider.RateLimitResetAt == nil || !provider.RateLimitResetAt.After(now) {
		return false
	}
	return QoderQuotaRateLimitResetMatches(provider.RateLimitResetAt, usage.QoderQuota.ExpiresAt)
}

// QoderQuotaShouldClearRateLimit reports whether a live observed quota-cycle
// pause may recover. Provider, quota expiry, and future rate-limit reset are
// required, and an active overload keeps the pause.
//
// The reset must match quota expiry within two seconds before current
// exhaustion is reconsidered. A future positive balance can make that cycle
// usable, while the same exhausted quota retains its pause. Expired quota also
// stops selecting a new exhaustion reset. This predicate performs no write;
// applyQoderQuotaSchedulingSignal submits the captured record version to the
// conditional clearer.
func QoderQuotaShouldClearRateLimit(provider *Record, quota *QoderQuotaInfo, now time.Time) bool {
	if provider == nil || quota == nil || quota.ExpiresAt == nil || provider.RateLimitResetAt == nil || !provider.RateLimitResetAt.After(now) {
		return false
	}
	if provider.OverloadUntil != nil && provider.OverloadUntil.After(now) {
		return false
	}
	if QoderQuotaRateLimitResetMatches(provider.RateLimitResetAt, quota.ExpiresAt) {
		_, limited := QoderQuotaRateLimitResetAt(quota, now)
		return !limited
	}
	return false
}

// QoderQuotaRateLimitResetMatches compares nonnil times within an inclusive
// two-second tolerance when their difference fits time.Duration. Missing time
// reports false; callers keep that difference representable. A saturated
// minimum negative duration remains negative after negation and can therefore
// pass the tolerance test despite a much larger separation.
func QoderQuotaRateLimitResetMatches(providerResetAt, quotaExpiresAt *time.Time) bool {
	if providerResetAt == nil || quotaExpiresAt == nil {
		return false
	}
	// Sub saturates differences outside duration range. Negation of its minimum
	// value also overflows, so the caller-provided times need a representable
	// difference for this absolute-distance comparison.
	delta := providerResetAt.Sub(*quotaExpiresAt)
	if delta < 0 {
		delta = -delta
	}
	// Permit two seconds of timestamp variation when associating a stored
	// provider pause with the quota cycle used for conditional scheduling
	// recovery.
	return delta <= 2*time.Second
}

// QoderQuotaRateLimitResetAt selects a future cycle expiry when current quota
// indicates exhaustion. Nil or elapsed expiry and exempt zero-capacity
// personal-standard data report no reset.
//
// When any pool is present, its summed remaining balance wins first: positive
// sum clears exhaustion even with IsQuotaExceeded. Nonpositive sum pauses only
// with explicit exhaustion or positive total capacity. When every pool is
// absent, only IsQuotaExceeded can select the cycle; the later personal-pool
// branch is unreachable under that absence. Numeric producers supply finite
// values so comparisons and sums have meaningful outcomes.
func QoderQuotaRateLimitResetAt(quota *QoderQuotaInfo, now time.Time) (time.Time, bool) {
	if quota == nil || quota.ExpiresAt == nil || !quota.ExpiresAt.After(now) {
		return time.Time{}, false
	}
	if quota.DisplayMode == "enterprise" {
		return time.Time{}, false
	}
	for _, item := range quota.Dedicated {
		// Dedicated credits cannot increase the general balance, but available
		// scoped credits prevent a provider-wide pause that would also block their models.
		if item.Available && item.Remaining > 0 && (item.ExpiresAt == nil || item.ExpiresAt.After(now)) {
			return time.Time{}, false
		}
	}
	if QoderQuotaIsPersonalZeroQuota(quota) {
		return time.Time{}, false
	}
	// A known aggregate balance takes precedence over flags and individual
	// pool fallback. Positive net credits keep the cycle usable even when an
	// upstream exhaustion flag remains set.
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

// QoderQuotaIsPersonalZeroQuota recognizes trimmed case-insensitive
// personal_standard with an observed personal pool. Its personal
// total/remaining and aggregate capacity/positive remaining must all be
// nonpositive; missing quota or personal pool reports false.
//
// This exemption distinguishes zero allocated capacity from an exhausted
// allocated plan. Aggregate remaining sums all present pools, including
// negative values, before its negative clamp, so an additional pool can affect
// the exemption through capacity or net remaining. The operation reads stable
// scalar observations and leaves any scheduling write to the caller.
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

// QoderQuotaTotalRemaining sums remaining balances for every present personal,
// add-on, and organization pool. Missing all pools reports false; zero is a
// known balance when any pool exists, and negative amounts can offset positive
// amounts.
//
// Presence remains known even when balances sum to zero, for example when
// ten remaining credits are offset by another pool holding negative ten.
// QoderQuotaRateLimitResetAt uses that distinction to prefer net balance and
// capacity before its flag-only path for absent pools. The operation performs
// no sign or finite-value validation, so producers retain responsibility for
// stable meaningful observations.
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

// QoderQuotaPositiveRemaining returns the known summed balance with negative
// totals clamped to zero. Unknown quota also returns zero; this helper uses
// net sum rather than summing only each pool’s positive contributions.
func QoderQuotaPositiveRemaining(quota *QoderQuotaInfo) float64 {
	remaining, ok := QoderQuotaTotalRemaining(quota)
	// Clamp after netting pools so negative balances still offset positive
	// observations before the display-oriented remaining value is selected.
	if !ok || remaining < 0 {
		return 0
	}
	return remaining
}

// QoderQuotaTotalCapacity sums positive Total for each pool, falling back to
// positive Cap only when that pool lacks positive Total. Nil pools contribute
// zero, and producers keep these observations stable while they are read.
//
// Total takes precedence within each pool, so Cap is an alternate observation
// rather than another amount to add to that same pool. Comparisons apply to
// supplied values directly: NaN Total tries Cap, NaN Cap contributes nothing,
// and positive infinity can enter the sum. Supply finite observations when a
// finite aggregate is required.
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
