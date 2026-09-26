package pricing

import (
	"strings"
)

// PricingSource 定价来源标识
const (
	PricingSourceGroup    = "group"
	PricingSourceConfig   = "pricing_config"
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
	Source string // "configPricing", "litellm", "fallback", "unpriced"

	// 是否支持缓存细分
	SupportsCacheBreakdown bool

	// 是否支持 service_tier（Fast/Flex）
	SupportsServiceTier bool

	// 价卡定价原始配置（用于区间模式下获取图片输出价格）
	ConfigPricing *ModelPricingEntry `json:"-"`

	LongContextPricingEnabled bool `json:"-"`
	// 空结构仍可承载倍率元数据，但不代表存在基础价；显式零单价不设置此标记。
	BasePricingUnavailable bool `json:"-"`
}

// ApplyPricingModifiers 在独立副本上覆盖同名倍率；不创建基础价格，也不切换按次计费。
func ApplyPricingModifiers(resolved *ResolvedPricing, config *ModelPricingEntry) {
	if resolved == nil || resolved.Mode != BillingModeToken || config == nil || resolved.BasePricing == nil {
		return
	}
	if config.FastMultiplier == nil && config.FastModeMultiplier == nil && config.FlexMultiplier == nil &&
		config.MaxReasoningEffortMultiplier == nil && config.TimePricing == nil {
		return
	}
	pricing := *resolved.BasePricing
	resolved.BasePricing = &pricing
	merged := ModelPricingEntry{BillingMode: BillingModeToken}
	if resolved.ConfigPricing != nil {
		merged = resolved.ConfigPricing.Clone()
	}
	// 区间解析会再次读取这些元数据，因此同步覆盖其副本，避免旧价卡倍率覆盖分组值。
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
	resolved.ConfigPricing = &merged
	ApplyResolvedFastModeMultiplier(resolved, &merged)
}

func (r *ResolvedPricing) IsUnpriced() bool {
	if r == nil {
		return false
	}
	if r.Source == PricingSourceUnpriced {
		return true
	}
	// 没有显式价卡时保留图片等独立价格回退；这里只阻止已配置但缺少基础价的倍率条目。
	if r.Mode != BillingModeToken || r.ConfigPricing == nil || r.hasBaseTokenPricing() {
		return false
	}
	for _, interval := range r.Intervals {
		if PricingIntervalHasEffectiveTokenPricing(interval) {
			return false
		}
	}
	return true
}

// hasBaseTokenPricing 区分有效基础价（包含显式免费）与仅用于保存倍率的占位结构。
func (r *ResolvedPricing) hasBaseTokenPricing() bool {
	return r != nil && r.BasePricing != nil && !r.BasePricingUnavailable
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
	if !r.hasBaseTokenPricing() && !PricingIntervalHasEffectiveTokenPricing(*interval) {
		return nil
	}
	pricing := IntervalToModelPricingWithBase(interval, r.SupportsCacheBreakdown, r.ConfigPricing, r.BasePricing)
	pricing.SupportsServiceTier = r.SupportsServiceTier
	return pricing
}

func (r *ResolvedPricing) HasEffectivePricing() bool {
	return r != nil && !r.IsUnpriced() && r.Source == PricingSourceConfig && r.ConfigPricing != nil && r.ConfigPricing.HasEffectivePricing()
}

// HasConfiguredPricing 识别实际参与解析的价卡，包括保留内置来源的纯倍率配置。
// 媒体计费按此结果分流，不能把基础价格来源当成“是否配置价卡”的标志。
func (r *ResolvedPricing) HasConfiguredPricing() bool {
	return r != nil && r.ConfigPricing != nil
}

// HasEffectiveOverridePricing 判断分组或共享价格配置是否提供了显式价格，包括显式零价。
func (r *ResolvedPricing) HasEffectiveOverridePricing() bool {
	return r != nil && !r.IsUnpriced() && (r.Source == PricingSourceGroup || r.Source == PricingSourceConfig) &&
		r.ConfigPricing != nil && r.ConfigPricing.HasEffectivePricing()
}

// ApplyTokenOverrides 应用 token 模式的价卡覆盖
func ApplyTokenOverrides(chPricing *ModelPricingEntry, resolved *ResolvedPricing) {
	// 过滤掉所有价格字段都为空的无效 interval
	validIntervals := FilterValidTokenIntervals(chPricing.Intervals)
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
	resolved.BasePricingUnavailable = !resolved.hasBaseTokenPricing() && !HasExplicitPricingPrice(baseConfig)
	resolved.Intervals = validIntervals
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// 防止修改 fallbackPrices 中的共享指针
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	ApplyConfigTokenPriceOverrides(resolved.BasePricing, chPricing)
	if resolved.SupportsCacheBreakdown {
		resolved.BasePricing.SupportsCacheBreakdown = true
	}
	// 图片输出价格与 token 价格不同：nil 表示该价卡未启用图片 token 计费，
	// 因此显式归零，避免意外回退到模型默认图片价格。
	if chPricing.ImageOutputPrice != nil {
		resolved.BasePricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
	} else {
		resolved.BasePricing.ImageOutputPricePerToken = 0
	}
	resolved.BasePricing.ImageOutputPriceExplicit = true
	ApplyConfigImageInputPrice(chPricing, resolved.BasePricing)
	if chPricing.MaxReasoningEffortMultiplier != nil {
		resolved.BasePricing.MaxReasoningEffortMultiplier = chPricing.MaxReasoningEffortMultiplier
	}
}

// ApplyConfigImageInputPrice 应用价卡图片输入价：显式配置则用配置值；
// 未配置时归零，使 ComputeTokenBreakdown 回退到文本输入价（向后兼容，
// 避免 LiteLLM 图片输入价泄漏进价卡自定义定价）。
// 与 image_output 不同，此处不设 Explicit 标志——图片输入未配置应回退文本价，
// 而非硬置 0。
func ApplyConfigImageInputPrice(chPricing *ModelPricingEntry, pricing *ModelPricing) {
	if chPricing != nil && chPricing.ImageInputPrice != nil {
		pricing.ImageInputPricePerToken = *chPricing.ImageInputPrice
	} else {
		pricing.ImageInputPricePerToken = 0
	}
}

// ApplyRequestTierOverrides 应用按次/图片模式的价卡覆盖
func ApplyRequestTierOverrides(chPricing *ModelPricingEntry, resolved *ResolvedPricing) {
	resolved.RequestTiers = FilterValidRequestIntervals(chPricing.Intervals)
	if chPricing.PerRequestPrice != nil {
		resolved.DefaultPerRequestPrice = *chPricing.PerRequestPrice
	}
}

// ApplyResolvedPriceMultiplier 在价卡价覆盖完成后缩放最终价格。
// 配置校验保证倍率只会与至少一个显式价格同时存在。
func ApplyResolvedPriceMultiplier(resolved *ResolvedPricing, chPricing *ModelPricingEntry) {
	multiplier, configured := NormalizedPriceMultiplier(chPricing)
	if resolved == nil || !configured {
		return
	}

	resolved.BasePricing = MultiplyModelPricing(resolved.BasePricing, multiplier)
	resolved.Intervals = MultiplyPricingIntervals(resolved.Intervals, multiplier)
	resolved.RequestTiers = MultiplyPricingIntervals(resolved.RequestTiers, multiplier)
	resolved.DefaultPerRequestPrice *= multiplier

	// 区间转模型价格时还会读取价卡级图片价格，因此保存一份同步缩放的副本。
	scaledConfigPricing := chPricing.Clone()
	scaledConfigPricing.PriceMultiplier = nil
	MultiplyPricingFields(&scaledConfigPricing, multiplier)
	resolved.ConfigPricing = &scaledConfigPricing
}

// ApplyResolvedFastModeMultiplier 在普通价卡价完成覆盖和缩放后附加 Fast 计费倍率。
func ApplyResolvedFastModeMultiplier(resolved *ResolvedPricing, chPricing *ModelPricingEntry) {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return
	}
	ApplyConfigFastModeMultiplier(resolved.BasePricing, chPricing)
	ApplyConfigFlexMultiplier(resolved.BasePricing, chPricing)
}

// NormalizedPriceMultiplier 返回可安全用于计费的倍率；未配置时不触发任何缩放。
func NormalizedPriceMultiplier(pricing *ModelPricingEntry) (float64, bool) {
	if pricing == nil || pricing.PriceMultiplier == nil {
		return 1, false
	}
	if *pricing.PriceMultiplier < 0 {
		return 0, true
	}
	return *pricing.PriceMultiplier, true
}

// MultiplyModelPricing 复制并缩放所有金额字段，不修改长上下文本身的倍率配置。
func MultiplyModelPricing(pricing *ModelPricing, multiplier float64) *ModelPricing {
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

// MultiplyPricingIntervals 返回独立的区间切片，并缩放其中所有价格字段。
func MultiplyPricingIntervals(intervals []PricingInterval, multiplier float64) []PricingInterval {
	if intervals == nil {
		return nil
	}
	scaled := make([]PricingInterval, len(intervals))
	for i := range intervals {
		scaled[i] = intervals[i]
		scaled[i].InputPrice = MultiplyPricePointer(intervals[i].InputPrice, multiplier)
		scaled[i].OutputPrice = MultiplyPricePointer(intervals[i].OutputPrice, multiplier)
		scaled[i].CacheWritePrice = MultiplyPricePointer(intervals[i].CacheWritePrice, multiplier)
		scaled[i].CacheWrite1hPrice = MultiplyPricePointer(intervals[i].CacheWrite1hPrice, multiplier)
		scaled[i].CacheReadPrice = MultiplyPricePointer(intervals[i].CacheReadPrice, multiplier)
		scaled[i].PerRequestPrice = MultiplyPricePointer(intervals[i].PerRequestPrice, multiplier)
	}
	return scaled
}

// MultiplyPricingFields 缩放价卡配置副本中的显式价格字段。
func MultiplyPricingFields(pricing *ModelPricingEntry, multiplier float64) {
	if pricing == nil {
		return
	}
	pricing.InputPrice = MultiplyPricePointer(pricing.InputPrice, multiplier)
	pricing.OutputPrice = MultiplyPricePointer(pricing.OutputPrice, multiplier)
	pricing.CacheWritePrice = MultiplyPricePointer(pricing.CacheWritePrice, multiplier)
	pricing.CacheWrite1hPrice = MultiplyPricePointer(pricing.CacheWrite1hPrice, multiplier)
	pricing.CacheReadPrice = MultiplyPricePointer(pricing.CacheReadPrice, multiplier)
	pricing.ImageInputPrice = MultiplyPricePointer(pricing.ImageInputPrice, multiplier)
	pricing.ImageOutputPrice = MultiplyPricePointer(pricing.ImageOutputPrice, multiplier)
	pricing.PerRequestPrice = MultiplyPricePointer(pricing.PerRequestPrice, multiplier)
	pricing.Intervals = MultiplyPricingIntervals(pricing.Intervals, multiplier)
}

func MultiplyPricePointer(price *float64, multiplier float64) *float64 {
	if price == nil {
		return nil
	}
	scaled := *price * multiplier
	return &scaled
}

// FilterValidTokenIntervals 过滤掉 token 模式下没有 token 价格字段的无效 interval。
// 前端可能创建了只有 min/max 但无价格的空 interval；mode 切换后残留的
// per_request 字段也不能让 token 计费误判为有效区间。
func FilterValidTokenIntervals(intervals []PricingInterval) []PricingInterval {
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

// FilterValidRequestIntervals 过滤掉 per_request / image 模式下没有按次价格的无效 interval。
func FilterValidRequestIntervals(intervals []PricingInterval) []PricingInterval {
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
func GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return resolved.tokenPricingForInterval(FindMatchingInterval(resolved.Intervals, totalContextTokens))
}

// IntervalToModelPricing 将区间定价转换为 ModelPricing
//
//nolint:unused // 兼容旧测试入口；生产路径需要 base pricing fallback 并调用 WithBase 版本。
func IntervalToModelPricing(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ModelPricingEntry) *ModelPricing {
	return IntervalToModelPricingWithBase(iv, supportsCacheBreakdown, chPricing, nil)
}

func IntervalToModelPricingWithBase(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ModelPricingEntry, base *ModelPricing) *ModelPricing {
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
		priority := ConfigTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *iv.InputPrice)
		pricing.InputPricePerToken = *iv.InputPrice
		pricing.InputPricePerTokenPriority = priority
	} else if iv.InputMultiplier != nil {
		pricing.InputPricePerToken *= *iv.InputMultiplier
		pricing.InputPricePerTokenPriority *= *iv.InputMultiplier
	}
	if iv.OutputPrice != nil {
		priority := ConfigTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *iv.OutputPrice)
		pricing.OutputPricePerToken = *iv.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	} else if iv.OutputMultiplier != nil {
		pricing.OutputPricePerToken *= *iv.OutputMultiplier
		pricing.OutputPricePerTokenPriority *= *iv.OutputMultiplier
	}
	if iv.CacheWritePrice != nil {
		priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *iv.CacheWritePrice)
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
		priority := ConfigTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *iv.CacheReadPrice)
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
		pricing.CacheReadPricePerTokenPriority = priority
	} else if iv.CacheReadMultiplier != nil {
		pricing.CacheReadPricePerToken *= *iv.CacheReadMultiplier
		pricing.CacheReadPricePerTokenPriority *= *iv.CacheReadMultiplier
	}
	// 价卡定价存在时显式覆盖图片输出价格；图片输入价格沿用价卡级配置，区间本身不携带该字段。
	if chPricing != nil {
		pricing.ImageOutputPriceExplicit = true
		if chPricing.ImageOutputPrice != nil {
			pricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
		} else {
			pricing.ImageOutputPricePerToken = 0
		}
		ApplyConfigImageInputPrice(chPricing, pricing)
		ApplyConfigFastModeMultiplier(pricing, chPricing)
		ApplyConfigFlexMultiplier(pricing, chPricing)
	}
	return pricing
}

// GetRequestTierPrice 根据层级标签获取按次价格
func GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	price, ok := GetRequestTierPriceValue(resolved, tierLabel)
	if !ok {
		return 0
	}
	return price
}

func GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	for _, tier := range resolved.RequestTiers {
		if strings.EqualFold(tier.TierLabel, tierLabel) && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice, true
		}
	}
	return 0, false
}

// GetRequestTierPriceByContext 根据 context token 数获取按次价格
func GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	price, ok := GetRequestTierPriceByContextValue(resolved, totalContextTokens)
	if !ok {
		return 0
	}
	return price
}

func GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	iv := FindMatchingInterval(resolved.RequestTiers, totalContextTokens)
	if iv != nil && iv.PerRequestPrice != nil {
		return *iv.PerRequestPrice, true
	}
	return 0, false
}

// HasExplicitPricingPrice 判断是否配置了实际价格，不把层级倍率当作基础价格。
// 这样 price_multiplier 与旧版 fast_mode_multiplier 仍不能单独改变默认定价。
func HasExplicitPricingPrice(p ModelPricingEntry) bool {
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		if p.PerRequestPrice != nil {
			return true
		}
		for _, iv := range p.Intervals {
			if iv.PerRequestPrice != nil {
				return true
			}
		}
		return false
	}
	if p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil || p.CacheWrite1hPrice != nil ||
		p.CacheReadPrice != nil || p.ImageInputPrice != nil || p.ImageOutputPrice != nil {
		return true
	}
	for _, iv := range p.Intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil || iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil {
			return true
		}
	}
	return false
}

// NormalizePriceModelName 统一 Anthropic 模型名的点号与连字符写法。
func NormalizePriceModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "claude-") {
		model = strings.ReplaceAll(model, ".", "-")
	}
	return model
}
