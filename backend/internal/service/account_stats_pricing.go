package service

import (
	"context"
	"strings"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// resolveAccountStatsCost 计算账号统计定价费用。
// 返回 nil 表示不覆盖，使用默认公式（total_cost × account_rate_multiplier）。
//
// 优先级（先命中为准）：
//  1. 自定义规则（始终尝试，不依赖 ApplyPricingToAccountStats 开关）
//  2. ApplyPricingToAccountStats 启用时，直接使用本次请求的客户计费（倍率前的 totalCost）
//  3. 模型定价文件（LiteLLM）中的默认价格（Qoder 按 requested → upstream 尝试，但已知 alias/route key 不走模型价兜底）
//  4. nil → 走默认公式（total_cost × account_rate_multiplier）
//
// upstreamModel 是最终发往上游的模型 ID。
// requestedModel 是渠道映射前的请求模型 ID；channelMappedModel 是渠道映射后的 route key。
// Qoder 这类上游 route key 与公开 alias 分离的平台会按 requested → channelMapped → upstream 尝试。
// totalCost 是本次请求的客户计费（倍率前），用于优先级 2。
// serviceTier 是最终参与用户计费的服务层级，仅用于优先级 3。
//
//nolint:unused // 兼容旧测试入口；生产路径会传入 channelMappedModel 并调用 WithMapped 版本。
func resolveAccountStatsCost(
	ctx context.Context,
	channelService *ChannelService,
	billingService *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	tokens UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	return resolveAccountStatsCostWithMapped(ctx, channelService, billingService, accountID, groupID, upstreamModel, requestedModel, "", tokens, requestCount, totalCost, serviceTier, reasoningEfforts...)
}

func resolveAccountStatsCostWithMapped(
	ctx context.Context,
	channelService *ChannelService,
	billingService *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	channelMappedModel string,
	tokens UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	reasoningEffort := ""
	if len(reasoningEfforts) > 0 {
		reasoningEffort = reasoningEfforts[0]
	}
	if channelService == nil || upstreamModel == "" {
		return nil
	}
	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return nil
	}

	platform := channelService.GetGroupPlatform(ctx, groupID)

	// 渠道查询结束后，纯规则决定自定义价/用户价是否已经处理。
	if cost, handled := purepricing.ResolveAccountStatsOverride(purepricing.AccountStatsInput{
		Rules: channel.AccountStatsPricingRules, AccountID: accountID, GroupID: groupID, Platform: platform,
		Models: accountStatsCustomRuleModels(platform, upstreamModel, requestedModel, channelMappedModel),
		Tokens: tokens, RequestCount: requestCount, UserTotalCost: totalCost, ApplyUserPrice: channel.ApplyPricingToAccountStats,
	}); handled {
		return cost
	}

	// 优先级 3：模型定价文件（LiteLLM）默认价格
	if billingService != nil {
		return tryModelFilePricing(billingService, upstreamModel, tokens, serviceTier, reasoningEffort)
	}

	return nil
}

func accountStatsCustomRuleModels(platform, upstreamModel, requestedModel string, channelMappedModel ...string) []string {
	upstreamModel = strings.TrimSpace(upstreamModel)
	requestedModel = strings.TrimSpace(requestedModel)
	mappedModel := ""
	if len(channelMappedModel) > 0 {
		mappedModel = strings.TrimSpace(channelMappedModel[0])
	}
	if platform != PlatformQoder || requestedModel == "" ||
		(requestedModel == upstreamModel && (mappedModel == "" || mappedModel == requestedModel)) {
		if upstreamModel == "" {
			return nil
		}
		return []string{upstreamModel}
	}
	models := []string{requestedModel}
	models = append(models, mappedModel)
	models = append(models, upstreamModel)
	return uniqueNonEmptyAccountStatsModels(models)
}

// uniqueNonEmptyAccountStatsModels 委托唯一账号统计定价规则。
func uniqueNonEmptyAccountStatsModels(models []string) []string {
	return purepricing.UniqueNonEmptyAccountStatsModels(models)
}

// tryModelFilePricing 使用模型定价文件（LiteLLM/fallback）中的价格计算费用。
// 与用户计费共用同一条定价管线，避免这里维护第二份"单价 × token 数"实现后，
// 每加一个定价特性都要手工镜像一次。channelPricing 为 nil，保持优先级 3 的
// 语义：只取模型定价文件，不引入渠道自定义定价。
func tryModelFilePricing(billingService *BillingService, model string, tokens UsageTokens, serviceTier string, reasoningEfforts ...string) *float64 {
	reasoningEffort := ""
	if len(reasoningEfforts) > 0 {
		reasoningEffort = reasoningEfforts[0]
	}
	breakdown, err := billingService.CalculateCostUnified(CostInput{
		Model: model, Tokens: tokens, RateMultiplier: 1,
		ServiceTier: normalizeBillingServiceTier(serviceTier), ReasoningEffort: reasoningEffort,
	})
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}

// applyAccountStatsCost resolves the account stats cost for a usage log entry.
// It resolves the upstream model (falling back to the requested model) and calls
// the 4-level priority chain via resolveAccountStatsCost.
func applyAccountStatsCost(
	ctx context.Context,
	usageLog *UsageLog,
	cs *ChannelService, bs *BillingService,
	accountID int64, groupID int64,
	upstreamModel, requestedModel, channelMappedModel string,
	tokens UsageTokens,
	totalCost float64,
) {
	model := upstreamModel
	if model == "" {
		model = requestedModel
	}
	requestCount := 1
	if usageLog != nil && usageLog.ImageCount > 0 {
		requestCount = usageLog.ImageCount
	}
	serviceTier := ""
	reasoningEffort := ""
	if usageLog != nil && usageLog.ServiceTier != nil {
		serviceTier = *usageLog.ServiceTier
	}
	if usageLog != nil && usageLog.ReasoningEffort != nil {
		reasoningEffort = *usageLog.ReasoningEffort
	}
	usageLog.AccountStatsCost = resolveAccountStatsCostWithMapped(
		ctx, cs, bs, accountID, groupID, model, requestedModel, channelMappedModel, tokens, requestCount, totalCost, serviceTier,
		reasoningEffort,
	)
}
