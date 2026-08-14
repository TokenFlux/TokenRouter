package xai

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// GrokQuotaSignalMaxAge 限制 grok-4.5 Responses 额度窗口影响 SuperGrok/Heavy 推断的最长时间。
const GrokQuotaSignalMaxAge = 24 * time.Hour

const (
	grok45ResponsesModel             = "grok-4.5"
	grokHeavyQuotaRequestLimit int64 = 8_300
	grokHeavyQuotaTokenLimit   int64 = 53_000_000
)

// MapJWTSubscriptionTier 将 prod_auth.SubscriptionTier 的数字 JWT 声明映射为
// Grok Build 和 Mixpanel 使用的稳定 snake_case 标识。
func MapJWTSubscriptionTier(tier uint64) string {
	switch tier {
	case 0:
		return "free"
	case 1:
		return "supergrok"
	case 2:
		return "x_basic"
	case 3:
		return "x_premium"
	case 4:
		return "x_premium_plus"
	case 5:
		return "supergrok_heavy"
	case 6:
		return "supergrok_lite"
	case 7:
		return "supergrok_plus"
	default:
		return strconv.FormatUint(tier, 10)
	}
}

// NormalizeSubscriptionTier 将展示名称、/user 字符串和 JWT 标识统一为 snake_case。
func NormalizeSubscriptionTier(raw string) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.ReplaceAll(t, "-", "_")
	t = strings.Join(strings.Fields(t), "_")
	switch t {
	case "free", "grok_free", "grokfree", "free_tier", "freetier", "grok_basic", "grokbasic":
		return "free"
	case "supergrok", "grokpro":
		return "supergrok"
	case "supergrok_lite", "supergroklite":
		return "supergrok_lite"
	case "supergrok_heavy", "supergrokheavy":
		return "supergrok_heavy"
	case "supergrok_pro", "supergrokpro":
		return "supergrok_pro"
	case "supergrok_plus", "supergrokplus":
		return "supergrok_plus"
	case "x_basic", "xbasic", "basic":
		return "x_basic"
	case "x_premium", "xpremium":
		return "x_premium"
	case "x_premium_plus", "xpremiumplus", "x_premium+":
		return "x_premium_plus"
	default:
		return t
	}
}

// SubscriptionTierFromJWT 解码访问令牌载荷（不校验签名），并映射数字或字符串 tier 声明。
func SubscriptionTierFromJWT(jwt string) string {
	claims := DecodeJWTClaims(jwt)
	if claims == nil {
		return ""
	}
	raw, ok := claims["tier"]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case float64:
		if v < 0 {
			return ""
		}
		return MapJWTSubscriptionTier(uint64(v))
	case json.Number:
		n, err := v.Int64()
		if err != nil || n < 0 {
			return NormalizeSubscriptionTier(v.String())
		}
		return MapJWTSubscriptionTier(uint64(n))
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return ""
		}
		if n, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
			return MapJWTSubscriptionTier(n)
		}
		return NormalizeSubscriptionTier(trimmed)
	default:
		return ""
	}
}

// CanonicalGrokPlan 在供应商返回含糊的 SuperGrokPro 时区分 SuperGrok 与 Heavy。
// 调用方应先应用 JWT 数字档位；存在每月 $150/$1500 限额时仍以限额为准；
// 只有来自 grok-4.5 Responses 的限流窗口才参与推断。
func CanonicalGrokPlan(monthlyLimitCents *float64, subscriptionTier string, quota *QuotaSnapshot) string {
	if plan := resolvePlan(monthlyLimitCents); plan != "" {
		return NormalizeSubscriptionTier(plan)
	}

	normalized := NormalizeSubscriptionTier(subscriptionTier)
	switch normalized {
	case "free", "x_basic":
		return "free"
	case "supergrok_heavy":
		return "supergrok_heavy"
	case "supergrok_lite":
		return "supergrok_lite"
	case "supergrok_plus":
		return "supergrok_plus"
	}

	if isAmbiguousGrokPaidPlan(normalized) {
		if hint := Grok45ResponsesPlanHint(quota, time.Time{}); hint != "" {
			return hint
		}
		return "supergrok"
	}
	return ""
}

func isAmbiguousGrokPaidPlan(normalized string) bool {
	switch normalized {
	case "supergrok", "supergrok_pro", "paid", "pro":
		return true
	default:
		return false
	}
}

// IsGrok45ResponsesQuotaModel 判断模型是否为 grok-4.5 Responses 标识或其日期快照变体。
func IsGrok45ResponsesQuotaModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(StripGrokProviderPrefix(model)))
	return m == grok45ResponsesModel || strings.HasPrefix(m, grok45ResponsesModel+"-")
}

// Grok45ResponsesPlanHint 根据 grok-4.5 Responses 窗口推断 SuperGrok 或 Heavy，
// 其他模型的额度不会参与判断。
func Grok45ResponsesPlanHint(quota *QuotaSnapshot, now time.Time) string {
	if quota == nil {
		return ""
	}
	if plan := NormalizeSubscriptionTier(quota.PlanFrom45Responses); plan == "supergrok" || plan == "supergrok_heavy" {
		if isQuotaTimestampFresh(quota.PlanFrom45ResponsesAt, now) {
			return plan
		}
	}
	if !IsGrok45ResponsesQuotaModel(quota.Model) || !IsQuotaSnapshotFresh(quota, now) {
		return ""
	}
	if quotaLooksLikeGrokHeavy(quota) {
		return "supergrok_heavy"
	}
	return ""
}

// ApplyGrok45ResponsesPlanSignal 记录 grok-4.5 的 Heavy/SuperGrok 提示；
// 当前观察来自其他模型时，延续此前的 4.5 提示。
func (s *QuotaSnapshot) ApplyGrok45ResponsesPlanSignal(prev *QuotaSnapshot) {
	if s == nil {
		return
	}
	observedAt := firstNonEmptyQuotaTime(s.LastHeadersSeenAt, s.UpdatedAt)
	if IsGrok45ResponsesQuotaModel(s.Model) && quotaHasLimitWindow(s) {
		if quotaLooksLikeGrokHeavy(s) {
			s.PlanFrom45Responses = "supergrok_heavy"
			s.PlanFrom45ResponsesAt = observedAt
			return
		}
		s.PlanFrom45Responses = "supergrok"
		s.PlanFrom45ResponsesAt = observedAt
		return
	}
	if prev != nil && strings.TrimSpace(prev.PlanFrom45Responses) != "" {
		s.PlanFrom45Responses = prev.PlanFrom45Responses
		s.PlanFrom45ResponsesAt = prev.PlanFrom45ResponsesAt
	}
}

// QuotaSnapshotObservedAt 优先使用 LastHeadersSeenAt，避免后续重写快照时
// 用 UpdatedAt 错误刷新已经过期的 Heavy 窗口。
func QuotaSnapshotObservedAt(snapshot *QuotaSnapshot) (time.Time, bool) {
	if snapshot == nil {
		return time.Time{}, false
	}
	return parseQuotaTimestamp(firstNonEmptyQuotaTime(snapshot.LastHeadersSeenAt, snapshot.UpdatedAt))
}

// IsQuotaSnapshotFresh 判断额度信号是否足够新，可用于区分 SuperGrok 与 Heavy。
func IsQuotaSnapshotFresh(snapshot *QuotaSnapshot, now time.Time) bool {
	observedAt, ok := QuotaSnapshotObservedAt(snapshot)
	if !ok {
		return false
	}
	return isTimeFresh(observedAt, now)
}

func isQuotaTimestampFresh(raw string, now time.Time) bool {
	parsed, ok := parseQuotaTimestamp(raw)
	if !ok {
		return false
	}
	return isTimeFresh(parsed, now)
}

func parseQuotaTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func isTimeFresh(observedAt, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(observedAt)
	return age <= GrokQuotaSignalMaxAge && age >= -5*time.Minute
}

func quotaHasLimitWindow(quota *QuotaSnapshot) bool {
	if quota == nil {
		return false
	}
	if quota.Requests != nil && quota.Requests.Limit != nil {
		return true
	}
	return quota.Tokens != nil && quota.Tokens.Limit != nil
}

func quotaLooksLikeGrokHeavy(quota *QuotaSnapshot) bool {
	if quota == nil {
		return false
	}
	var requestLimit, tokenLimit int64
	if quota.Requests != nil && quota.Requests.Limit != nil {
		requestLimit = *quota.Requests.Limit
	}
	if quota.Tokens != nil && quota.Tokens.Limit != nil {
		tokenLimit = *quota.Tokens.Limit
	}
	return requestLimit >= grokHeavyQuotaRequestLimit || tokenLimit >= grokHeavyQuotaTokenLimit
}

func firstNonEmptyQuotaTime(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
