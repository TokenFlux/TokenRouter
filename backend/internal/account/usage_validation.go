// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	math "math"
	reflect "reflect"
	strings "strings"
)

func SameUpstreamUsageIdentity(expected, current *Record, expectedConfig UpstreamUsageQueryConfig) bool {
	if expected == nil || current == nil || expected.ID != current.ID || current.Type != AccountTypeAPIKey ||
		expected.Platform != current.Platform || !reflect.DeepEqual(expected.Credentials, current.Credentials) ||
		!SameUpstreamUsageOptionalInt64(expected.ProxyID, current.ProxyID) || expected.Concurrency != current.Concurrency ||
		expected.Status != current.Status {
		return false
	}
	if expected.ProxyID != nil && !SameUpstreamUsageProxy(expected.Proxy, current.Proxy, *expected.ProxyID) {
		// 仓储可能暂时没有预加载代理详情；两边都缺失时交给客户端构建阶段
		// 返回请求错误，避免把可诊断的配置缺失误报成身份冲突。
		if expected.Proxy != nil || current.Proxy != nil {
			return false
		}
	}
	for _, key := range []string{"enable_tls_fingerprint", "tls_fingerprint_profile_id", "tls_fingerprint_router_id"} {
		if !reflect.DeepEqual(ExtraValue(expected.Extra, key), ExtraValue(current.Extra, key)) {
			return false
		}
	}
	currentConfig, err := EffectiveUpstreamUsageConfig(current)
	if err != nil || currentConfig != expectedConfig {
		return false
	}
	return true
}

func SameUpstreamUsageOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func SameUpstreamUsageProxy(expected, current *egress.Proxy, id int64) bool {
	if expected == nil || current == nil || expected.ID != id || current.ID != id {
		return false
	}
	return expected.Protocol == current.Protocol && expected.Host == current.Host && expected.Port == current.Port &&
		expected.Username == current.Username && expected.Password == current.Password && expected.Status == current.Status
}

func ExtraValue(extra map[string]any, key string) any {
	if extra == nil {
		return nil
	}
	return extra[key]
}

func ValidateNormalizedUsage(usage *UpstreamUsageInfo) error {
	if usage == nil || strings.TrimSpace(usage.Provider) == "" || strings.TrimSpace(usage.Mode) == "" {
		return errors.New("missing normalized usage fields")
	}
	switch usage.Mode {
	case "balance", "quota", "limits", "subscription":
	default:
		return errors.New("unknown normalized usage mode")
	}
	switch usage.Mode {
	case "balance", "quota":
		if usage.Balance == nil {
			return errors.New("missing normalized balance")
		}
	case "limits":
		if len(usage.Limits) == 0 && (usage.Subscription == nil || len(usage.Subscription.Limits) == 0) {
			return errors.New("missing normalized limits")
		}
	case "subscription":
		if usage.Subscription == nil {
			return errors.New("missing normalized subscription")
		}
	}
	if usage.Unit != "" && usage.Unit != "USD" && usage.Unit != "CNY" && usage.Unit != "TOKENS" && usage.Unit != "PERCENT" {
		return errors.New("unknown usage unit")
	}
	for _, balance := range usage.Balances {
		if strings.TrimSpace(balance.Currency) == "" || !ValidFiniteNumber(balance.Remaining) {
			return errors.New("invalid usage balance entry")
		}
	}
	if usage.Balance != nil {
		if err := ValidateUsageAmount(usage.Balance); err != nil {
			return err
		}
	}
	if err := ValidateUsageLimits(usage.Limits); err != nil {
		return err
	}
	if usage.Subscription != nil {
		if strings.TrimSpace(usage.Subscription.PlanName) == "" {
			return errors.New("missing subscription plan")
		}
		if usage.Subscription.Unlimited && (usage.Subscription.Remaining != nil || len(usage.Subscription.Limits) > 0) {
			return errors.New("unlimited subscription must not contain remaining or limits")
		}
		if !usage.Subscription.Unlimited && usage.Subscription.Remaining == nil && len(usage.Subscription.Limits) == 0 {
			return errors.New("limited subscription is missing remaining or limits")
		}
		if usage.Subscription.Remaining != nil && !ValidFiniteNumber(*usage.Subscription.Remaining) {
			return errors.New("invalid subscription remaining")
		}
		if usage.Subscription.ExpiresAt != nil && usage.Subscription.ExpiresAt.IsZero() {
			return errors.New("invalid subscription expiry")
		}
		if err := ValidateUsageLimits(usage.Subscription.Limits); err != nil {
			return err
		}
	}
	if usage.ExpiresAt != nil && usage.ExpiresAt.IsZero() {
		return errors.New("invalid expiry")
	}
	return nil
}

func ValidateUsageAmount(amount *UpstreamUsageAmount) error {
	if amount == nil || (amount.Used == nil && amount.Total == nil && amount.Remaining == nil) {
		return errors.New("missing usage amount values")
	}
	for _, value := range []*float64{amount.Used, amount.Total} {
		if value != nil && !ValidNonNegativeNumber(*value) {
			return errors.New("invalid usage amount")
		}
	}
	if amount.Remaining != nil && !ValidFiniteNumber(*amount.Remaining) {
		return errors.New("invalid usage remaining")
	}
	// 钱包余额允许为负；New API Token 额度在适配器层已经校验为非负。
	return nil
}

func ValidateUsageLimits(limits []UpstreamUsageLimit) error {
	seen := make(map[string]struct{}, len(limits))
	for _, limit := range limits {
		name := strings.TrimSpace(limit.Name)
		if name == "" {
			return errors.New("missing usage limit name")
		}
		if _, exists := seen[name]; exists {
			return errors.New("duplicate usage limit")
		}
		seen[name] = struct{}{}
		for _, value := range []*float64{limit.Used, limit.Limit} {
			if value != nil && !ValidNonNegativeNumber(*value) {
				return errors.New("invalid usage limit amount")
			}
		}
		if limit.Used == nil && limit.Limit == nil && limit.Remaining == nil {
			return errors.New("missing usage limit values")
		}
		if limit.Remaining != nil && !ValidFiniteNumber(*limit.Remaining) {
			return errors.New("invalid usage limit remaining")
		}
		if limit.ResetAt != nil && limit.ResetAt.IsZero() {
			return errors.New("invalid usage limit reset")
		}
	}
	return nil
}

func ValidNonNegativeNumber(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ValidPositiveNumber(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ValidFiniteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func UpstreamUsageContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrUpstreamUsageTimeout
	}
	return ErrUpstreamUsageRequestFailed
}
