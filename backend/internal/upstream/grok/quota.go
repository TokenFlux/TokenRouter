package grok

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	usageview "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// GrokFreeRolling24hTokenLimit 是运维软门禁使用的 Free 账号滚动 24 小时名义额度；
// 上游请求头仍可能返回历史版本的 100 万或 200 万额度快照。
const GrokFreeRolling24hTokenLimit int64 = 500_000

var grokFreeRolling24hTokenLimits = map[int64]struct{}{

	GrokFreeRolling24hTokenLimit: {},

	1_000_000: {},
	// 上游观测到的 Free 限额变体。
	2_000_000: {},
	// 2026 年 7 月前观测到的旧版 Free 限额。
}

func IsGrokFreeRolling24hTokenLimit(limit int64) bool {
	_, ok := grokFreeRolling24hTokenLimits[limit]
	return ok
}

// QuotaWindow 复用账号展示叶子值；供应商解析仍由本包拥有。
type QuotaWindow = usageview.QuotaWindow

type QuotaSnapshot = usageview.QuotaSnapshot

var quotaHeaderAllowlist = []string{
	"x-ratelimit-limit-requests",
	"x-ratelimit-remaining-requests",
	"x-ratelimit-reset-requests",
	"x-ratelimit-limit-tokens",
	"x-ratelimit-remaining-tokens",
	"x-ratelimit-reset-tokens",
	"x-rate-limit-limit-requests",
	"x-rate-limit-remaining-requests",
	"x-rate-limit-reset-requests",
	"x-rate-limit-limit-tokens",
	"x-rate-limit-remaining-tokens",
	"x-rate-limit-reset-tokens",
	"retry-after",
	"x-subscription-tier",
	"xai-subscription-tier",
	"x-xai-subscription-tier",
	"x-xai-user-tier",
	"xai-user-tier",
	"xai-tier",
	"x-user-tier",
	"x-plan-tier",
	"x-subscription-plan",
	"x-entitlement-status",
	"xai-entitlement-status",
	"x-xai-entitlement-status",
	"x-xai-user-entitlement-status",
	"x-user-entitlement-status",
}

func ParseQuotaHeaders(headers http.Header, statusCode int) *QuotaSnapshot {
	return parseQuotaHeaders(headers, statusCode, "", false)
}

func ObserveQuotaHeaders(headers http.Header, statusCode int, source string) *QuotaSnapshot {
	return parseQuotaHeaders(headers, statusCode, source, true)
}

func parseQuotaHeaders(headers http.Header, statusCode int, source string, keepEmpty bool) *QuotaSnapshot {
	if headers == nil && !keepEmpty {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	snapshot := &QuotaSnapshot{

		Requests: parseQuotaWindow(headers, "requests"),

		Tokens: parseQuotaWindow(headers, "tokens"),

		StatusCode: statusCode,

		Headers: make(map[string]string),

		ObservationSource: strings.TrimSpace(source),

		UpdatedAt: now,
	}
	if snapshot.ObservationSource == "active_probe" {
		snapshot.LastProbeAt = now
	}
	if retryAfter := parseRetryAfter(headers.Get("retry-after")); retryAfter != nil {
		snapshot.RetryAfterSeconds = retryAfter
	}
	snapshot.SubscriptionTier = firstHeader(headers,
		"xai-subscription-tier",
		"x-subscription-tier",
		"x-xai-subscription-tier",
		"x-xai-user-tier",
		"xai-user-tier",
		"xai-tier",
		"x-user-tier",
		"x-plan-tier",
		"x-subscription-plan",
	)
	snapshot.EntitlementStatus = firstHeader(headers,
		"xai-entitlement-status",
		"x-entitlement-status",
		"x-xai-entitlement-status",
		"x-xai-user-entitlement-status",
		"x-user-entitlement-status",
	)

	for _, name := range quotaHeaderAllowlist {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			snapshot.Headers[name] = value
		}
	}

	if snapshot.Requests == nil &&
		snapshot.Tokens == nil &&
		snapshot.RetryAfterSeconds == nil &&
		snapshot.SubscriptionTier == "" &&
		snapshot.EntitlementStatus == "" &&
		len(snapshot.Headers) == 0 {
		if keepEmpty {
			return snapshot
		}
		return nil
	}
	snapshot.HeadersObserved = true
	snapshot.LastHeadersSeenAt = now
	return snapshot
}

func parseQuotaWindow(headers http.Header, dimension string) *QuotaWindow {
	limitHeader := firstHeader(headers,
		"x-ratelimit-limit-"+dimension,
		"x-rate-limit-limit-"+dimension,
	)
	remainingHeader := firstHeader(headers,
		"x-ratelimit-remaining-"+dimension,
		"x-rate-limit-remaining-"+dimension,
	)
	resetHeader := firstHeader(headers,
		"x-ratelimit-reset-"+dimension,
		"x-rate-limit-reset-"+dimension,
	)
	window := &QuotaWindow{
		Limit:     parseInt64Ptr(limitHeader),
		Remaining: parseInt64Ptr(remainingHeader),
	}
	if reset := parseResetHeader(resetHeader); reset != nil {
		window.ResetUnix = reset
		window.ResetAt = time.Unix(*reset, 0).UTC().Format(time.RFC3339)
	}
	if window.Limit == nil && window.Remaining == nil && window.ResetUnix == nil {
		return nil
	}
	return window
}

func parseResetHeader(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		// xAI 兼容上游可能使用毫秒时间戳、秒时间戳或相对秒数表达重置时间，
		// 因此按数量级区分，避免把相对值 60 误解为 1970 年的时间戳。
		switch {
		case value >= 1_000_000_000_000: // milliseconds epoch → seconds
			value = value / 1000
		case value >= 1_000_000_000: // already a plausible unix-seconds epoch (>= 2001-09)
			// 已是合理的 Unix 秒时间戳，保持原值。
		default: // relative seconds from now
			value = time.Now().Unix() + value
		}
		return &value
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		if duration < time.Second {
			duration = time.Second
		}
		value := time.Now().Add(duration).Unix()
		return &value
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		value := t.Unix()
		return &value
	}
	return nil
}

func parseRetryAfter(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if value, err := strconv.Atoi(raw); err == nil {
		return &value
	}
	if t, err := http.ParseTime(raw); err == nil {
		seconds := int(time.Until(t).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		return &seconds
	}
	return nil
}

func parseInt64Ptr(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
