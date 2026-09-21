package capability

// PolicyScopeMatches 根据显式账号认证类型判断规则作用域。
func PolicyScopeMatches(scope string, isOAuth bool, isBedrock bool) bool {
	switch scope {
	case "all":
		return true
	case "oauth":
		return isOAuth
	case "apikey":
		return !isOAuth && !isBedrock
	case "bedrock":
		return isBedrock
	default:
		return true // 未知作用域保持原故障放行行为
	}
}
