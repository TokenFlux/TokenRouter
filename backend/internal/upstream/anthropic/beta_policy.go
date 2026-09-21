// 策略评估只接收已读取的设置与账号类型投影，不访问动态设置存储。
package anthropic

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

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
		if !capability.PolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
			continue
		}
		effectiveAction, effectiveErrMsg := modelmap.ResolveAction(modelmap.ActionRule{Action: rule.Action, ErrorMessage: rule.ErrorMessage, ModelWhitelist: rule.ModelWhitelist, FallbackAction: rule.FallbackAction, FallbackErrorMessage: rule.FallbackErrorMessage}, model)
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

func CheckBetaPolicyBlockForTokens(settings *BetaPolicySettings, tokens []string, isOAuth, isBedrock bool, model string) *BetaBlockedError {
	if settings == nil || len(tokens) == 0 {
		return nil
	}
	tokenSet := BuildBetaTokenSet(tokens)
	for _, rule := range settings.Rules {
		effectiveAction, effectiveErrMsg := modelmap.ResolveAction(modelmap.ActionRule{Action: rule.Action, ErrorMessage: rule.ErrorMessage, ModelWhitelist: rule.ModelWhitelist, FallbackAction: rule.FallbackAction, FallbackErrorMessage: rule.FallbackErrorMessage}, model)
		if effectiveAction != BetaPolicyActionBlock {
			continue
		}
		if !capability.PolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
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
