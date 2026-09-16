// 策略评估只接收已读取的设置与账号类型投影，不访问动态设置存储。
package anthropic

import "github.com/TokenFlux/TokenRouter/internal/routing/modelmap"

type BetaPolicyResult struct {
	BlockErr  *BetaBlockedError
	FilterSet map[string]struct{}
}

// BetaBlockedError indicates a request was blocked by a beta policy rule.
type BetaBlockedError struct {
	Message string
}

func (e *BetaBlockedError) Error() string { return e.Message }
func EvaluateBetaPolicy(settings *BetaPolicySettings, betaHeader string, isOAuth, isBedrock bool, model string) BetaPolicyResult {
	if settings == nil {
		return BetaPolicyResult{}
	}
	var result BetaPolicyResult
	for _, rule := range settings.Rules {
		if !BetaPolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
			continue
		}
		effectiveAction, effectiveErrMsg := ResolveRuleAction(rule, model)
		switch effectiveAction {
		case BetaPolicyActionBlock:
			if result.BlockErr == nil && betaHeader != "" && ContainsBetaToken(betaHeader, rule.BetaToken) {
				msg := effectiveErrMsg
				if msg == "" {
					msg = "beta feature " + rule.BetaToken + " is not allowed"
				}
				result.BlockErr = &BetaBlockedError{Message: msg}
			}
		case BetaPolicyActionFilter:
			if result.FilterSet == nil {
				result.FilterSet = make(map[string]struct{})
			}
			result.FilterSet[rule.BetaToken] = struct{}{}
		}
	}
	return result
}

// BetaPolicyScopeMatches checks whether a rule's scope matches the current account type.
func BetaPolicyScopeMatches(scope string, isOAuth bool, isBedrock bool) bool {
	switch scope {
	case BetaPolicyScopeAll:
		return true
	case BetaPolicyScopeOAuth:
		return isOAuth
	case BetaPolicyScopeAPIKey:
		return !isOAuth && !isBedrock
	case BetaPolicyScopeBedrock:
		return isBedrock
	default:
		return true // unknown scope → match all (fail-open)
	}
}

// MatchModelWhitelist checks if a model matches any pattern in the whitelist.
// Reuses modelmap.Matches from group.go which supports exact and wildcard prefix matching.
func MatchModelWhitelist(model string, whitelist []string) bool {
	for _, pattern := range whitelist {
		if modelmap.Matches(pattern, model) {
			return true
		}
	}
	return false
}

// ResolveRuleAction determines the effective action and error message for a rule given the request model.
// When ModelWhitelist is empty, the rule's primary Action/ErrorMessage applies unconditionally.
// When non-empty, Action applies to matching models; FallbackAction/FallbackErrorMessage applies to others.
func ResolveRuleAction(rule BetaPolicyRule, model string) (action, errorMessage string) {
	if len(rule.ModelWhitelist) == 0 {
		return rule.Action, rule.ErrorMessage
	}
	if MatchModelWhitelist(model, rule.ModelWhitelist) {
		return rule.Action, rule.ErrorMessage
	}
	if rule.FallbackAction != "" {
		return rule.FallbackAction, rule.FallbackErrorMessage
	}
	return BetaPolicyActionPass, "" // default fallback: pass (fail-open)
}
func CheckBetaPolicyBlockForTokens(settings *BetaPolicySettings, tokens []string, isOAuth, isBedrock bool, model string) *BetaBlockedError {
	if settings == nil || len(tokens) == 0 {
		return nil
	}
	tokenSet := BuildBetaTokenSet(tokens)
	for _, rule := range settings.Rules {
		effectiveAction, effectiveErrMsg := ResolveRuleAction(rule, model)
		if effectiveAction != BetaPolicyActionBlock {
			continue
		}
		if !BetaPolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
			continue
		}
		if _, present := tokenSet[rule.BetaToken]; present {
			msg := effectiveErrMsg
			if msg == "" {
				msg = "beta feature " + rule.BetaToken + " is not allowed"
			}
			return &BetaBlockedError{Message: msg}
		}
	}
	return nil
}
