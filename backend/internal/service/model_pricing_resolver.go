package service

import (
	"context"
	"log/slog"
	"slices"
	"strings"
)

// PricingSource 定价来源标识
const (
	PricingSourceGroup    = "group"
	PricingSourceChannel  = "channel"
	PricingSourceLiteLLM  = "litellm"
	PricingSourceFallback = "fallback"
	PricingSourceUnpriced = "unpriced"
)

// ResolvedPricing 统一定价解析结果
type ResolvedPricing struct {
	// Mode 计费模式
	Mode BillingMode

	// Token 模式：基础定价（来自 LiteLLM 或 fallback）
	BasePricing *ModelPricing

	// Token 模式：区间定价列表（如有，覆盖 BasePricing 中的对应字段）
	Intervals []PricingInterval

	// 按次/图片模式：分层定价
	RequestTiers []PricingInterval

	// 按次/图片模式：默认价格（未命中层级时使用）
	DefaultPerRequestPrice float64

	// 来源标识
	Source string // "channel", "litellm", "fallback", "unpriced"

	// 是否支持缓存细分
	SupportsCacheBreakdown bool

	// 是否支持 service_tier（Fast/Flex）
	SupportsServiceTier bool

	// 渠道定价原始配置（用于区间模式下获取图片输出价格）
	channelPricing *ChannelModelPricing

	longContextPricingEnabled bool
	// 空结构仍可承载倍率元数据，但不代表存在基础价；显式零单价不设置此标记。
	basePricingUnavailable bool
}

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

// Resolve 按分组价卡、渠道和内置价格解析；纯倍率价卡只覆盖继承价的对应倍率。
// @project-doc docs/domains/routing_and_billing.md#group_model_pricing
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	groupPricing := matchGroupModelPricing(input.Group, input.Model)
	if groupPricing != nil && (hasExplicitPricingPrice(*groupPricing) || len(filterValidTokenIntervals(groupPricing.Intervals)) > 0) {
		resolved := r.resolveConfiguredPricing(groupPricing, input.Model, PricingSourceGroup)
		resolved.longContextPricingEnabled = input.Group == nil || input.Group.LongContextPricingEnabled
		return resolved
	}
	resolved := r.resolveInheritedPricing(ctx, input)
	applyPricingModifiers(resolved, groupPricing)
	return resolved
}

// resolveInheritedPricing 保留渠道/内置价格来源和计费模式，避免纯倍率配置意外重置基础价。
func (r *ModelPricingResolver) resolveInheritedPricing(ctx context.Context, input PricingInput) *ResolvedPricing {
	var config *ChannelModelPricing
	if input.GroupID != nil && r.channelService != nil {
		config = r.lookupChannelPricingNormalized(ctx, *input.GroupID, input.Model)
	}
	if config != nil && (hasExplicitPricingPrice(*config) || len(filterValidTokenIntervals(config.Intervals)) > 0) {
		resolved := r.resolveConfiguredPricing(config, input.Model, PricingSourceChannel)
		resolved.longContextPricingEnabled = input.Group == nil || input.Group.LongContextPricingEnabled
		return resolved
	}

	// 纯倍率价卡保留内置价格的全部价格桶和来源，内置模型规则也继续生效。
	basePricing, source := r.resolveBasePricing(input.Model)
	resolved := &ResolvedPricing{
		Mode: BillingModeToken, BasePricing: basePricing, Source: source,
		SupportsCacheBreakdown:    basePricing != nil && basePricing.SupportsCacheBreakdown,
		SupportsServiceTier:       basePricing != nil && basePricing.SupportsServiceTier,
		longContextPricingEnabled: input.Group == nil || input.Group.LongContextPricingEnabled,
	}
	applyPricingModifiers(resolved, config)
	return resolved
}

// applyPricingModifiers 在独立副本上覆盖同名倍率；不创建基础价格，也不切换按次计费。
func applyPricingModifiers(resolved *ResolvedPricing, config *ChannelModelPricing) {
	if resolved == nil || resolved.Mode != BillingModeToken || config == nil || resolved.BasePricing == nil {
		return
	}
	if config.FastMultiplier == nil && config.FastModeMultiplier == nil && config.FlexMultiplier == nil &&
		config.MaxReasoningEffortMultiplier == nil && config.TimePricing == nil {
		return
	}
	pricing := *resolved.BasePricing
	resolved.BasePricing = &pricing
	merged := ChannelModelPricing{BillingMode: BillingModeToken}
	if resolved.channelPricing != nil {
		merged = resolved.channelPricing.Clone()
	}
	// 区间解析会再次读取这些元数据，因此同步覆盖其副本，避免旧渠道倍率覆盖分组值。
	if config.FastMultiplier != nil || config.FastModeMultiplier != nil {
		merged.FastMultiplier = config.FastMultiplier
		merged.FastModeMultiplier = config.FastModeMultiplier
	}
	if config.FlexMultiplier != nil {
		merged.FlexMultiplier = config.FlexMultiplier
	}
	if config.MaxReasoningEffortMultiplier != nil {
		merged.MaxReasoningEffortMultiplier = config.MaxReasoningEffortMultiplier
		pricing.MaxReasoningEffortMultiplier = config.MaxReasoningEffortMultiplier
	}
	if config.TimePricing != nil && len(config.TimePricing.Periods) > 0 {
		merged.TimePricing = config.Clone().TimePricing
	}
	resolved.channelPricing = &merged
	applyResolvedFastModeMultiplier(resolved, &merged)
}

func (r *ResolvedPricing) IsUnpriced() bool {
	if r == nil {
		return false
	}
	if r.Source == PricingSourceUnpriced {
		return true
	}
	// 没有显式价卡时保留图片等独立价格回退；这里只阻止已配置但缺少基础价的倍率条目。
	if r.Mode != BillingModeToken || r.channelPricing == nil || r.hasBaseTokenPricing() {
		return false
	}
	for _, interval := range r.Intervals {
		if pricingIntervalHasEffectiveTokenPricing(interval) {
			return false
		}
	}
	return true
}

// hasBaseTokenPricing 区分有效基础价（包含显式免费）与仅用于保存倍率的占位结构。
func (r *ResolvedPricing) hasBaseTokenPricing() bool {
	return r != nil && r.BasePricing != nil && !r.basePricingUnavailable
}

// tokenPricingForInterval 同时供结算和展示使用；没有基础价的纯倍率区间保持未定价。
func (r *ResolvedPricing) tokenPricingForInterval(interval *PricingInterval) *ModelPricing {
	if r == nil {
		return nil
	}
	if interval == nil {
		if r.hasBaseTokenPricing() {
			return r.BasePricing
		}
		return nil
	}
	if !r.hasBaseTokenPricing() && !pricingIntervalHasEffectiveTokenPricing(*interval) {
		return nil
	}
	pricing := intervalToModelPricingWithBase(interval, r.SupportsCacheBreakdown, r.channelPricing, r.BasePricing)
	pricing.SupportsServiceTier = r.SupportsServiceTier
	return pricing
}

func (r *ResolvedPricing) HasEffectiveChannelPricing() bool {
	return r != nil && !r.IsUnpriced() && r.Source == PricingSourceChannel && r.channelPricing != nil && r.channelPricing.HasEffectivePricing()
}

// HasEffectiveOverridePricing 判断分组或渠道是否提供了显式价格，包括显式零价。
func (r *ResolvedPricing) HasEffectiveOverridePricing() bool {
	return r != nil && !r.IsUnpriced() && (r.Source == PricingSourceGroup || r.Source == PricingSourceChannel) &&
		r.channelPricing != nil && r.channelPricing.HasEffectivePricing()
}

func (r *ModelPricingResolver) resolveConfiguredPricing(config *ChannelModelPricing, model, source string) *ResolvedPricing {
	mode := config.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	resolved := &ResolvedPricing{Mode: mode, Source: source, channelPricing: config}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		r.applyRequestTierOverrides(config, resolved)
		applyResolvedPriceMultiplier(resolved, config)
		return resolved
	}
	resolved.BasePricing, _ = r.resolveBasePricing(model)
	resolved.SupportsCacheBreakdown = resolved.BasePricing != nil && resolved.BasePricing.SupportsCacheBreakdown
	resolved.SupportsServiceTier = resolved.BasePricing != nil && resolved.BasePricing.SupportsServiceTier
	r.applyTokenOverrides(config, resolved)
	applyResolvedPriceMultiplier(resolved, config)
	applyResolvedFastModeMultiplier(resolved, config)
	return resolved
}

func matchGroupModelPricing(group *Group, model string) *ChannelModelPricing {
	if group == nil {
		return nil
	}
	return lookupPricingForModel(model, func(candidate string) *ChannelModelPricing {
		candidate = normalizeChannelPricingModelName(candidate)
		var wildcard *ChannelModelPricing
		for i := range group.ModelPricing {
			entry := &group.ModelPricing[i]
			if !entry.HasEffectivePricing() {
				continue
			}
			for _, pattern := range entry.Models {
				normalized := normalizeChannelPricingModelName(pattern)
				if normalized == candidate {
					cp := entry.Clone()
					return &cp
				}
				if strings.HasSuffix(normalized, "*") && strings.HasPrefix(candidate, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
					cp := entry.Clone()
					wildcard = &cp
				}
			}
		}
		return wildcard
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

// applyTokenOverrides 应用 token 模式的渠道覆盖
func (r *ModelPricingResolver) applyTokenOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	// 过滤掉所有价格字段都为空的无效 interval
	validIntervals := filterValidTokenIntervals(chPricing.Intervals)
	// 配置独立的 1h 缓存写入价时，强制启用缓存 TTL 明细计费。
	if chPricing.CacheWrite1hPrice != nil {
		resolved.SupportsCacheBreakdown = true
	}
	for _, iv := range validIntervals {
		if iv.CacheWrite1hPrice != nil {
			resolved.SupportsCacheBreakdown = true
			break
		}
	}

	// 先把价卡默认单价覆盖到独立的基础价，再保存区间；区间留空字段、倍率及
	// 未命中区间时都使用这份基础价，不能跳过默认单价而回退内置价或零价。
	// 只有默认单价能建立基础价；区间单价仅在命中该区间时生效。
	baseConfig := *chPricing
	baseConfig.Intervals = nil
	resolved.basePricingUnavailable = !resolved.hasBaseTokenPricing() && !hasExplicitPricingPrice(baseConfig)
	resolved.Intervals = validIntervals
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// 防止修改 fallbackPrices 中的共享指针
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	applyChannelTokenPriceOverrides(resolved.BasePricing, chPricing)
	if resolved.SupportsCacheBreakdown {
		resolved.BasePricing.SupportsCacheBreakdown = true
	}
	// 图片输出价格与 token 价格不同：nil 表示该渠道未启用图片 token 计费，
	// 因此显式归零，避免意外回退到模型默认图片价格。
	if chPricing.ImageOutputPrice != nil {
		resolved.BasePricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
	} else {
		resolved.BasePricing.ImageOutputPricePerToken = 0
	}
	resolved.BasePricing.ImageOutputPriceExplicit = true
	applyChannelImageInputPrice(chPricing, resolved.BasePricing)
	if chPricing.MaxReasoningEffortMultiplier != nil {
		resolved.BasePricing.MaxReasoningEffortMultiplier = chPricing.MaxReasoningEffortMultiplier
	}
}

// applyChannelImageInputPrice 应用渠道图片输入价：显式配置则用配置值；
// 未配置时归零，使 computeTokenBreakdown 回退到文本输入价（向后兼容，
// 避免 LiteLLM 图片输入价泄漏进渠道自定义定价）。
// 与 image_output 不同，此处不设 Explicit 标志——图片输入未配置应回退文本价，
// 而非硬置 0。
func applyChannelImageInputPrice(chPricing *ChannelModelPricing, pricing *ModelPricing) {
	if chPricing != nil && chPricing.ImageInputPrice != nil {
		pricing.ImageInputPricePerToken = *chPricing.ImageInputPrice
	} else {
		pricing.ImageInputPricePerToken = 0
	}
}

// applyRequestTierOverrides 应用按次/图片模式的渠道覆盖
func (r *ModelPricingResolver) applyRequestTierOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	resolved.RequestTiers = filterValidRequestIntervals(chPricing.Intervals)
	if chPricing.PerRequestPrice != nil {
		resolved.DefaultPerRequestPrice = *chPricing.PerRequestPrice
	}
}

// applyResolvedPriceMultiplier 在渠道价覆盖完成后缩放最终价格。
// 配置校验保证倍率只会与至少一个显式价格同时存在。
func applyResolvedPriceMultiplier(resolved *ResolvedPricing, chPricing *ChannelModelPricing) {
	multiplier, configured := normalizedPriceMultiplier(chPricing)
	if resolved == nil || !configured {
		return
	}

	resolved.BasePricing = multiplyModelPricing(resolved.BasePricing, multiplier)
	resolved.Intervals = multiplyPricingIntervals(resolved.Intervals, multiplier)
	resolved.RequestTiers = multiplyPricingIntervals(resolved.RequestTiers, multiplier)
	resolved.DefaultPerRequestPrice *= multiplier

	// 区间转模型价格时还会读取渠道级图片价格，因此保存一份同步缩放的副本。
	scaledChannelPricing := chPricing.Clone()
	scaledChannelPricing.PriceMultiplier = nil
	multiplyChannelPricingFields(&scaledChannelPricing, multiplier)
	resolved.channelPricing = &scaledChannelPricing
}

// applyResolvedFastModeMultiplier 在普通渠道价完成覆盖和缩放后附加 Fast 计费倍率。
func applyResolvedFastModeMultiplier(resolved *ResolvedPricing, chPricing *ChannelModelPricing) {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return
	}
	applyChannelFastModeMultiplier(resolved.BasePricing, chPricing)
	applyChannelFlexMultiplier(resolved.BasePricing, chPricing)
}

// normalizedPriceMultiplier 返回可安全用于计费的倍率；未配置时不触发任何缩放。
func normalizedPriceMultiplier(pricing *ChannelModelPricing) (float64, bool) {
	if pricing == nil || pricing.PriceMultiplier == nil {
		return 1, false
	}
	if *pricing.PriceMultiplier < 0 {
		return 0, true
	}
	return *pricing.PriceMultiplier, true
}

// multiplyModelPricing 复制并缩放所有金额字段，不修改长上下文本身的倍率配置。
func multiplyModelPricing(pricing *ModelPricing, multiplier float64) *ModelPricing {
	if pricing == nil {
		return nil
	}
	scaled := *pricing
	scaled.InputPricePerToken *= multiplier
	scaled.InputPricePerTokenPriority *= multiplier
	scaled.ImageInputPricePerToken *= multiplier
	scaled.OutputPricePerToken *= multiplier
	scaled.OutputPricePerTokenPriority *= multiplier
	scaled.CacheCreationPricePerToken *= multiplier
	scaled.CacheCreationPricePerTokenPriority *= multiplier
	scaled.CacheReadPricePerToken *= multiplier
	scaled.CacheReadPricePerTokenPriority *= multiplier
	scaled.CacheCreation5mPrice *= multiplier
	scaled.CacheCreation1hPrice *= multiplier
	scaled.ImageOutputPricePerToken *= multiplier
	return &scaled
}

// multiplyPricingIntervals 返回独立的区间切片，并缩放其中所有价格字段。
func multiplyPricingIntervals(intervals []PricingInterval, multiplier float64) []PricingInterval {
	if intervals == nil {
		return nil
	}
	scaled := make([]PricingInterval, len(intervals))
	for i := range intervals {
		scaled[i] = intervals[i]
		scaled[i].InputPrice = multiplyPricePointer(intervals[i].InputPrice, multiplier)
		scaled[i].OutputPrice = multiplyPricePointer(intervals[i].OutputPrice, multiplier)
		scaled[i].CacheWritePrice = multiplyPricePointer(intervals[i].CacheWritePrice, multiplier)
		scaled[i].CacheWrite1hPrice = multiplyPricePointer(intervals[i].CacheWrite1hPrice, multiplier)
		scaled[i].CacheReadPrice = multiplyPricePointer(intervals[i].CacheReadPrice, multiplier)
		scaled[i].PerRequestPrice = multiplyPricePointer(intervals[i].PerRequestPrice, multiplier)
	}
	return scaled
}

// multiplyChannelPricingFields 缩放渠道配置副本中的显式价格字段。
func multiplyChannelPricingFields(pricing *ChannelModelPricing, multiplier float64) {
	if pricing == nil {
		return
	}
	pricing.InputPrice = multiplyPricePointer(pricing.InputPrice, multiplier)
	pricing.OutputPrice = multiplyPricePointer(pricing.OutputPrice, multiplier)
	pricing.CacheWritePrice = multiplyPricePointer(pricing.CacheWritePrice, multiplier)
	pricing.CacheWrite1hPrice = multiplyPricePointer(pricing.CacheWrite1hPrice, multiplier)
	pricing.CacheReadPrice = multiplyPricePointer(pricing.CacheReadPrice, multiplier)
	pricing.ImageInputPrice = multiplyPricePointer(pricing.ImageInputPrice, multiplier)
	pricing.ImageOutputPrice = multiplyPricePointer(pricing.ImageOutputPrice, multiplier)
	pricing.PerRequestPrice = multiplyPricePointer(pricing.PerRequestPrice, multiplier)
	pricing.Intervals = multiplyPricingIntervals(pricing.Intervals, multiplier)
}

func multiplyPricePointer(price *float64, multiplier float64) *float64 {
	if price == nil {
		return nil
	}
	scaled := *price * multiplier
	return &scaled
}

// filterValidTokenIntervals 过滤掉 token 模式下没有 token 价格字段的无效 interval。
// 前端可能创建了只有 min/max 但无价格的空 interval；mode 切换后残留的
// per_request 字段也不能让 token 计费误判为有效区间。
func filterValidTokenIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil ||
			iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil ||
			iv.InputMultiplier != nil || iv.OutputMultiplier != nil ||
			iv.CacheWriteMultiplier != nil || iv.CacheReadMultiplier != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// filterValidRequestIntervals 过滤掉 per_request / image 模式下没有按次价格的无效 interval。
func filterValidRequestIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.PerRequestPrice != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// GetIntervalPricing 根据 context token 数获取区间定价。
// 如果有区间列表，找到匹配区间并构造 ModelPricing；否则直接返回 BasePricing。
func (r *ModelPricingResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return resolved.tokenPricingForInterval(FindMatchingInterval(resolved.Intervals, totalContextTokens))
}

// intervalToModelPricing 将区间定价转换为 ModelPricing
//
//nolint:unused // 兼容旧测试入口；生产路径需要 base pricing fallback 并调用 WithBase 版本。
func intervalToModelPricing(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ChannelModelPricing) *ModelPricing {
	return intervalToModelPricingWithBase(iv, supportsCacheBreakdown, chPricing, nil)
}

func intervalToModelPricingWithBase(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ChannelModelPricing, base *ModelPricing) *ModelPricing {
	if iv == nil {
		return base
	}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: supportsCacheBreakdown,
	}
	if base != nil {
		cloned := *base
		pricing = &cloned
		pricing.SupportsCacheBreakdown = supportsCacheBreakdown
		// 区间价本身已经表达上下文分段；不要把模型文件里的长上下文
		// 阈值再次带入展示或后续计算。
		pricing.LongContextInputThreshold = 0
		pricing.LongContextInputMultiplier = 0
		pricing.LongContextOutputMultiplier = 0
	}
	if iv.InputPrice != nil {
		priority := channelTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *iv.InputPrice)
		pricing.InputPricePerToken = *iv.InputPrice
		pricing.InputPricePerTokenPriority = priority
	} else if iv.InputMultiplier != nil {
		pricing.InputPricePerToken *= *iv.InputMultiplier
		pricing.InputPricePerTokenPriority *= *iv.InputMultiplier
	}
	if iv.OutputPrice != nil {
		priority := channelTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *iv.OutputPrice)
		pricing.OutputPricePerToken = *iv.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	} else if iv.OutputMultiplier != nil {
		pricing.OutputPricePerToken *= *iv.OutputMultiplier
		pricing.OutputPricePerTokenPriority *= *iv.OutputMultiplier
	}
	if iv.CacheWritePrice != nil {
		priority := channelTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *iv.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *iv.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *iv.CacheWritePrice
		if iv.CacheWrite1hPrice == nil {
			// 兼容旧配置：只有 cache_write_price 时两种 TTL 使用同一单价。
			pricing.CacheCreation1hPrice = *iv.CacheWritePrice
		}
	} else if iv.CacheWriteMultiplier != nil {
		pricing.CacheCreationPricePerToken *= *iv.CacheWriteMultiplier
		pricing.CacheCreationPricePerTokenPriority *= *iv.CacheWriteMultiplier
		pricing.CacheCreation5mPrice *= *iv.CacheWriteMultiplier
		pricing.CacheCreation1hPrice *= *iv.CacheWriteMultiplier
	}
	if iv.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *iv.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if iv.CacheReadPrice != nil {
		priority := channelTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *iv.CacheReadPrice)
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
		pricing.CacheReadPricePerTokenPriority = priority
	} else if iv.CacheReadMultiplier != nil {
		pricing.CacheReadPricePerToken *= *iv.CacheReadMultiplier
		pricing.CacheReadPricePerTokenPriority *= *iv.CacheReadMultiplier
	}
	// 渠道定价存在时显式覆盖图片输出价格；图片输入价格沿用渠道级配置，区间本身不携带该字段。
	if chPricing != nil {
		pricing.ImageOutputPriceExplicit = true
		if chPricing.ImageOutputPrice != nil {
			pricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
		} else {
			pricing.ImageOutputPricePerToken = 0
		}
		applyChannelImageInputPrice(chPricing, pricing)
		applyChannelFastModeMultiplier(pricing, chPricing)
		applyChannelFlexMultiplier(pricing, chPricing)
	}
	return pricing
}

// GetRequestTierPrice 根据层级标签获取按次价格
func (r *ModelPricingResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	price, ok := r.GetRequestTierPriceValue(resolved, tierLabel)
	if !ok {
		return 0
	}
	return price
}

func (r *ModelPricingResolver) GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	for _, tier := range resolved.RequestTiers {
		if strings.EqualFold(tier.TierLabel, tierLabel) && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice, true
		}
	}
	return 0, false
}

// GetRequestTierPriceByContext 根据 context token 数获取按次价格
func (r *ModelPricingResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	price, ok := r.GetRequestTierPriceByContextValue(resolved, totalContextTokens)
	if !ok {
		return 0
	}
	return price
}

func (r *ModelPricingResolver) GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	iv := FindMatchingInterval(resolved.RequestTiers, totalContextTokens)
	if iv != nil && iv.PerRequestPrice != nil {
		return *iv.PerRequestPrice, true
	}
	return 0, false
}
