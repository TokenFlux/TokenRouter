// 媒体资格与免费层判断使用同一账号规则，平台声明解析由外层注入。
package account

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"strings"
)

// GrokTierRules 提供平台声明的纯解析能力，不持有账号或运行状态。
type GrokTierRules struct {
	SubscriptionTierFromJWT   func(string) string
	NormalizeSubscriptionTier func(string) string
	IsFreeRollingTokenLimit   func(int64) bool
}

func KnownGrokFreeAccount(account *Record, rules GrokTierRules) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	// 实时访问令牌 JWT 优先于陈旧的账单或凭据快照，令牌刷新后可立即反映降级到免费档位。
	if jwtTier := rules.SubscriptionTierFromJWT(account.GetCredential("access_token")); jwtTier != "" {
		return isGrokFreeSubscriptionTier(jwtTier, rules)
	}
	freeSignal := false
	paidSignal := false
	inferredFreeSignal := false
	if billing, err := ParseGrokBillingSnapshot(account.Extra); err == nil && billing != nil {
		if tier := strings.TrimSpace(billing.Plan); tier != "" {
			if isGrokFreeSubscriptionTier(tier, rules) {
				freeSignal = true
			} else if !isGrokUnknownSubscriptionTier(tier) {
				paidSignal = true
			}
		}
		// 用量百分比或月度美元上限可以证明账号属于付费计划。
		if billing.UsagePercent != nil || billing.UsedPercent != nil ||
			(billing.MonthlyLimitCents != nil && *billing.MonthlyLimitCents > 0) {
			paidSignal = true
		}
		// xAI 会故意为 Free 账号返回空 plan，只有付费订阅才带 SuperGrok plan/月度限额。
		// 因此，成功且没有付费信号的月度计费观测是 Free 的正向证据，而不是未知层级；
		// 部分探测仍按关闭策略处理。
		if strings.TrimSpace(billing.MonthlyUpdatedAt) != "" ||
			(billing.StatusCode >= 200 && billing.StatusCode < 300 &&
				!billing.Partial && len(billing.FailedWindows) == 0) {
			inferredFreeSignal = true
		}
	}
	if snapshot, err := GrokQuotaSnapshotFromExtra(account.Extra); err == nil && snapshot != nil {
		if tier := strings.TrimSpace(snapshot.SubscriptionTier); tier != "" {
			if isGrokFreeSubscriptionTier(tier, rules) {
				freeSignal = true
			} else if !isGrokUnknownSubscriptionTier(tier) {
				paidSignal = true
			}
		}
		if snapshot.Tokens != nil && snapshot.Tokens.Limit != nil &&
			rules.IsFreeRollingTokenLimit(*snapshot.Tokens.Limit) {
			inferredFreeSignal = true
		}
	}
	// 此处仅凭证中的 subscription_tier 具有权威性，不采用 plan_type 或扩展字段。
	if tier := strings.TrimSpace(account.GetCredential("subscription_tier")); tier != "" {
		if isGrokFreeSubscriptionTier(tier, rules) {
			freeSignal = true
		} else if !isGrokUnknownSubscriptionTier(tier) {
			paidSignal = true
		}
	}
	// 明确的付费证据始终覆盖推断的 Free 信号，避免已升级但快照陈旧的账号仍携带历史
	// 200 万 Free token 限额而被误判。
	return !paidSignal && (freeSignal || inferredFreeSignal)
}

func isGrokFreeSubscriptionTier(tier string, rules GrokTierRules) bool {
	switch rules.NormalizeSubscriptionTier(tier) {
	case "free", "x_basic":
		return true
	default:
		return false
	}
}

func isGrokUnknownSubscriptionTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "", "unknown", "n/a", "none":
		return true
	default:
		return false
	}
}

func GrokMediaGenerationEligibility(a *Record, rules GrokTierRules) (bool, string) {
	if a == nil || !a.IsGrok() {
		return false, "not_grok"
	}
	if override, ok := GrokMediaEligibilityOverride(a.Extra); ok {
		if override {
			return true, "override_enabled"
		}
		return false, "override_disabled"
	}
	if a.Type != capability.AccountTypeOAuth {
		return true, "non_oauth"
	}

	billing, err := ParseGrokBillingSnapshot(a.Extra)
	if err != nil || billing == nil {
		return false, "billing_unobserved"
	}
	if billing.StatusCode == 403 || billing.WeeklyStatusCode == 403 || billing.MonthlyStatusCode == 403 {
		return false, "billing_forbidden"
	}
	if KnownGrokFreeAccount(a, rules) {
		return false, "billing_free_tier"
	}
	if !GrokBillingHasAuthoritativeQuota(billing) {
		return false, "billing_inconclusive"
	}
	return true, "eligible"
}
