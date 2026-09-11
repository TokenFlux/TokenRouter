// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	context "context"
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	strings "strings"
)

// AccountStatsChannel 不携带账号或渠道实体，只包含统计价卡。
type AccountStatsChannel struct {
	Rules          []purepricing.AccountStatsPricingRule
	ApplyUserPrice bool
}
type AccountStatsPlatform struct {
	ID                   string
	PreferRequestedModel bool
}
type AccountStatsSource interface {
	AccountStatsGroup(context.Context, int64) (*AccountStatsChannel, error)
	AccountStatsPlatform(context.Context, int64) AccountStatsPlatform
}

// AccountStatsCostInput 固定客户总价和最终服务层级，账号倍率由结算快照另行处理。
type AccountStatsCostInput struct {
	AccountID, GroupID                         int64
	UpstreamModel, RequestedModel, MappedModel string
	Tokens                                     UsageTokens
	RequestCount                               int
	UserTotalCost                              float64
	ServiceTier, ReasoningEffort               string
}

func (r *PriceResolver) ResolveAccountStats(ctx context.Context, input AccountStatsCostInput) *float64 {
	if r == nil || r.accountStats == nil || input.UpstreamModel == "" {
		return nil
	}
	channel, err := r.accountStats.AccountStatsGroup(ctx, input.GroupID)
	if err != nil || channel == nil {
		return nil
	}
	platform := r.accountStats.AccountStatsPlatform(ctx, input.GroupID)
	if cost, handled := purepricing.ResolveAccountStatsOverride(purepricing.AccountStatsInput{
		Rules: channel.Rules, AccountID: input.AccountID, GroupID: input.GroupID, Platform: platform.ID,
		Models: AccountStatsRuleModels(platform.PreferRequestedModel, input.UpstreamModel, input.RequestedModel, input.MappedModel),
		Tokens: input.Tokens, RequestCount: input.RequestCount, UserTotalCost: input.UserTotalCost, ApplyUserPrice: channel.ApplyUserPrice,
	}); handled {
		return cost
	}
	if r.calculator == nil {
		return nil
	}
	return r.calculator.ModelFileStatsCost(input.UpstreamModel, input.Tokens, input.ServiceTier, input.ReasoningEffort)
}

// ModelFileStatsCost 使用与用户计费相同的纯规则，但不读取渠道覆盖价。
func (s *Calculator) ModelFileStatsCost(model string, tokens UsageTokens, serviceTier, effort string) *float64 {
	breakdown, err := s.CalculateCostUnified(CostInput{Model: model, Tokens: tokens, RateMultiplier: 1, ServiceTier: purepricing.NormalizeBillingServiceTier(serviceTier), ReasoningEffort: effort})
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}
func AccountStatsRuleModels(preferRequested bool, upstreamModel, requestedModel string, channelMappedModel ...string) []string {
	upstreamModel = strings.TrimSpace(upstreamModel)
	requestedModel = strings.TrimSpace(requestedModel)
	mappedModel := ""
	if len(channelMappedModel) > 0 {
		mappedModel = strings.TrimSpace(channelMappedModel[0])
	}
	if !preferRequested || requestedModel == "" ||
		(requestedModel == upstreamModel && (mappedModel == "" || mappedModel == requestedModel)) {
		if upstreamModel == "" {
			return nil
		}
		return []string{upstreamModel}
	}
	models := []string{requestedModel}
	models = append(models, mappedModel)
	models = append(models, upstreamModel)
	return purepricing.UniqueNonEmptyAccountStatsModels(models)
}
