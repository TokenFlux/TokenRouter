package service

import (
	"context"
	"log/slog"
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
}

// ModelPricingResolver 统一模型定价解析器。
// 解析链：Group → Channel → Qoder 手动价要求 → LiteLLM → Fallback。
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
	Model         string
	GroupID       *int64 // nil 表示不检查渠道
	BaseModelHint string // 可选：最终上游模型提示，用于 Qoder 自定义 alias 的部分手动价回退
	Group         *Group
}

// Resolve 解析模型定价。
// 1. 获取基础定价（Qoder 已知 alias 为 0 / 其他模型 LiteLLM → Fallback）
// 2. 如果指定了 GroupID，查找渠道定价并覆盖
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	longContextPricingEnabled := input.Group == nil || input.Group.LongContextPricingEnabled
	if groupPricing := matchGroupModelPricing(input.Group, input.Model); groupPricing != nil {
		// 分组 token 价格卡只覆盖基础价格，长上下文倍率仍使用内置模型价格并受分组开关控制。
		if groupPricing.BillingMode == "" || groupPricing.BillingMode == BillingModeToken {
			stripped := groupPricing.Clone()
			stripped.Intervals = nil
			groupPricing = &stripped
		}
		resolved := r.resolveConfiguredPricing(groupPricing, input.Model, PricingSourceGroup)
		resolved.longContextPricingEnabled = longContextPricingEnabled
		return resolved
	}

	qoderManualOnly := false
	var chPricing *ChannelModelPricing
	if input.GroupID != nil && r.channelService != nil {
		qoderManualOnly = r.isQoderManualPricingOnlyModel(ctx, *input.GroupID, input)
		chPricing = r.channelService.GetEffectiveChannelModelPricing(ctx, *input.GroupID, input.Model)
		if chPricing != nil {
			mode := chPricing.BillingMode
			if mode == "" {
				mode = BillingModeToken
			}
			if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
				// 按次/图片渠道价不依赖基础 token 定价，直接返回可避免先触发
				// LiteLLM/OpenAI 的全局 fallback 查询，再被渠道价覆盖。
				resolved := &ResolvedPricing{
					Mode:           mode,
					Source:         PricingSourceChannel,
					channelPricing: chPricing,
				}
				resolved.longContextPricingEnabled = longContextPricingEnabled
				r.applyRequestTierOverrides(chPricing, resolved)
				applyResolvedPriceMultiplier(resolved, chPricing)
				return resolved
			}
		}
	}

	// 1. 获取基础定价
	var basePricing *ModelPricing
	source := PricingSourceUnpriced
	if qoderManualOnly {
		// Qoder 的内置 alias/route key 必须通过渠道定价手动设价；
		// 未配置字段保持 0，避免回退到 Opus 或模型文件价格造成误扣。
		basePricing = &ModelPricing{}
	} else {
		basePricing, source = r.resolveBasePricing(input.Model)
	}

	resolved := &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            basePricing,
		Source:                 source,
		SupportsCacheBreakdown: basePricing != nil && basePricing.SupportsCacheBreakdown,
		SupportsServiceTier:    basePricing != nil && basePricing.SupportsServiceTier,
	}
	resolved.longContextPricingEnabled = longContextPricingEnabled

	// 2. 如果有 GroupID，尝试渠道覆盖
	if chPricing != nil {
		resolved.Source = PricingSourceChannel
		resolved.channelPricing = chPricing
		r.applyTokenOverrides(chPricing, resolved)
		applyResolvedPriceMultiplier(resolved, chPricing)
		applyResolvedFastModeMultiplier(resolved, chPricing)
	} else if input.GroupID != nil && r.channelService != nil {
		r.applyChannelOverrides(ctx, *input.GroupID, input.Model, resolved)
	}

	return resolved
}

func (r *ResolvedPricing) IsUnpriced() bool {
	return r != nil && r.Source == PricingSourceUnpriced
}

func (r *ResolvedPricing) HasEffectiveChannelPricing() bool {
	return r != nil && r.Source == PricingSourceChannel && r.channelPricing != nil && r.channelPricing.HasEffectivePricing()
}

// HasEffectiveOverridePricing 判断分组或渠道是否提供了显式价格，包括显式零价。
func (r *ResolvedPricing) HasEffectiveOverridePricing() bool {
	return r != nil && (r.Source == PricingSourceGroup || r.Source == PricingSourceChannel) &&
		r.channelPricing != nil && r.channelPricing.HasEffectivePricing()
}

func (r *ModelPricingResolver) isQoderManualPricingOnlyModel(ctx context.Context, groupID int64, input PricingInput) bool {
	if r.channelService.GetGroupPlatform(ctx, groupID) != PlatformQoder {
		return false
	}
	model := strings.TrimSpace(input.Model)
	if qoderAliasRequiresManualPricingAny(model) {
		return true
	}
	if r.hasDefaultPricingForModel(model) {
		return false
	}
	if hint := strings.TrimSpace(input.BaseModelHint); hint != "" && hint != model {
		if qoderAliasRequiresManualPricingAny(hint) {
			return true
		}
	}
	if mapping := r.channelService.ResolveChannelMapping(ctx, groupID, input.Model); mapping.Mapped {
		return qoderAliasRequiresManualPricingAny(mapping.MappedModel)
	}
	return false
}

func (r *ModelPricingResolver) hasDefaultPricingForModel(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || r == nil || r.billingService == nil {
		return false
	}
	pricing, err := r.billingService.GetModelPricing(model)
	return err == nil && pricing != nil && hasAnyDisplayTokenPricing(pricing)
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
	model = normalizeChannelPricingModelName(model)
	var wildcard *ChannelModelPricing
	for i := range group.ModelPricing {
		entry := &group.ModelPricing[i]
		for _, pattern := range entry.Models {
			normalized := normalizeChannelPricingModelName(pattern)
			if normalized == model {
				cp := entry.Clone()
				return &cp
			}
			if strings.HasSuffix(normalized, "*") && strings.HasPrefix(model, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
				cp := entry.Clone()
				wildcard = &cp
			}
		}
	}
	return wildcard
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

// applyChannelOverrides 应用渠道定价覆盖
func (r *ModelPricingResolver) applyChannelOverrides(ctx context.Context, groupID int64, model string, resolved *ResolvedPricing) {
	// 精简构造或测试环境可能未注入渠道服务，此时只使用基础模型定价。
	if r == nil || r.channelService == nil {
		return
	}
	chPricing := r.channelService.GetEffectiveChannelModelPricing(ctx, groupID, model)
	if chPricing == nil {
		return
	}

	resolved.Source = PricingSourceChannel
	resolved.channelPricing = chPricing
	resolved.Mode = chPricing.BillingMode
	if resolved.Mode == "" {
		resolved.Mode = BillingModeToken
	}

	switch resolved.Mode {
	case BillingModeToken:
		r.applyTokenOverrides(chPricing, resolved)
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		r.applyRequestTierOverrides(chPricing, resolved)
	}
	applyResolvedPriceMultiplier(resolved, chPricing)
	applyResolvedFastModeMultiplier(resolved, chPricing)
}

// applyTokenOverrides 应用 token 模式的渠道覆盖
func (r *ModelPricingResolver) applyTokenOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	// 过滤掉所有价格字段都为空的无效 interval
	validIntervals := filterValidTokenIntervals(chPricing.Intervals)

	// 如果有有效的区间定价，使用区间
	if len(validIntervals) > 0 {
		resolved.Intervals = validIntervals
		// 区间不匹配时回退到基础定价，也需要覆盖图片价格
		if resolved.BasePricing == nil {
			resolved.BasePricing = &ModelPricing{}
		} else {
			// 防止修改 fallbackPrices 中的共享指针
			cloned := *resolved.BasePricing
			resolved.BasePricing = &cloned
		}
		if chPricing.ImageOutputPrice != nil {
			resolved.BasePricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
		} else {
			resolved.BasePricing.ImageOutputPricePerToken = 0
		}
		resolved.BasePricing.ImageOutputPriceExplicit = true
		applyChannelImageInputPrice(chPricing, resolved.BasePricing)
		return
	}

	// 否则用 flat 字段覆盖 BasePricing；未配置的 token 价格字段继续保留基础定价，
	// 便于只手动覆盖其中一部分字段。
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// 防止修改 fallbackPrices 中的共享指针
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	if chPricing.InputPrice != nil {
		resolved.BasePricing.InputPricePerToken = *chPricing.InputPrice
		resolved.BasePricing.InputPricePerTokenPriority = *chPricing.InputPrice
	}
	if chPricing.OutputPrice != nil {
		resolved.BasePricing.OutputPricePerToken = *chPricing.OutputPrice
		resolved.BasePricing.OutputPricePerTokenPriority = *chPricing.OutputPrice
	}
	if chPricing.CacheWritePrice != nil {
		resolved.BasePricing.CacheCreationPricePerToken = *chPricing.CacheWritePrice
		resolved.BasePricing.CacheCreationPricePerTokenPriority = *chPricing.CacheWritePrice
		resolved.BasePricing.CacheCreationPriceExplicit = true
		resolved.BasePricing.CacheCreation5mPrice = *chPricing.CacheWritePrice
		resolved.BasePricing.CacheCreation1hPrice = *chPricing.CacheWritePrice
	}
	if chPricing.CacheReadPrice != nil {
		resolved.BasePricing.CacheReadPricePerToken = *chPricing.CacheReadPrice
		resolved.BasePricing.CacheReadPricePerTokenPriority = *chPricing.CacheReadPrice
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
			iv.CacheWritePrice != nil || iv.CacheReadPrice != nil {
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
	if len(resolved.Intervals) == 0 {
		return resolved.BasePricing
	}

	iv := FindMatchingInterval(resolved.Intervals, totalContextTokens)
	if iv == nil {
		return resolved.BasePricing
	}

	return intervalToModelPricingWithBase(iv, resolved.SupportsCacheBreakdown, resolved.channelPricing, resolved.BasePricing)
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
		pricing.InputPricePerToken = *iv.InputPrice
		pricing.InputPricePerTokenPriority = *iv.InputPrice
	}
	if iv.OutputPrice != nil {
		pricing.OutputPricePerToken = *iv.OutputPrice
		pricing.OutputPricePerTokenPriority = *iv.OutputPrice
	}
	if iv.CacheWritePrice != nil {
		pricing.CacheCreationPricePerToken = *iv.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = *iv.CacheWritePrice
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *iv.CacheWritePrice
		pricing.CacheCreation1hPrice = *iv.CacheWritePrice
	}
	if iv.CacheReadPrice != nil {
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
		pricing.CacheReadPricePerTokenPriority = *iv.CacheReadPrice
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
