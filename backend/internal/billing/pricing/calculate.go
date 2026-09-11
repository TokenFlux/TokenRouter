package pricing

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

func NormalizeBillingServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}

func UsePriorityServiceTierPricing(serviceTier string, pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	tier := NormalizeBillingServiceTier(serviceTier)
	if tier != "priority" && tier != "fast" {
		return false
	}
	if pricing.FastModeMultiplier != nil || pricing.FastMultiplier != nil {
		return false
	}
	return pricing.InputPricePerTokenPriority > 0 || pricing.OutputPricePerTokenPriority > 0 ||
		pricing.CacheCreationPricePerTokenPriority > 0 || pricing.CacheReadPricePerTokenPriority > 0
}

func ServiceTierCostMultiplier(serviceTier string) float64 {
	switch NormalizeBillingServiceTier(serviceTier) {
	case "priority", "fast", OpenAIFastTierUltrafast:
		return 2.0
	case "flex":
		return 0.5
	default:
		return 1.0
	}
}

// NormalizedFastModeMultiplier 返回渠道 Fast 倍率；负值按 0 防御处理。
func NormalizedFastModeMultiplier(pricing *ModelPricing) (float64, bool) {
	if pricing == nil {
		return 1, false
	}
	configured := pricing.FastModeMultiplier
	if configured == nil {
		configured = pricing.FastMultiplier
	}
	if configured == nil {
		return 1, false
	}
	if *configured < 0 {
		return 0, true
	}
	return *configured, true
}

// ConfiguredServiceTierMultiplier 返回渠道显式层级倍率；未配置时沿用官方默认倍率。
func ConfiguredServiceTierMultiplier(serviceTier string, pricing *ModelPricing) float64 {
	if pricing != nil {
		switch NormalizeBillingServiceTier(serviceTier) {
		case "priority", "fast":
			if multiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
				return multiplier
			}
		case "flex":
			if pricing.FlexMultiplier != nil {
				return *pricing.FlexMultiplier
			}
		}
	}
	return ServiceTierCostMultiplier(serviceTier)
}

// ApplyChannelFastModeMultiplier 将渠道 Fast 倍率写入最终定价元数据。
func ApplyChannelFastModeMultiplier(pricing *ModelPricing, ChannelPricing *ChannelModelPricing) {
	if pricing == nil || ChannelPricing == nil {
		return
	}
	multiplierPtr := ChannelPricing.FastMultiplier
	if multiplierPtr == nil {
		multiplierPtr = ChannelPricing.FastModeMultiplier
	}
	if multiplierPtr == nil {
		return
	}
	multiplier := *multiplierPtr
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FastModeMultiplier = &multiplier
	pricing.FastMultiplier = &multiplier
}

func ApplyChannelFlexMultiplier(pricing *ModelPricing, ChannelPricing *ChannelModelPricing) {
	if pricing == nil || ChannelPricing == nil || ChannelPricing.FlexMultiplier == nil {
		return
	}
	multiplier := *ChannelPricing.FlexMultiplier
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FlexMultiplier = &multiplier
}

// ApplyCostBreakdownMultiplier 将渠道分时倍率应用到所有 token 费用桶。
func ApplyCostBreakdownMultiplier(cost *CostBreakdown, multiplier float64) {
	if cost == nil || multiplier == 1 {
		return
	}
	cost.InputCost *= multiplier
	cost.ImageInputCost *= multiplier
	cost.OutputCost *= multiplier
	cost.ImageOutputCost *= multiplier
	cost.CacheCreationCost *= multiplier
	cost.CacheReadCost *= multiplier
	cost.TotalCost *= multiplier
	cost.ActualCost *= multiplier
}

const ClaudeFable51MaxReasoningEffortMultiplier = 3.0

// IsClaudeFable51Model 判断模型是否属于 Fable 5.1，允许常见的分隔符写法。
func IsClaudeFable51Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, marker := range []string{"fable-5-1", "fable-5.1", "fable5.1", "fable51"} {
		if at := strings.Index(model, marker); at >= 0 {
			after := at + len(marker)
			if after == len(model) || model[after] < '0' || model[after] > '9' {
				return true
			}
		}
	}
	return false
}

func DefaultMaxReasoningEffortMultiplier(model string) *float64 {
	if !IsClaudeFable51Model(model) {
		return nil
	}
	multiplier := ClaudeFable51MaxReasoningEffortMultiplier
	return &multiplier
}

// MaxReasoningEffortBillingMultiplier 返回 max 档位的模型/渠道倍率。
func MaxReasoningEffortBillingMultiplier(model, effort string, pricing *ModelPricing) float64 {
	if protocol.NormalizeClaudeOutputEffort(effort) == nil || !strings.EqualFold(strings.TrimSpace(effort), "max") {
		return 1
	}
	if pricing != nil && pricing.MaxReasoningEffortMultiplier != nil && *pricing.MaxReasoningEffortMultiplier > 0 {
		return *pricing.MaxReasoningEffortMultiplier
	}
	if multiplier := DefaultMaxReasoningEffortMultiplier(model); multiplier != nil {
		return *multiplier
	}
	return 1
}

// ErrModelPricingUnavailable 表示当前所有定价来源都无法为请求模型提供价格。
var ErrModelPricingUnavailable = errors.New("pricing not found")

// DeepSeek 官方价卡以美元/token 表示；峰值时段为工作日 UTC 01:00–04:00
// 与 06:00–10:00，峰值价格是低谷价格的 2 倍。
const (
	DeepseekFlashOffPeakInputPrice  = 2.2e-7
	DeepseekFlashOffPeakOutputPrice = 6.6e-7
	DeepseekFlashOffPeakCacheRead   = 7e-9
	DeepseekProOffPeakInputPrice    = 6.6e-7
	DeepseekProOffPeakOutputPrice   = 1.98e-6
	DeepseekProOffPeakCacheRead     = 2.2e-8
)

// IsDeepSeekModel 判断模型名是否属于 DeepSeek 系列，未知后缀也按 Flash 价卡处理。
func IsDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}

// DeepseekPeakMultiplierAt 返回 DeepSeek 官方峰谷倍率。周末按北京时间判断，
// 其余日期按 UTC 窗口判断，避免服务器时区影响计费结果。
func DeepseekPeakMultiplierAt(now time.Time) float64 {
	beijing := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	if beijing.Weekday() == time.Saturday || beijing.Weekday() == time.Sunday {
		return 1
	}
	hour := now.UTC().Hour()
	if (hour >= 1 && hour < 4) || (hour >= 6 && hour < 10) {
		return 2
	}
	return 1
}

// ApplyDeepSeekOfficialPricing 用官方低谷价覆盖远端或旧的 DeepSeek 价卡，
// 保留其它能力字段，确保渠道/分组显式价格不会经过此函数。
func ApplyDeepSeekOfficialPricing(model string, pricing *ModelPricing) *ModelPricing {
	if pricing == nil || !IsDeepSeekModel(model) {
		return pricing
	}
	cloned := *pricing
	if strings.Contains(strings.ToLower(strings.TrimSpace(model)), "deepseek-v4-pro") {
		cloned.InputPricePerToken = DeepseekProOffPeakInputPrice
		cloned.OutputPricePerToken = DeepseekProOffPeakOutputPrice
		cloned.CacheReadPricePerToken = DeepseekProOffPeakCacheRead
	} else {
		cloned.InputPricePerToken = DeepseekFlashOffPeakInputPrice
		cloned.OutputPricePerToken = DeepseekFlashOffPeakOutputPrice
		cloned.CacheReadPricePerToken = DeepseekFlashOffPeakCacheRead
	}
	return &cloned
}

// ApplyDeepSeekPeakPricing 在默认模型价卡上叠加官方峰值倍率；自定义价格不应调用。
func ApplyDeepSeekPeakPricing(model string, pricing *ModelPricing, pricingAt time.Time) *ModelPricing {
	if pricing == nil || !IsDeepSeekModel(model) {
		return pricing
	}
	multiplier := DeepseekPeakMultiplierAt(pricingAt)
	if multiplier <= 1 {
		return pricing
	}
	cloned := *pricing
	cloned.InputPricePerToken *= multiplier
	cloned.OutputPricePerToken *= multiplier
	cloned.CacheReadPricePerToken *= multiplier
	return &cloned
}

// IsGrokMediaFamilyModel 判断模型 ID 是否属于按图片、视频或音频单位计费的媒体族。
// 带版本号的媒体 ID 不能进入未知文本兜底；vision 多模态对话仍按 token 计费。
func IsGrokMediaFamilyModel(native string) bool {
	for _, marker := range []string{"imagine", "image", "video", "audio", "speech", "tts", "transcribe", "realtime"} {
		if strings.Contains(native, marker) {
			return true
		}
	}
	return false
}

// ChannelTierOverridePrice 根据模型目录中的层级比例推导渠道层级价格。
// 渠道只覆盖普通价时，不能把 priority/Fast 价格也压成普通价。
func ChannelTierOverridePrice(baseStandard, baseTier, channelStandard float64) float64 {
	if baseStandard > 0 && baseTier > 0 {
		return channelStandard * (baseTier / baseStandard)
	}
	return 0
}

// ApplyChannelTokenPriceOverrides 应用渠道 token 价格，同时保留模型内置层级比例。
func ApplyChannelTokenPriceOverrides(pricing *ModelPricing, ChannelPricing *ChannelModelPricing) {
	if pricing == nil || ChannelPricing == nil {
		return
	}
	if ChannelPricing.InputPrice != nil {
		priority := ChannelTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *ChannelPricing.InputPrice)
		pricing.InputPricePerToken = *ChannelPricing.InputPrice
		pricing.InputPricePerTokenPriority = priority
	}
	if ChannelPricing.OutputPrice != nil {
		priority := ChannelTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *ChannelPricing.OutputPrice)
		pricing.OutputPricePerToken = *ChannelPricing.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	}
	if ChannelPricing.CacheWritePrice != nil {
		basePriority := pricing.CacheCreationPricePerTokenPriority
		if pricing.CacheCreationPriorityDerived {
			// 兜底推导的 priority 价不代表模型原生目录配置；渠道显式
			// 覆盖缓存写价时，应继续保持“未配置 priority”的 fork 语义。
			basePriority = 0
		}
		priority := ChannelTierOverridePrice(pricing.CacheCreationPricePerToken, basePriority, *ChannelPricing.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *ChannelPricing.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreationPriorityDerived = false
		pricing.CacheCreation5mPrice = *ChannelPricing.CacheWritePrice
		if ChannelPricing.CacheWrite1hPrice == nil {
			// 兼容旧配置：未拆分时继续让 cache_write_price 覆盖两个 TTL 档位。
			pricing.CacheCreation1hPrice = *ChannelPricing.CacheWritePrice
		}
	}
	if ChannelPricing.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *ChannelPricing.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if ChannelPricing.CacheReadPrice != nil {
		priority := ChannelTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *ChannelPricing.CacheReadPrice)
		pricing.CacheReadPricePerToken = *ChannelPricing.CacheReadPrice
		pricing.CacheReadPricePerTokenPriority = priority
	}
}

// CalculateTokenCost 按 token 区间计费
func CalculateTokenCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens

	pricing := GetIntervalPricing(resolved, totalContext)
	if pricing == nil {
		return nil, fmt.Errorf("no pricing available for model: %s: %w", input.Model, ErrModelPricingUnavailable)
	}

	pricing = ApplyModelSpecificPricingPolicyEx(input.Model, pricing, resolved.Source == PricingSourceLiteLLM || resolved.Source == PricingSourceFallback, input.ModelPolicy)
	if resolved.Source == PricingSourceLiteLLM || resolved.Source == PricingSourceFallback {
		pricing = ApplyDeepSeekPeakPricing(input.Model, pricing, input.ModelPricingAt)
	}

	// 长上下文定价仅在无区间定价且分组允许时应用（区间定价已包含上下文分层）。
	applyLongCtx := len(resolved.Intervals) == 0 && resolved.LongContextPricingEnabled

	breakdown := ComputeTokenBreakdown(pricing, input.Tokens, input.RateMultiplier, input.ServiceTier, applyLongCtx)
	ApplyCostBreakdownMultiplier(breakdown, ResolvedChannelTimeMultiplier(resolved, input.PricingAt, input.TimePricingLocation))
	ApplyCostBreakdownMultiplier(breakdown, MaxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, pricing))
	return breakdown, nil
}

// ComputeTokenBreakdown 是 token 计费的核心逻辑，由 CalculateTokenCost 和 calculateCostInternal 共用。
// applyLongCtx 控制是否检查长上下文定价（区间定价已自含上下文分层，不需要额外应用）。
func ComputeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	// 保存时强制 > 0；若仍有负数泄漏，按 0 处理避免按 1x 误扣。
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	inputPrice := pricing.InputPricePerToken
	outputPrice := pricing.OutputPricePerToken
	cacheReadPrice := pricing.CacheReadPricePerToken
	cacheCreationPrice := pricing.CacheCreationPricePerToken
	cacheCreationMultiplier := 1.0
	tierMultiplier := 1.0

	tier := NormalizeBillingServiceTier(serviceTier)
	if tier == "priority" || tier == "fast" {
		if fastMultiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
			// 渠道显式倍率以普通模式最终价为基准，避免和模型内置 priority 价重复叠乘。
			tierMultiplier = fastMultiplier
		} else if UsePriorityServiceTierPricing(serviceTier, pricing) {
			if pricing.InputPricePerTokenPriority > 0 {
				inputPrice = pricing.InputPricePerTokenPriority
			}
			if pricing.OutputPricePerTokenPriority > 0 {
				outputPrice = pricing.OutputPricePerTokenPriority
			}
			if pricing.CacheReadPricePerTokenPriority > 0 {
				cacheReadPrice = pricing.CacheReadPricePerTokenPriority
			}
			if pricing.CacheCreationPricePerTokenPriority > 0 {
				cacheCreationPrice = pricing.CacheCreationPricePerTokenPriority
			}
		} else {
			tierMultiplier = ServiceTierCostMultiplier(serviceTier)
		}
	} else {
		tierMultiplier = ConfiguredServiceTierMultiplier(serviceTier, pricing)
	}

	longContextPricingEligible := applyLongCtx && ShouldApplySessionLongContextPricing(tokens, pricing)
	var baselineCost *CostBreakdown
	if longContextPricingEligible {
		baselineCost = ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, false)
		// 未配置的一侧倍率按 1 计，避免部分覆盖条目把对应分项算成免费。
		longContextInputMultiplier := LongContextMultiplierOrOne(pricing.LongContextInputMultiplier)
		inputPrice *= longContextInputMultiplier
		outputPrice *= LongContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
		// 缓存读取本质上是输入侧的复用，应与 input 一同应用长上下文倍率；
		// 否则 cache hit 越多，少计的费用越多（见 #2293）。
		cacheReadPrice *= longContextInputMultiplier
		// 缓存创建（cache_write）也是输入侧操作，三档价格（标准 / 5m / 1h）
		// 都通过 ComputeCacheCreationCost 直接读取 pricing.*，不会经过这里
		// 的倍率修改，因此显式向下传一个倍率，避免长上下文场景下被漏乘。
		cacheCreationMultiplier = longContextInputMultiplier
	}

	bd := &CostBreakdown{}
	// 分离图片输入 token 与文本输入 token（多模态 embedding、图片编辑等图文不同价场景）。
	// InputCost 仅计文本输入，图片输入费用单独记入 ImageInputCost，便于对账；总额不变。
	// ImageInputTokens 为 0 时（绝大多数 chat/vision 流量）走原始单价路径，行为不变。
	if tokens.ImageInputTokens > 0 {
		imageInputTokens := tokens.ImageInputTokens
		textInputTokens := tokens.InputTokens - imageInputTokens
		if textInputTokens < 0 {
			textInputTokens = 0
			imageInputTokens = tokens.InputTokens
		}
		imageInputPrice := pricing.ImageInputPricePerToken
		if imageInputPrice == 0 {
			imageInputPrice = inputPrice
		}
		bd.InputCost = float64(textInputTokens) * inputPrice
		bd.ImageInputCost = float64(imageInputTokens) * imageInputPrice
	} else {
		bd.InputCost = float64(tokens.InputTokens) * inputPrice
	}

	// 分离图片输出 token 与文本输出 token
	textOutputTokens := tokens.OutputTokens - tokens.ImageOutputTokens
	if textOutputTokens < 0 {
		textOutputTokens = 0
	}
	bd.OutputCost = float64(textOutputTokens) * outputPrice

	// 图片输出 token 费用（独立费率）
	if tokens.ImageOutputTokens > 0 {
		imgPrice := pricing.ImageOutputPricePerToken
		if imgPrice == 0 && !pricing.ImageOutputPriceExplicit {
			imgPrice = outputPrice
		}
		bd.ImageOutputCost = float64(tokens.ImageOutputTokens) * imgPrice
	}

	// 缓存创建费用
	bd.CacheCreationCost = ComputeCacheCreationCost(pricing, tokens, cacheCreationPrice, cacheCreationMultiplier)

	bd.CacheReadCost = float64(tokens.CacheReadTokens) * cacheReadPrice

	if tierMultiplier != 1.0 {
		bd.InputCost *= tierMultiplier
		bd.ImageInputCost *= tierMultiplier
		bd.OutputCost *= tierMultiplier
		bd.ImageOutputCost *= tierMultiplier
		bd.CacheCreationCost *= tierMultiplier
		bd.CacheReadCost *= tierMultiplier
	}

	bd.TotalCost = bd.InputCost + bd.ImageInputCost + bd.OutputCost + bd.ImageOutputCost +
		bd.CacheCreationCost + bd.CacheReadCost
	bd.ActualCost = bd.TotalCost * rateMultiplier
	bd.LongContextBillingApplied = baselineCost != nil && bd.ActualCost > baselineCost.ActualCost

	return bd
}

// ComputeCacheCreationCost 计算缓存创建费用（支持 5m/1h 分类或标准计费）。
// multiplier 用于长上下文等场景下的整体价格缩放（普通调用传 1.0 即可）。
func ComputeCacheCreationCost(pricing *ModelPricing, tokens UsageTokens, price, multiplier float64) float64 {
	if pricing.SupportsCacheBreakdown && (pricing.CacheCreation5mPrice > 0 || pricing.CacheCreation1hPrice > 0) {
		cacheCreation5mTokens, cacheCreation1hTokens := NormalizeCacheCreationBreakdown(tokens)
		if cacheCreation5mTokens == 0 && cacheCreation1hTokens == 0 && tokens.CacheCreationTokens > 0 {
			// API 未返回 ephemeral 明细，回退到全部按 5m 单价计费
			return float64(tokens.CacheCreationTokens) * pricing.CacheCreation5mPrice * multiplier
		}
		return float64(cacheCreation5mTokens)*pricing.CacheCreation5mPrice*multiplier +
			float64(cacheCreation1hTokens)*pricing.CacheCreation1hPrice*multiplier
	}
	return float64(tokens.CacheCreationTokens) * price * multiplier
}

// NormalizeCacheCreationBreakdown 在聚合值为正且 TTL 明细相互矛盾时封顶明细，
// 并在整数 token 约束下尽量保留上游报告的比例。
func NormalizeCacheCreationBreakdown(tokens UsageTokens) (int, int) {
	cacheCreation5mTokens := tokens.CacheCreation5mTokens
	cacheCreation1hTokens := tokens.CacheCreation1hTokens
	aggregate := tokens.CacheCreationTokens
	if cacheCreation5mTokens < 0 {
		cacheCreation5mTokens = 0
	}
	if cacheCreation1hTokens < 0 {
		cacheCreation1hTokens = 0
	}
	if aggregate <= 0 || (cacheCreation5mTokens <= aggregate && cacheCreation1hTokens <= aggregate-cacheCreation5mTokens) {
		return cacheCreation5mTokens, cacheCreation1hTokens
	}

	detailTotal := float64(cacheCreation5mTokens) + float64(cacheCreation1hTokens)
	normalized5mTokens := math.Round(float64(aggregate) * float64(cacheCreation5mTokens) / detailTotal)
	if normalized5mTokens >= float64(aggregate) {
		cacheCreation5mTokens = aggregate
	} else {
		cacheCreation5mTokens = int(normalized5mTokens)
	}
	return cacheCreation5mTokens, aggregate - cacheCreation5mTokens
}

// CalculatePerRequestCost 按次/图片计费
func CalculatePerRequestCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	units := input.UsageUnits
	if units <= 0 {
		count := input.RequestCount
		if count <= 0 {
			count = 1
		}
		units = float64(count)
	}

	var unitPrice float64
	var priceFound bool

	if input.SizeTier != "" {
		unitPrice, priceFound = GetRequestTierPriceValue(resolved, input.SizeTier)
	}

	if !priceFound {
		totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens
		unitPrice, priceFound = GetRequestTierPriceByContextValue(resolved, totalContext)
	}

	// 回退到默认按次价格
	if !priceFound {
		unitPrice = resolved.DefaultPerRequestPrice
	}

	totalCost := unitPrice * units
	actualCost := totalCost * input.RateMultiplier

	return &CostBreakdown{
		TotalCost:  totalCost,
		ActualCost: actualCost,
	}, nil
}

func ApplyModelSpecificPricingPolicy(model string, pricing *ModelPricing, policy ModelPolicy) *ModelPricing {
	return ApplyModelSpecificPricingPolicyEx(model, pricing, true, policy)
}

// ApplyModelSpecificPricingPolicyEx 应用模型专属定价修正；forceDeepSeekRates 为 false
// 时保留分组/渠道对 DeepSeek 的显式价格，避免官方价覆盖运营者配置。
func ApplyModelSpecificPricingPolicyEx(model string, pricing *ModelPricing, forceDeepSeekRates bool, policy ModelPolicy) *ModelPricing {
	if pricing == nil {
		return nil
	}
	if forceDeepSeekRates && IsDeepSeekModel(model) {
		return ApplyDeepSeekOfficialPricing(model, pricing)
	}
	normalized := policy.NormalizedOpenAIModel
	isGPT56 := policy.IsGPT56
	needsCacheCreationPolicy := isGPT56 && !pricing.CacheCreationPriceExplicit && (pricing.CacheCreationPricePerToken <= 0 ||
		(pricing.InputPricePerTokenPriority > 0 && pricing.CacheCreationPricePerTokenPriority <= 0))
	fastRatio := OpenAIModelFastPricingRatio(normalized)
	if !needsCacheCreationPolicy && fastRatio <= 0 {
		return pricing
	}
	cloned := *pricing
	if isGPT56 && !cloned.CacheCreationPriceExplicit {
		if cloned.CacheCreationPricePerToken <= 0 {
			cloned.CacheCreationPricePerToken = cloned.InputPricePerToken * 1.25
		}
		if cloned.CacheCreationPricePerTokenPriority <= 0 {
			cloned.CacheCreationPricePerTokenPriority = cloned.InputPricePerTokenPriority * 1.25
		}
	}
	if fastRatio > 0 {
		EnforceOpenAIFastPricingRatio(&cloned, fastRatio)
	}
	return &cloned
}

// LongContextMultiplierOrOne 将未配置的长上下文倍率归一为 1。
func LongContextMultiplierOrOne(multiplier float64) float64 {
	if multiplier <= 0 {
		return 1
	}
	return multiplier
}

// OpenAIModelFastPricingRatio 返回业务口径下 OpenAI GPT-5.x 模型 Fast/priority
// 的标准价倍率：gpt-5.6 系列与 gpt-5.4 为 2x，gpt-5.5 为 2.5x。未定义 Fast
// 档的模型（如 gpt-5.5-pro、gpt-5.4-mini/nano）返回 0。
func OpenAIModelFastPricingRatio(normalized string) float64 {
	switch normalized {
	case "gpt-5.4", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		return 2.0
	case "gpt-5.5":
		return 2.5
	default:
		return 0
	}
}

// EnforceOpenAIFastPricingRatio 把 priority 档价格改写为「标准价 × ratio」。
// 本地/远程 LiteLLM 目录可能只带官方旧口径（如 gpt-5.5 priority 仍标 2x），
// 直接采用会导致 Fast 模式少计费；这里按业务倍率兜底修正，且对已正确的
// fallback 条目（2x/2.5x）是幂等的。ComputeTokenBreakdown 在 priority 价格
// 存在时走显式档位价、不再叠加通用 tier 倍率，因此不会重复乘价。
func EnforceOpenAIFastPricingRatio(pricing *ModelPricing, ratio float64) {
	if pricing == nil || ratio <= 0 {
		return
	}
	pricing.InputPricePerTokenPriority = pricing.InputPricePerToken * ratio
	pricing.OutputPricePerTokenPriority = pricing.OutputPricePerToken * ratio
	if pricing.CacheReadPricePerToken > 0 {
		pricing.CacheReadPricePerTokenPriority = pricing.CacheReadPricePerToken * ratio
	}
	// 渠道显式覆盖缓存写价格时，保留其是否配置 priority 的原语义，
	// 不因模型 Fast 兜底倍率凭空生成未配置的档位价。
	if !pricing.CacheCreationPriceExplicit && pricing.CacheCreationPricePerToken > 0 {
		hadNativePriority := pricing.CacheCreationPricePerTokenPriority > 0
		pricing.CacheCreationPricePerTokenPriority = pricing.CacheCreationPricePerToken * ratio
		pricing.CacheCreationPriorityDerived = !hadNativePriority && pricing.CacheCreationPricePerTokenPriority > 0
	}
}

func ShouldApplySessionLongContextPricing(tokens UsageTokens, pricing *ModelPricing) bool {
	if pricing == nil || pricing.LongContextInputThreshold <= 0 {
		return false
	}
	if pricing.LongContextInputMultiplier <= 1 && pricing.LongContextOutputMultiplier <= 1 {
		return false
	}
	totalInputTokens := tokens.InputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
	if pricing.LongContextThresholdInclusive {
		return totalInputTokens >= pricing.LongContextInputThreshold
	}
	return totalInputTokens > pricing.LongContextInputThreshold
}

// DisplayPricingFromResolved 将已解析的计费配置转换成模型广场展示价格。
func DisplayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	if resolved == nil {
		return ModelDisplayPricing{}, false
	}

	switch resolved.Mode {
	case BillingModeToken:
		pricing := ResolvedDisplayTokenPricing(resolved)
		if pricing != nil && !resolved.LongContextPricingEnabled {
			pricing = WithoutLongContextDisplayPricing(pricing)
		}
		if pricing != nil && (HasAnyDisplayTokenPricing(pricing) || resolved.HasEffectiveOverridePricing()) {
			return BuildTokenDisplayPricing(pricing, rateMultiplier), true
		}
		intervals := ResolvedDisplayPricingIntervals(resolved, rateMultiplier)
		if len(intervals) > 0 {
			return BuildTokenIntervalDisplayPricing(intervals), true
		}
		return ModelDisplayPricing{}, false
	case BillingModeImage, BillingModePerRequest:
		if resolved.Source != PricingSourceGroup && resolved.Source != PricingSourceChannel {
			return ModelDisplayPricing{}, false
		}
		if resolved.Mode == BillingModePerRequest && !LooksLikeImageModel(model) {
			return ModelDisplayPricing{}, false
		}
		price1K, price2K, price4K := ResolvedImageTierPrices(resolved)
		if price1K <= 0 && price2K <= 0 && price4K <= 0 && !resolved.HasEffectiveOverridePricing() {
			return ModelDisplayPricing{}, false
		}
		return BuildImageDisplayPricing(
			price1K*rateMultiplier,
			price2K*rateMultiplier,
			price4K*rateMultiplier,
		), true
	default:
		return ModelDisplayPricing{}, false
	}
}

// WithoutLongContextDisplayPricing 只移除内置长上下文展示元数据，不改变基础单价。
func WithoutLongContextDisplayPricing(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	cloned := *pricing
	cloned.LongContextInputThreshold = 0
	cloned.LongContextInputMultiplier = 0
	cloned.LongContextOutputMultiplier = 0
	return &cloned
}

// ResolvedTokenPriceRanges 补齐显式区间前后与中间的默认价范围，不修改原价卡。
func ResolvedTokenPriceRanges(resolved *ResolvedPricing) []ResolvedTokenPriceRange {
	if resolved == nil || len(resolved.Intervals) == 0 {
		return nil
	}
	intervals := append([]PricingInterval(nil), resolved.Intervals...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].MinTokens < intervals[j].MinTokens })
	ranges := make([]ResolvedTokenPriceRange, 0, len(intervals)*2+1)
	cursor := 0
	for i := range intervals {
		interval := &intervals[i]
		if interval.MinTokens > cursor {
			maxTokens := interval.MinTokens
			ranges = append(ranges, ResolvedTokenPriceRange{cursor, &maxTokens, WithoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
		}
		ranges = append(ranges, ResolvedTokenPriceRange{interval.MinTokens, interval.MaxTokens, resolved.tokenPricingForInterval(interval)})
		if interval.MaxTokens == nil {
			return ranges
		}
		cursor = *interval.MaxTokens
	}
	return append(ranges, ResolvedTokenPriceRange{cursor, nil, WithoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
}

func ResolvedDisplayTokenPricing(resolved *ResolvedPricing) *ModelPricing {
	if resolved == nil {
		return nil
	}
	if len(resolved.Intervals) == 0 {
		return resolved.tokenPricingForInterval(nil)
	}
	ranges := ResolvedTokenPriceRanges(resolved)
	pricing := ranges[0].pricing
	// 缺价范围或不同价格不能压平成一个覆盖所有上下文的单价。
	for _, priceRange := range ranges {
		if priceRange.pricing == nil || !SameDisplayTokenPricing(pricing, priceRange.pricing) {
			return nil
		}
	}
	return pricing
}

// ResolvedDisplayPricingIntervals 展示全部有定价的范围，缺价范围不伪装成免费。
func ResolvedDisplayPricingIntervals(resolved *ResolvedPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	var intervals []ModelDisplayPricingInterval
	for _, priceRange := range ResolvedTokenPriceRanges(resolved) {
		if priceRange.pricing != nil {
			intervals = append(intervals, ModelPricingDisplayInterval(priceRange.minTokens, priceRange.maxTokens, priceRange.pricing, rateMultiplier))
		}
	}
	return intervals
}

func PricingIntervalHasEffectiveTokenPricing(interval PricingInterval) bool {
	return interval.InputPrice != nil ||
		interval.OutputPrice != nil ||
		interval.CacheWritePrice != nil ||
		interval.CacheWrite1hPrice != nil ||
		interval.CacheReadPrice != nil
}

func SameDisplayTokenPricing(a *ModelPricing, b *ModelPricing) bool {
	if a == nil || b == nil {
		return a == b
	}
	// 普通价和 Fast 价都相同才能合并，避免默认段与显式段的服务层级差异被隐藏。
	return ModelPricingDisplayInterval(0, nil, a, 1) == ModelPricingDisplayInterval(0, nil, b, 1)
}

func ResolvedImageTierPrices(resolved *ResolvedPricing) (float64, float64, float64) {
	if resolved == nil {
		return 0, 0, 0
	}

	defaultPrice := resolved.DefaultPerRequestPrice
	price1K := ResolvedRequestTierPrice(resolved.RequestTiers, "1K", defaultPrice)
	price2K := ResolvedRequestTierPrice(resolved.RequestTiers, "2K", defaultPrice)
	price4K := ResolvedRequestTierPrice(resolved.RequestTiers, "4K", defaultPrice)
	return price1K, price2K, price4K
}

func ResolvedRequestTierPrice(tiers []PricingInterval, label string, defaultPrice float64) float64 {
	for _, tier := range tiers {
		if strings.EqualFold(tier.TierLabel, label) && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice
		}
	}
	return defaultPrice
}

func BuildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	if intervals := LongContextDisplayPricingIntervals(pricing, rateMultiplier); len(intervals) > 0 {
		return BuildTokenIntervalDisplayPricing(intervals)
	}

	cacheWritePrice, cacheWrite1hPrice := CacheCreationDisplayPrices(pricing)
	displayPricing := ModelDisplayPricing{
		PricingMode:               "token",
		PriceStatus:               "priced",
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := FastModeDisplayPricing(pricing); ok {
		displayPricing.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		displayPricing.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		displayPricing.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := CacheCreationDisplayPrices(fastPricing)
		displayPricing.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		displayPricing.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		displayPricing.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		displayPricing.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return displayPricing
}

// CacheCreationDisplayPrices 返回展示用的 5m/1h 缓存写入单价。
// 未启用 TTL 明细时只返回兼容旧配置的聚合单价。
func CacheCreationDisplayPrices(pricing *ModelPricing) (float64, float64) {
	if pricing == nil {
		return 0, 0
	}
	short := pricing.CacheCreationPricePerToken
	if pricing.SupportsCacheBreakdown && pricing.CacheCreation5mPrice > 0 {
		short = pricing.CacheCreation5mPrice
	}
	if !pricing.SupportsCacheBreakdown {
		return short, 0
	}
	return short, pricing.CacheCreation1hPrice
}

// LongContextDisplayPricingIntervals 将内置长上下文倍率转换成模型广场可展示的两段价格。
func LongContextDisplayPricingIntervals(pricing *ModelPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	if !HasLongContextDisplayPricing(pricing) {
		return nil
	}

	maxTokens := pricing.LongContextInputThreshold
	baseInterval := ModelPricingDisplayInterval(0, &maxTokens, pricing, rateMultiplier)

	longContextPricing := ApplyLongContextDisplayMultipliers(pricing)
	longContextInterval := ModelPricingDisplayInterval(pricing.LongContextInputThreshold, nil, longContextPricing, rateMultiplier)

	return []ModelDisplayPricingInterval{baseInterval, longContextInterval}
}

// ApplyLongContextDisplayMultipliers 按结算规则生成长上下文展示价格，不修改原始模型定价。
// 缓存创建与读取都属于输入侧，普通价、priority 价及缓存时长明细必须使用同一输入倍率。
func ApplyLongContextDisplayMultipliers(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	adjusted := *pricing
	// 与结算路径保持一致：覆盖文件只声明一侧倍率时，另一侧按 1x 展示，不能显示为免费。
	inputMultiplier := LongContextMultiplierOrOne(pricing.LongContextInputMultiplier)
	outputMultiplier := LongContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
	adjusted.InputPricePerToken *= inputMultiplier
	adjusted.InputPricePerTokenPriority *= inputMultiplier
	adjusted.OutputPricePerToken *= outputMultiplier
	adjusted.OutputPricePerTokenPriority *= outputMultiplier
	adjusted.CacheCreationPricePerToken *= inputMultiplier
	adjusted.CacheCreationPricePerTokenPriority *= inputMultiplier
	adjusted.CacheCreation5mPrice *= inputMultiplier
	adjusted.CacheCreation1hPrice *= inputMultiplier
	adjusted.CacheReadPricePerToken *= inputMultiplier
	adjusted.CacheReadPricePerTokenPriority *= inputMultiplier
	return &adjusted
}

func HasLongContextDisplayPricing(pricing *ModelPricing) bool {
	return HasAnyDisplayTokenPricing(pricing) &&
		pricing.LongContextInputThreshold > 0 &&
		(pricing.LongContextInputMultiplier > 1 || pricing.LongContextOutputMultiplier > 1)
}

func ModelPricingDisplayInterval(minTokens int, maxTokens *int, pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricingInterval {
	cacheWritePrice, cacheWrite1hPrice := CacheCreationDisplayPrices(pricing)
	interval := ModelDisplayPricingInterval{
		MinTokens:                 minTokens,
		MaxTokens:                 maxTokens,
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := FastModeDisplayPricing(pricing); ok {
		interval.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		interval.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		interval.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := CacheCreationDisplayPrices(fastPricing)
		interval.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		interval.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		interval.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		interval.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return interval
}

func FastModeDisplayPricing(pricing *ModelPricing) (*ModelPricing, bool) {
	if !HasFastModeDisplayPricing(pricing) {
		return nil, false
	}
	if multiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
		fastPricing := MultiplyModelPricing(pricing, multiplier)
		fastPricing.FastModeMultiplier = nil
		return fastPricing, true
	}

	fastPricing := *pricing
	if UsePriorityServiceTierPricing(OpenAIFastTierPriority, pricing) {
		if pricing.InputPricePerTokenPriority > 0 {
			fastPricing.InputPricePerToken = pricing.InputPricePerTokenPriority
		}
		if pricing.OutputPricePerTokenPriority > 0 {
			fastPricing.OutputPricePerToken = pricing.OutputPricePerTokenPriority
		}
		if pricing.CacheCreationPricePerTokenPriority > 0 {
			fastPricing.CacheCreationPricePerToken = pricing.CacheCreationPricePerTokenPriority
		}
		if pricing.CacheReadPricePerTokenPriority > 0 {
			fastPricing.CacheReadPricePerToken = pricing.CacheReadPricePerTokenPriority
		}
		return &fastPricing, true
	}

	multiplier := ServiceTierCostMultiplier(OpenAIFastTierPriority)
	fastPricing.InputPricePerToken *= multiplier
	fastPricing.ImageInputPricePerToken *= multiplier
	fastPricing.OutputPricePerToken *= multiplier
	fastPricing.CacheCreationPricePerToken *= multiplier
	fastPricing.CacheReadPricePerToken *= multiplier
	fastPricing.ImageOutputPricePerToken *= multiplier
	return &fastPricing, true
}

// ResolvedHasFastModeDisplayPricing 同时检查默认价与有效区间，覆盖只有区间单价的自定义模型。
func ResolvedHasFastModeDisplayPricing(resolved *ResolvedPricing) bool {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return false
	}
	if HasFastModeDisplayPricing(resolved.tokenPricingForInterval(nil)) {
		return true
	}
	for i := range resolved.Intervals {
		pricing := resolved.tokenPricingForInterval(&resolved.Intervals[i])
		if HasFastModeDisplayPricing(pricing) {
			return true
		}
	}
	return false
}

func HasFastModeDisplayPricing(pricing *ModelPricing) bool {
	return HasAnyDisplayTokenPricing(pricing) &&
		(pricing.FastModeMultiplier != nil ||
			pricing.SupportsServiceTier ||
			pricing.InputPricePerTokenPriority > 0 ||
			pricing.OutputPricePerTokenPriority > 0 ||
			pricing.CacheCreationPricePerTokenPriority > 0 ||
			pricing.CacheReadPricePerTokenPriority > 0)
}

// BuildTokenIntervalDisplayPricing 标记此模型需要按上下文区间展示价格。
func BuildTokenIntervalDisplayPricing(intervals []ModelDisplayPricingInterval) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:      "token",
		PriceStatus:      "priced",
		ContextIntervals: intervals,
	}
}

func BuildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:  "image",
		PriceStatus:  "priced",
		ImagePrice1K: price1K,
		ImagePrice2K: price2K,
		ImagePrice4K: price4K,
	}
}

func UnknownDisplayPricing() ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode: "unknown",
		PriceStatus: "unpriced",
	}
}

const (
	DefaultImageGenerationPrice = 0.134

	DefaultGrokImagineImagePrice1K        = 0.02
	DefaultGrokImagineImagePrice2K        = 0.02
	DefaultGrokImagineImageQualityPrice1K = 0.05
	DefaultGrokImagineImageQualityPrice2K = 0.07
	DefaultGrokImagineImage20Price1K      = 0.06 // default quality is Medium
	DefaultGrokImagineImage20Price2K      = 0.08

	// 视频默认价为 xAI 官方**每秒**输出价格（USD/s），总价 = 每秒价 × 时长（秒）。
	DefaultGrokImagineVideoPrice480P    = 0.05
	DefaultGrokImagineVideoPrice720P    = 0.07
	DefaultGrokImagineVideo15Price480P  = 0.08
	DefaultGrokImagineVideo15Price720P  = 0.14
	DefaultGrokImagineVideo15Price1080P = 0.25

	// Codex alpha/search 网页搜索单次默认价：OpenAI 官方 web search 定价 $10/1000 次。
	DefaultWebSearchPricePerCall = 0.01

	// xAI 服务端网页/X 搜索与代码执行按每千次 $5 计费。
	DefaultSearchPricePer1k = 5.0

	// 通用实时语音默认采用 think-fast-1.0 价格；think-fast-2.0 可通过
	// 分组或渠道逐模型价格独立配置。
	DefaultAudioRealtimePricePerMin     = 0.05
	DefaultAudioTTSPricePerMillionChars = 15.0
	DefaultAudioSTTPricePerHour         = 0.10
)

// CalculateWebSearchCost 计算 Codex alpha/search 网页搜索按次费用。
// callCount: 搜索调用次数（每次请求为 1）
// groupPrice: 分组配置的单次价格（nil 表示使用默认价 0.01；0 表示免费）
// rateMultiplier: 分组费率倍数
func CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	if callCount <= 0 {
		return &CostBreakdown{}
	}
	unitPrice := DefaultWebSearchPricePerCall
	if groupPrice != nil && *groupPrice >= 0 {
		unitPrice = *groupPrice
	}
	totalCost := unitPrice * float64(callCount)

	// 应用倍率（保存时强制 > 0；负数按 0 处理避免按 1x 误扣）
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  totalCost * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateSearchCost 按每千次调用结算搜索工具；nil 使用默认价，显式 0 表示免费。
func CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	if numCalls <= 0 {
		return &CostBreakdown{}
	}
	pricePer1k := DefaultSearchPricePer1k
	if groupPricePer1k != nil {
		if *groupPricePer1k < 0 {
			return &CostBreakdown{}
		}
		pricePer1k = *groupPricePer1k
	}
	if pricePer1k == 0 {
		return &CostBreakdown{}
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	unit := pricePer1k / 1000.0
	total := unit * float64(numCalls)
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateAudioCost 分别按分钟、百万字符和小时结算 Realtime、TTS 与 STT；
// 分组价格缺失时使用默认值，显式 0 表示对应模式免费。
func CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *AudioPriceConfig, rateMultiplier float64) *CostBreakdown {
	if durationOrUnits <= 0 {
		return &CostBreakdown{}
	}
	var unitPrice float64
	switch strings.ToLower(mode) {
	case "realtime":
		unitPrice = DefaultAudioRealtimePricePerMin
		if groupConfig != nil && groupConfig.RealtimePerMin != nil {
			unitPrice = *groupConfig.RealtimePerMin
		}
	case "tts":
		unitPrice = DefaultAudioTTSPricePerMillionChars
		if groupConfig != nil && groupConfig.TTSPerMChars != nil {
			unitPrice = *groupConfig.TTSPerMChars
		}
	case "stt":
		unitPrice = DefaultAudioSTTPricePerHour
		if groupConfig != nil && groupConfig.STTPerHour != nil {
			unitPrice = *groupConfig.STTPerHour
		}
	default:
		return &CostBreakdown{}
	}
	if unitPrice <= 0 {
		return &CostBreakdown{}
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	total := unitPrice * durationOrUnits
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// HasExplicitImageGenerationPricing 仅把明确标记为图片生成的按图价格视为图片计费。
// 部分聊天模型也携带 output_cost_per_image 元数据，不能据此覆盖其 token 定价。
func HasExplicitImageGenerationPricing(pricing *LiteLLMModelPricing) bool {
	return pricing != nil &&
		pricing.OutputCostPerImage > 0 &&
		strings.EqualFold(strings.TrimSpace(pricing.Mode), "image_generation")
}

func HasAnyDisplayTokenPricing(pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	return pricing.InputPricePerToken > 0 ||
		pricing.ImageInputPricePerToken > 0 ||
		pricing.OutputPricePerToken > 0 ||
		pricing.CacheCreationPricePerToken > 0 ||
		pricing.CacheCreation1hPrice > 0 ||
		pricing.CacheReadPricePerToken > 0 ||
		pricing.ImageOutputPricePerToken > 0
}

func LooksLikeImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}

	return strings.Contains(model, "-image") ||
		strings.Contains(model, "image-") ||
		strings.Contains(model, "/image") ||
		strings.HasPrefix(model, "imagen-") ||
		strings.Contains(model, "gpt-image") ||
		strings.Contains(model, "dall-e")
}
