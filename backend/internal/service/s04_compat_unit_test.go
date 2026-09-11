//go:build unit

// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	time "time"
)

// tryModelFilePricing 委托 billing 的唯一账号统计规则。
func tryModelFilePricing(billingService *BillingService, model string, tokens UsageTokens, serviceTier string, reasoningEfforts ...string) *float64 {
	effort := ""
	if len(reasoningEfforts) > 0 {
		effort = reasoningEfforts[0]
	}
	return billingService.ModelFileStatsCost(model, tokens, serviceTier, effort)
}

// resolveBalanceThreshold 委托 billing 的唯一阈值规则。
func resolveBalanceThreshold(threshold float64, thresholdType string, totalRecharged float64) float64 {
	return billing.BalanceThreshold(threshold, thresholdType, totalRecharged)
}

// resolvedThreshold 委托 billing 的唯一阈值规则。
func (d quotaDim) resolvedThreshold() float64 { return d.billingDimension().UsageThreshold() }

func nextMonthlyResetFrom(start *time.Time, now time.Time) time.Time {
	return billing.NextMonthlyResetFrom(start, now)
}

func monthlyQuotaWindowExpired(start *time.Time, now time.Time) bool {
	return billing.MonthlyQuotaWindowExpired(start, now)
}

func (s *BillingCacheService) checkUserPlatformQuotaEligibility(ctx context.Context, id int64, platform string) error {
	return s.CheckUserPlatformQuotaEligibility(ctx, id, platform)
}

// calculateCostInternal 委托唯一 billing 计费实例。
func (s *BillingService) calculateCostInternal(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, ChannelPricing *ChannelModelPricing) (*CostBreakdown, error) {
	return s.CalculateCostInternal(model, tokens, rateMultiplier, serviceTier, ChannelPricing)
}

// displayPricingFromResolved 委托纯定价实现，旧查询与配置投影保留在适配层。
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	return purepricing.DisplayPricingFromResolved(model, rateMultiplier, resolved)
}

// looksLikeImageModel 委托纯定价实现，旧查询与配置投影保留在适配层。
func looksLikeImageModel(model string) bool { return purepricing.LooksLikeImageModel(model) }

func syncBalanceCacheAfterDeduction(ctx context.Context, p *usageBillingParams, deps *billingDeps, result *UsageBillingApplyResult) {
	if p == nil || p.User == nil || deps == nil {
		return
	}
	settlementEffects(deps).SyncBalance(ctx, p.User.ID, result)
}

func normalizePlanCurrency(raw string) (string, error) { return billing.NormalizePlanCurrency(raw) }

func normalizePlanGroupIDs(groupID int64, groupIDs []int64) []int64 {
	return billing.NormalizePlanGroupIDs(groupID, groupIDs)
}

func normalizePlanGroupRateMultipliers(groupIDs []int64, rates map[int64]float64) (map[int64]float64, error) {
	return billing.NormalizePlanGroupRateMultipliers(groupIDs, rates)
}

func validatePlanRequired(name string, price float64, validityDays int, validityUnit string, originalPrice *float64) error {
	return billing.ValidatePlanRequired(name, price, validityDays, validityUnit, originalPrice)
}

func validatePlanPatch(req UpdatePlanRequest) error { return billing.ValidatePlanPatch(req) }

type nullableFloat64Patch = billing.NullableFloat64Patch
