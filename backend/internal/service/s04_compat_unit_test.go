//go:build unit

// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// tryModelFilePricing 委托 billing 的唯一账号统计规则。
func tryModelFilePricing(billingService *billing.Calculator, model string, tokens purepricing.UsageTokens, serviceTier string, reasoningEfforts ...string) *float64 {
	effort := ""
	if len(reasoningEfforts) > 0 {
		effort = reasoningEfforts[0]
	}
	return billingService.ModelFileStatsCost(model, tokens, serviceTier, effort)
}

// displayPricingFromResolved 委托纯定价实现，旧查询与配置投影保留在适配层。
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *purepricing.ResolvedPricing) (purepricing.ModelDisplayPricing, bool) {
	return purepricing.DisplayPricingFromResolved(model, rateMultiplier, resolved)
}

// looksLikeImageModel 委托纯定价实现，旧查询与配置投影保留在适配层。
func looksLikeImageModel(model string) bool { return purepricing.LooksLikeImageModel(model) }
