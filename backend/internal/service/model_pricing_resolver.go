// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	slog "log/slog"
)

const PricingSourceGroup = purepricing.PricingSourceGroup

const PricingSourceChannel = purepricing.PricingSourceChannel

const PricingSourceLiteLLM = purepricing.PricingSourceLiteLLM

const PricingSourceFallback = purepricing.PricingSourceFallback

const PricingSourceUnpriced = purepricing.PricingSourceUnpriced

// ResolvedPricing 保留旧解析结果入口，由纯定价包唯一拥有。
type ResolvedPricing = purepricing.ResolvedPricing

// PricingInput 定价解析输入
type PricingInput struct {
	Model   string
	GroupID *int64 // nil 表示不检查渠道
	Group   *Group
}

// Resolve 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	return r.PriceResolver.Resolve(ctx, billing.PricingInput{Model: input.Model, GroupID: input.GroupID, Group: projectPriceGroup(input.Group)})
}

// applyPricingModifiers 委托纯定价实现，旧查询与配置投影保留在适配层。
func applyPricingModifiers(resolved *ResolvedPricing, config *ChannelModelPricing) {
	purepricing.ApplyPricingModifiers(resolved, config)
}

// GetIntervalPricing 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return r.PriceResolver.GetIntervalPricing(resolved, totalContextTokens)
}

// GetRequestTierPrice 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	return r.PriceResolver.GetRequestTierPrice(resolved, tierLabel)
}

// GetRequestTierPriceValue 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	return r.PriceResolver.GetRequestTierPriceValue(resolved, tierLabel)
}

// GetRequestTierPriceByContext 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	return r.PriceResolver.GetRequestTierPriceByContext(resolved, totalContextTokens)
}

// GetRequestTierPriceByContextValue 委托唯一 billing 查价规则。
func (r *ModelPricingResolver) GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	return r.PriceResolver.GetRequestTierPriceByContextValue(resolved, totalContextTokens)
}

type ModelPricingResolver struct {
	*billing.PriceResolver
	billingService *BillingService
	channelService *ChannelService // 旧任务入口仍需渠道路由映射，S13 退出。
}

func NewModelPricingResolver(channels *ChannelService, calculator *BillingService) *ModelPricingResolver {
	var source billing.ChannelPrices
	if channels != nil {
		source = channels
	}
	var stats billing.AccountStatsSource
	if channels != nil {
		stats = LegacyAccountStatsSource{Service: channels}
	}
	core := billing.NewPriceResolver(source, calculator.Calculator, PricingModelIdentity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, stats)
	return &ModelPricingResolver{PriceResolver: core, billingService: calculator, channelService: channels}
}
func WrapPriceResolver(core *billing.PriceResolver, calculator *BillingService, channels *ChannelService) *ModelPricingResolver {
	return &ModelPricingResolver{PriceResolver: core, billingService: calculator, channelService: channels}
}
func projectPriceGroup(group *Group) *billing.PriceGroup {
	if group == nil {
		return nil
	}
	return &billing.PriceGroup{ModelPricing: group.ModelPricing, LongContextPricingEnabled: group.LongContextPricingEnabled}
}

// PricingModelIdentity 每次查询只读取一次旧平台候选快照，S09 退出。
func PricingModelIdentity(model string) billing.ModelIdentity {
	return billing.ModelIdentity{Candidates: buildModelLookupCandidates(model), NormalizedOpenAI: normalizeKnownOpenAICodexModel(model)}
}
