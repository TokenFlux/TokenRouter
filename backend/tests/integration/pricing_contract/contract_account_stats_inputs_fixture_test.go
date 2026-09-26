//go:build unit

// 账号统计合同按显式输入调用实际价格解析器。
package pricingcontract

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// applyContractAccountStatsCost resolves the account stats cost for a usage log entry.
// It resolves the upstream model (falling back to the requested model) and calls
// the 4-level priority chain via contractAccountStatsCost.
func applyContractAccountStatsCost(
	ctx context.Context,
	usageLog *usage.UsageLog,
	cs *routing.PricingConfigService, bs *billing.Calculator,
	accountID int64, groupID int64,
	upstreamModel, requestedModel, groupMappedModel string,
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
		usageLog.AccountStatsCost = resolvers[0].ResolveAccountStats(ctx, billing.AccountStatsCostInput{AccountID: accountID, GroupID: groupID, UpstreamModel: model, RequestedModel: requestedModel, MappedModel: groupMappedModel, Tokens: tokens, RequestCount: requestCount, ServiceTier: serviceTier, ReasoningEffort: reasoningEffort})
		return
	}
	usageLog.AccountStatsCost = contractAccountStatsWithMapping(
		ctx, cs, bs, accountID, groupID, model, requestedModel, groupMappedModel, tokens, requestCount, totalCost, serviceTier,
		reasoningEffort,
	)
}

// contractAccountStatsCost 计算独立账号成本，先匹配自定义规则，再查询模型默认价。
// 无可用成本价时返回 nil，保留日志层的历史回退公式。
// Qoder 自定义规则依次按请求模型、分组映射模型和最终上游模型匹配。
// totalCost 仅作为测试输入，生产成本解析器不接收用户售价。
func contractAccountStatsCost(
	ctx context.Context,
	pricingConfigService *routing.PricingConfigService,
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
	return contractAccountStatsWithMapping(ctx, pricingConfigService, billingService, accountID, groupID, upstreamModel, requestedModel, "", tokens, requestCount, totalCost, serviceTier, reasoningEfforts...)
}

// contractAccountStatsWithMapping 委托 billing 的唯一账号统计规则。
func contractAccountStatsWithMapping(
	ctx context.Context,
	pricingConfigService *routing.PricingConfigService,
	billingService *billing.Calculator,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	groupMappedModel string,
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
	if pricingConfigService != nil {
		source = gatewayprovider.AccountStatsSource{Service: pricingConfigService}
	}
	var calculator *billing.Calculator
	if billingService != nil {
		calculator = billingService
	}
	resolver := billing.NewPriceResolver(nil, calculator, nil, nil, source)
	return resolver.ResolveAccountStats(ctx, billing.AccountStatsCostInput{AccountID: accountID, GroupID: groupID, UpstreamModel: upstreamModel, RequestedModel: requestedModel, MappedModel: groupMappedModel, Tokens: tokens, RequestCount: requestCount, ServiceTier: serviceTier, ReasoningEffort: effort})
}
