package modelmap

// ActionRule 只表达模型范围与对应动作，不携带平台或请求实体。
type ActionRule struct {
	Action               string
	ErrorMessage         string
	ModelWhitelist       []string
	FallbackAction       string
	FallbackErrorMessage string
}

// MatchesAny 沿用精确或末尾通配符规则匹配模型白名单。
func MatchesAny(model string, whitelist []string) bool {
	for _, pattern := range whitelist {
		if Matches(pattern, model) {
			return true
		}
	}
	return false
}

// ResolveAction 按模型白名单选择主动作或回退动作；空回退仍放行。
func ResolveAction(rule ActionRule, model string) (action, errorMessage string) {
	if len(rule.ModelWhitelist) == 0 {
		return rule.Action, rule.ErrorMessage
	}
	if MatchesAny(model, rule.ModelWhitelist) {
		return rule.Action, rule.ErrorMessage
	}
	if rule.FallbackAction != "" {
		return rule.FallbackAction, rule.FallbackErrorMessage
	}
	return "pass", "" // 缺省回退保持放行
}
