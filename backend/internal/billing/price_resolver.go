package billing

import (
	context "context"
	slices "slices"
	strings "strings"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
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
	Group   *PriceGroup
}

// Resolve 获取旧分组/渠道输入，纯包唯一决定价卡优先级和金额策略。
// @project-doc docs/domains/routing_and_billing.md#group_model_pricing
func (r *PriceResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	group := MatchGroupModelPricing(input.Group, input.Model, r.lookup)
	var channel *ChannelModelPricing
	if !purepricing.PriceCardOverrides(group) && input.GroupID != nil && r.channels != nil {
		channel = r.LookupChannelPricingNormalized(ctx, *input.GroupID, input.Model)
	}
	var base *ModelPricing
	source := PricingSourceUnpriced
	if purepricing.PriceCardNeedsBase(purepricing.SelectPriceCard(group, channel)) {
		base, source = r.ResolveBasePricing(input.Model)
	}
	return purepricing.ResolvePriceCards(group, channel, base, source, input.Group == nil || input.Group.LongContextPricingEnabled)
}

// MatchGroupModelPricing 获取旧分组输入，具体匹配由纯定价唯一执行。
func MatchGroupModelPricing(group *PriceGroup, model string, candidates ModelCandidates) *ChannelModelPricing {
	if group == nil {
		return nil
	}
	return LookupPricingForModel(model, func(candidate string) *ChannelModelPricing {
		return purepricing.MatchPriceCard(group.ModelPricing, candidate)
	}, candidates)
}

// ResolveBasePricing 从 LiteLLM 或 Fallback 获取基础定价
func (r *PriceResolver) ResolveBasePricing(model string) (*ModelPricing, string) {
	pricing, err := r.calculator.GetModelPricing(model)
	if err != nil {
		r.observe(model, err)
		return nil, PricingSourceFallback
	}
	return pricing, PricingSourceLiteLLM
}

// LookupChannelPricingNormalized 优先匹配原始请求，再复用目录的明确身份候选。
// 候选不依赖内置价存在，避免新型号的基础名渠道价被目录回退绕过。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
func (r *PriceResolver) LookupChannelPricingNormalized(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	if r == nil || r.channels == nil {
		return nil
	}
	return LookupPricingForModel(model, func(candidate string) *ChannelModelPricing {
		return r.channels.GetEffectiveChannelModelPricing(ctx, groupID, candidate)
	}, r.lookup)
}

// LookupPricingForModel 统一分组与渠道的候选顺序，完整请求名的精确/通配价卡优先。
func LookupPricingForModel(model string, lookup func(string) *ChannelModelPricing, identities ModelCandidates) *ChannelModelPricing {
	if pricing := lookup(model); pricing != nil {
		return pricing
	}
	projection := ModelIdentity{}
	if identities != nil {
		projection = identities(model)
	}
	candidates := projection.Candidates
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, strings.TrimSpace(model)) {
			continue
		}
		if pricing := lookup(candidate); pricing != nil {
			return pricing
		}
	}
	// 同型号候选均未命中后，再兼容既有 OpenAI 日期和路由名称。
	normalized := projection.NormalizedOpenAI
	if normalized == "" || slices.Contains(candidates, normalized) {
		return nil
	}
	return lookup(normalized)
}

// GetIntervalPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *PriceResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return purepricing.GetIntervalPricing(resolved, totalContextTokens)
}

// GetRequestTierPrice 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *PriceResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	return purepricing.GetRequestTierPrice(resolved, tierLabel)
}

// GetRequestTierPriceValue 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *PriceResolver) GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	return purepricing.GetRequestTierPriceValue(resolved, tierLabel)
}

// GetRequestTierPriceByContext 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *PriceResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	return purepricing.GetRequestTierPriceByContext(resolved, totalContextTokens)
}

// GetRequestTierPriceByContextValue 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *PriceResolver) GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	return purepricing.GetRequestTierPriceByContextValue(resolved, totalContextTokens)
}

// PriceGroup 只包含定价规则所需字段，nil 与显式空集合保持区别。
type PriceGroup struct {
	ModelPricing              []ChannelModelPricing
	LongContextPricingEnabled bool
}
type ChannelPrices interface {
	GetEffectiveChannelModelPricing(context.Context, int64, string) *ChannelModelPricing
}
type ModelIdentity struct {
	Candidates       []string
	NormalizedOpenAI string
}
type ModelCandidates func(string) ModelIdentity

// PriceResolver 保留分组、渠道、目录的按需读取顺序。
type PriceResolver struct {
	channels     ChannelPrices
	calculator   *Calculator
	lookup       ModelCandidates
	observe      func(string, error)
	accountStats AccountStatsSource
}

func NewPriceResolver(channels ChannelPrices, calculator *Calculator, lookup ModelCandidates, observe func(string, error), stats ...AccountStatsSource) *PriceResolver {
	if observe == nil {
		observe = func(string, error) {}
	}
	var accountStats AccountStatsSource
	if len(stats) > 0 {
		accountStats = stats[0]
	}
	return &PriceResolver{channels: channels, calculator: calculator, lookup: lookup, observe: observe, accountStats: accountStats}
}
