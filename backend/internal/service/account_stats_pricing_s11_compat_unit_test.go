//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// applyAccountStatsCost resolves the account stats cost for a usage log entry.
// It resolves the upstream model (falling back to the requested model) and calls
// the 4-level priority chain via resolveAccountStatsCost.
func applyAccountStatsCost(
	ctx context.Context,
	usageLog *usage.UsageLog,
	cs *routing.ChannelService, bs *billing.Calculator,
	accountID int64, groupID int64,
	upstreamModel, requestedModel, channelMappedModel string,
	tokens pricing.UsageTokens,
	totalCost float64,
	resolvers ...*billing.PriceResolver,
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
	if len(resolvers) > 0 && resolvers[0] != nil {
		usageLog.AccountStatsCost = resolvers[0].ResolveAccountStats(ctx, billing.AccountStatsCostInput{AccountID: accountID, GroupID: groupID, UpstreamModel: model, RequestedModel: requestedModel, MappedModel: channelMappedModel, Tokens: tokens, RequestCount: requestCount, UserTotalCost: totalCost, ServiceTier: serviceTier, ReasoningEffort: reasoningEffort})
		return
	}
	usageLog.AccountStatsCost = resolveAccountStatsCostWithMapped(
		ctx, cs, bs, accountID, groupID, model, requestedModel, channelMappedModel, tokens, requestCount, totalCost, serviceTier,
		reasoningEffort,
	)
}

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
func resolveAccountStatsCost(
	ctx context.Context,
	channelService *routing.ChannelService,
	billingService *billing.Calculator,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	tokens pricing.UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	return resolveAccountStatsCostWithMapped(ctx, channelService, billingService, accountID, groupID, upstreamModel, requestedModel, "", tokens, requestCount, totalCost, serviceTier, reasoningEfforts...)
}

// resolveAccountStatsCostWithMapped 委托 billing 的唯一账号统计规则。
func resolveAccountStatsCostWithMapped(
	ctx context.Context,
	channelService *routing.ChannelService,
	billingService *billing.Calculator,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	channelMappedModel string,
	tokens pricing.UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	effort := ""
	if len(reasoningEfforts) > 0 {
		effort = reasoningEfforts[0]
	}
	var source billing.AccountStatsSource
	if channelService != nil {
		source = gatewayprovider.AccountStatsSource{Service: channelService}
	}
	var calculator *billing.Calculator
	if billingService != nil {
		calculator = billingService
	}
	resolver := billing.NewPriceResolver(nil, calculator, nil, nil, source)
	return resolver.ResolveAccountStats(ctx, billing.AccountStatsCostInput{AccountID: accountID, GroupID: groupID, UpstreamModel: upstreamModel, RequestedModel: requestedModel, MappedModel: channelMappedModel, Tokens: tokens, RequestCount: requestCount, UserTotalCost: totalCost, ServiceTier: serviceTier, ReasoningEffort: effort})
}
