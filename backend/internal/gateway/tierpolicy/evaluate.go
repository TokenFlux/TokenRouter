package tierpolicy

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// Evaluate 只评估已取得的策略与显式账号投影，保持用户优先及组内首条命中。
func Evaluate(settings *OpenAIFastPolicySettings, userID int64, isOAuth, isBedrock bool, model, tier string) (action, errMsg string) {
	if settings == nil {
		return "pass", ""
	}

	// 用户专属规则先于全局规则。规则组内仍按配置顺序首条命中，允许
	// 管理员为某位用户配置例外，而不被先出现的全局规则覆盖。
	for _, userScoped := range []bool{true, false} {
		for _, rule := range settings.Rules {
			if (len(rule.UserIDs) > 0) != userScoped || !userMatches(rule.UserIDs, userID) {
				continue
			}
			if !capability.PolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
				continue
			}
			ruleTier := strings.ToLower(strings.TrimSpace(rule.ServiceTier))
			if ruleTier != "" && ruleTier != OpenAIFastTierAny && ruleTier != tier {
				continue
			}
			eff := modelmap.ActionRule{
				Action:               rule.Action,
				ErrorMessage:         rule.ErrorMessage,
				ModelWhitelist:       rule.ModelWhitelist,
				FallbackAction:       rule.FallbackAction,
				FallbackErrorMessage: rule.FallbackErrorMessage,
			}
			return modelmap.ResolveAction(eff, model)
		}
	}
	return "pass", ""
}

// userMatches 判断全局规则或指定用户规则是否匹配当前用户。
func userMatches(ruleUserIDs []int64, userID int64) bool {
	if len(ruleUserIDs) == 0 {
		return true
	}
	for _, ruleUserID := range ruleUserIDs {
		if ruleUserID == userID {
			return true
		}
	}
	return false
}
