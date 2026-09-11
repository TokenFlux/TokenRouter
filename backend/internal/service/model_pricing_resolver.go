package service

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

const PricingSourceGroup = purepricing.PricingSourceGroup
const PricingSourceChannel = purepricing.PricingSourceChannel
const PricingSourceLiteLLM = purepricing.PricingSourceLiteLLM
const PricingSourceFallback = purepricing.PricingSourceFallback
const PricingSourceUnpriced = purepricing.PricingSourceUnpriced

// ResolvedPricing 保留旧解析结果入口，由纯定价包唯一拥有。
type ResolvedPricing = purepricing.ResolvedPricing

// ModelPricingResolver 统一模型定价解析器。
// 解析链：分组 → 渠道 → 内置目录 → 默认回退，各平台使用相同规则。
type ModelPricingResolver struct {
	channelService *ChannelService
	billingService *BillingService
}

// NewModelPricingResolver 创建定价解析器实例
func NewModelPricingResolver(channelService *ChannelService, billingService *BillingService) *ModelPricingResolver {
	return &ModelPricingResolver{
		channelService: channelService,
		billingService: billingService,
	}
}

// PricingInput 定价解析输入
type PricingInput struct {
	Model   string
	GroupID *int64 // nil 表示不检查渠道
	Group   *Group
}

// Resolve 获取旧分组/渠道输入，纯包唯一决定价卡优先级和金额策略。
// @project-doc docs/domains/routing_and_billing.md#group_model_pricing
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	group := matchGroupModelPricing(input.Group, input.Model)
	var channel *ChannelModelPricing
	if !purepricing.PriceCardOverrides(group) && input.GroupID != nil && r.channelService != nil {
		channel = r.lookupChannelPricingNormalized(ctx, *input.GroupID, input.Model)
	}
	var base *ModelPricing
	source := PricingSourceUnpriced
	if purepricing.PriceCardNeedsBase(purepricing.SelectPriceCard(group, channel)) {
		base, source = r.resolveBasePricing(input.Model)
	}
	return purepricing.ResolvePriceCards(group, channel, base, source, input.Group == nil || input.Group.LongContextPricingEnabled)
}

// applyPricingModifiers 委托纯定价实现，旧查询与配置投影保留在适配层。
func applyPricingModifiers(resolved *ResolvedPricing, config *ChannelModelPricing) {
	purepricing.ApplyPricingModifiers(resolved, config)
}

// matchGroupModelPricing 获取旧分组输入，具体匹配由纯定价唯一执行。
func matchGroupModelPricing(group *Group, model string) *ChannelModelPricing {
	if group == nil {
		return nil
	}
	return lookupPricingForModel(model, func(candidate string) *ChannelModelPricing {
		return purepricing.MatchPriceCard(group.ModelPricing, candidate)
	})
}

// resolveBasePricing 从 LiteLLM 或 Fallback 获取基础定价
func (r *ModelPricingResolver) resolveBasePricing(model string) (*ModelPricing, string) {
	pricing, err := r.billingService.GetModelPricing(model)
	if err != nil {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback",
			"model", model, "error", err)
		return nil, PricingSourceFallback
	}
	return pricing, PricingSourceLiteLLM
}

// lookupChannelPricingNormalized 优先匹配原始请求，再复用目录的明确身份候选。
// 候选不依赖内置价存在，避免新型号的基础名渠道价被目录回退绕过。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
func (r *ModelPricingResolver) lookupChannelPricingNormalized(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	if r == nil || r.channelService == nil {
		return nil
	}
	return lookupPricingForModel(model, func(candidate string) *ChannelModelPricing {
		return r.channelService.GetEffectiveChannelModelPricing(ctx, groupID, candidate)
	})
}

// lookupPricingForModel 统一分组与渠道的候选顺序，完整请求名的精确/通配价卡优先。
func lookupPricingForModel(model string, lookup func(string) *ChannelModelPricing) *ChannelModelPricing {
	if pricing := lookup(model); pricing != nil {
		return pricing
	}
	candidates := buildModelLookupCandidates(model)
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, strings.TrimSpace(model)) {
			continue
		}
		if pricing := lookup(candidate); pricing != nil {
			return pricing
		}
	}
	// 同型号候选均未命中后，再兼容既有 OpenAI 日期和路由名称。
	normalized := normalizeKnownOpenAICodexModel(model)
	if normalized == "" || slices.Contains(candidates, normalized) {
		return nil
	}
	return lookup(normalized)
}

// GetIntervalPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return purepricing.GetIntervalPricing(resolved, totalContextTokens)
}

// GetRequestTierPrice 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	return purepricing.GetRequestTierPrice(resolved, tierLabel)
}

// GetRequestTierPriceValue 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	return purepricing.GetRequestTierPriceValue(resolved, tierLabel)
}

// GetRequestTierPriceByContext 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	return purepricing.GetRequestTierPriceByContext(resolved, totalContextTokens)
}

// GetRequestTierPriceByContextValue 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	return purepricing.GetRequestTierPriceByContextValue(resolved, totalContextTokens)
}
