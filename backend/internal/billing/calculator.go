package billing

import (
	context "context"
	fmt "fmt"
	strings "strings"
	time "time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// ModelPricing 保留旧用量/定价类型入口。
type ModelPricing = purepricing.ModelPricing

// UsageTokens 保留旧用量/定价类型入口。
type UsageTokens = purepricing.UsageTokens

// CostBreakdown 保留旧用量/定价类型入口。
type CostBreakdown = purepricing.CostBreakdown

// applyCostBreakdownMultiplier 委托纯定价实现，旧查询与配置投影保留在适配层。
func applyCostBreakdownMultiplier(cost *CostBreakdown, multiplier float64) {
	purepricing.ApplyCostBreakdownMultiplier(cost, multiplier)
}

// maxReasoningEffortBillingMultiplier 委托纯定价实现，旧查询与配置投影保留在适配层。
func maxReasoningEffortBillingMultiplier(model, effort string, pricing *ModelPricing) float64 {
	return purepricing.MaxReasoningEffortBillingMultiplier(model, effort, pricing)
}

var ErrModelPricingUnavailable = purepricing.ErrModelPricingUnavailable

// GetModelPricing 获取模型价格配置
func (s *Calculator) GetModelPricing(model string) (*ModelPricing, error) {
	model = strings.ToLower(model)
	var raw *LiteLLMModelPricing
	if s.catalog != nil {
		raw = s.catalog.GetModelPricing(model)
	}
	price, fallback, err := purepricing.ResolveModelPricing(model, raw, s.fallbackPrices, s.options.ModelPolicy(model))
	if fallback {
		s.options.FallbackWarning(model)
	}
	return price, err
}

// GetModelPricingWithChannel 保留目录查价失败语义，覆盖计算由纯包完成。
func (s *Calculator) GetModelPricingWithChannel(model string, channelPricing *ChannelModelPricing) (*ModelPricing, error) {
	pricing, err := s.GetModelPricing(model)
	if err != nil {
		return nil, err
	}
	return purepricing.ApplyChannelPrice(pricing, channelPricing), nil
}

// CostInput 统一计费输入
type CostInput struct {
	Ctx             context.Context
	Model           string
	GroupID         *int64 // 用于渠道定价查找
	Group           *PriceGroup
	Tokens          UsageTokens
	RequestCount    int     // 按次计费时使用
	UsageUnits      float64 // 音频等连续计量单位（分钟/小时/百万字符）
	SizeTier        string  // 按次/图片模式的层级标签（"1K","2K","4K","HD" 等）
	RateMultiplier  float64
	PricingAt       time.Time        // 渠道分时定价使用的计费时刻
	ServiceTier     string           // "priority","flex","" 等
	ReasoningEffort string           // 最终转发的推理档位；max 可触发模型/渠道倍率
	Resolver        *PriceResolver   // 定价解析器
	Resolved        *ResolvedPricing // 可选：预解析的定价结果（避免重复 Resolve 调用）
}

// CalculateCostUnified 统一计费入口，支持三种计费模式。
// 使用 PriceResolver 解析定价，然后根据 BillingMode 分发计算。
func (s *Calculator) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	if input.Resolver == nil {
		// 无 Resolver，回退到旧路径
		breakdown, err := s.CalculateCostInternal(input.Model, input.Tokens, input.RateMultiplier, input.ServiceTier, nil)
		if err == nil {
			applyCostBreakdownMultiplier(breakdown, maxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, nil))
		}
		return breakdown, err
	}

	// 优先使用预解析结果，避免重复 Resolve 调用
	resolved := input.Resolved
	if resolved == nil {
		resolved = input.Resolver.Resolve(input.Ctx, PricingInput{
			Model:   input.Model,
			GroupID: input.GroupID,
			Group:   input.Group,
		})
	}

	return purepricing.CalculateCost(resolved, s.ProjectCostInput(input, resolved))
}

// ComputeTokenBreakdown 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *Calculator) ComputeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	return purepricing.ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, applyLongCtx)
}

// CalculateCost 计算使用费用
func (s *Calculator) CalculateCost(model string, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, error) {
	return s.CalculateCostInternal(model, tokens, rateMultiplier, "", nil)
}

func (s *Calculator) CalculateCostWithServiceTier(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.CalculateCostInternal(model, tokens, rateMultiplier, serviceTier, nil)
}

func (s *Calculator) CalculateCostInternal(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, ChannelPricing *ChannelModelPricing) (*CostBreakdown, error) {
	var pricing *ModelPricing
	var err error
	if ChannelPricing != nil {
		pricing, err = s.GetModelPricingWithChannel(model, ChannelPricing)
	} else {
		pricing, err = s.GetModelPricing(model)
	}
	if err != nil {
		return nil, err
	}
	if ChannelPricing == nil {
		pricing = purepricing.ApplyDeepSeekPeakPricing(model, pricing, s.options.Now())
	}

	return s.ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, true), nil
}

// ApplyModelSpecificPricingPolicy 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *Calculator) ApplyModelSpecificPricingPolicy(model string, pricing *ModelPricing) *ModelPricing {
	return purepricing.ApplyModelSpecificPricingPolicy(model, pricing, s.options.ModelPolicy(model))
}

// CalculateCostWithConfig 使用配置中的默认倍率计算费用
func (s *Calculator) CalculateCostWithConfig(model string, tokens UsageTokens) (*CostBreakdown, error) {
	multiplier := s.options.DefaultRateMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	return s.CalculateCost(model, tokens, multiplier)
}

// CalculateCostWithLongContext 计算费用，支持长上下文双倍计费
// threshold: 阈值（如 200000），超过此值的部分按 extraMultiplier 倍计费
// extraMultiplier: 超出部分的倍率（如 2.0 表示双倍）
//
// 示例：缓存 210k + 输入 10k = 220k，阈值 200k，倍率 2.0
// 拆分为：范围内 (200k, 0) + 范围外 (10k, 10k)
// 范围内正常计费，范围外 × 2 计费
func (s *Calculator) CalculateCostWithLongContext(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64) (*CostBreakdown, error) {
	return s.CalculateCostWithLongContextAndServiceTier(model, tokens, rateMultiplier, threshold, extraMultiplier, "")
}

// CalculateCostWithLongContextAndServiceTier 保留两段查询及部分失败返回，分段/金额合并委托纯包。
func (s *Calculator) CalculateCostWithLongContextAndServiceTier(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	charges := purepricing.LegacyLongContextCharges(tokens, rateMultiplier, threshold, extraMultiplier)
	first, err := s.CalculateCostWithServiceTier(model, charges[0].Tokens, charges[0].RateMultiplier, serviceTier)
	if err != nil {
		return nil, err
	}
	if len(charges) == 1 {
		return first, nil
	}
	second, err := s.CalculateCostWithServiceTier(model, charges[1].Tokens, charges[1].RateMultiplier, serviceTier)
	if err != nil {
		return first, fmt.Errorf("out-range cost: %w", err)
	}
	return purepricing.CombineLongContextCosts(first, second), nil
}

// ListSupportedModels 列出所有支持的模型（现在总是返回true，因为有模糊匹配）
func (s *Calculator) ListSupportedModels() []string {
	models := make([]string, 0)
	// 返回回退价格支持的模型系列
	for model := range s.fallbackPrices {
		models = append(models, model)
	}
	return models
}

// IsModelSupported 检查模型是否支持（现在总是返回true，因为有模糊匹配回退）
func (s *Calculator) IsModelSupported(model string) bool {
	// 所有Claude模型都有回退价格支持
	modelLower := strings.ToLower(model)
	return strings.Contains(modelLower, "claude") ||
		strings.Contains(modelLower, "opus") ||
		strings.Contains(modelLower, "sonnet") ||
		strings.Contains(modelLower, "haiku")
}

// GetEstimatedCost 估算费用（用于前端展示）
func (s *Calculator) GetEstimatedCost(model string, estimatedInputTokens, estimatedOutputTokens int) (float64, error) {
	tokens := UsageTokens{
		InputTokens:  estimatedInputTokens,
		OutputTokens: estimatedOutputTokens,
	}

	breakdown, err := s.CalculateCostWithConfig(model, tokens)
	if err != nil {
		return 0, err
	}

	return breakdown.ActualCost, nil
}

// GetPricingServiceStatus 获取价格服务状态
func (s *Calculator) GetPricingServiceStatus() map[string]any {
	if s.catalog != nil {
		return s.catalog.GetStatus()
	}
	return map[string]any{
		"model_count":  len(s.fallbackPrices),
		"last_updated": "using fallback",
		"local_hash":   "N/A",
	}
}

// ForceUpdatePricing 强制更新价格数据
func (s *Calculator) ForceUpdatePricing() error {
	if s.catalog != nil {
		return s.catalog.ForceUpdate()
	}
	return fmt.Errorf("pricing service not initialized")
}

// ModelDisplayPricing 保留旧用量/定价类型入口。
type ModelDisplayPricing = purepricing.ModelDisplayPricing

// ModelDisplayPricingInterval 保留旧用量/定价类型入口。
type ModelDisplayPricingInterval = purepricing.ModelDisplayPricingInterval

// GetDisplayPricing 返回用于模型广场展示的价格信息。
// 它会优先识别图片模型并展示按图计费，否则展示按 token 计费。
func (s *Calculator) GetDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	return s.DisplayPricing(model, rateMultiplier)
}

// DisplayPricing 使用分组倍率计算模型广场展示价格。
func (s *Calculator) DisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	rawPricing := s.RawModelPricing(model)
	if hasExplicitImageGenerationPricing(rawPricing) || looksLikeImageModel(model) {
		return buildImageDisplayPricing(
			s.DefaultImagePrice(model, "1K")*rateMultiplier,
			s.DefaultImagePrice(model, "2K")*rateMultiplier,
			s.DefaultImagePrice(model, "4K")*rateMultiplier,
		)
	}

	pricing, err := s.GetModelPricing(model)
	if err != nil || pricing == nil || !hasAnyDisplayTokenPricing(pricing) {
		return unknownDisplayPricing()
	}

	return buildTokenDisplayPricing(pricing, rateMultiplier)
}

// DisplayPricingWithResolvedMultipliers 优先使用已解析的渠道价格计算展示价格。
func (s *Calculator) DisplayPricingWithResolvedMultipliers(model string, rateMultiplier float64, resolved *ResolvedPricing) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	if resolved.IsUnpriced() {
		return unknownDisplayPricing()
	}
	if pricing, ok := displayPricingFromResolved(model, rateMultiplier, resolved); ok {
		return pricing
	}
	return s.DisplayPricing(model, rateMultiplier)
}

// displayPricingFromResolved 委托纯定价实现，旧查询与配置投影保留在适配层。
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	return purepricing.DisplayPricingFromResolved(model, rateMultiplier, resolved)
}

// buildTokenDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func buildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	return purepricing.BuildTokenDisplayPricing(pricing, rateMultiplier)
}

// buildImageDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func buildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return purepricing.BuildImageDisplayPricing(price1K, price2K, price4K)
}

// unknownDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func unknownDisplayPricing() ModelDisplayPricing { return purepricing.UnknownDisplayPricing() }

// CalculateWebSearchCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *Calculator) CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateWebSearchCost(callCount, groupPrice, rateMultiplier)
}

// CalculateSearchCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *Calculator) CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateSearchCost(numCalls, groupPricePer1k, rateMultiplier)
}

// audioPriceConfig 保留旧用量/定价类型入口。
type audioPriceConfig = purepricing.AudioPriceConfig

// CalculateAudioCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *Calculator) CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *audioPriceConfig, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateAudioCost(mode, durationOrUnits, groupConfig, rateMultiplier)
}

// CalculateImageCost 在确定需要计费后才读取单价，金额计算由纯包完成。
func (s *Calculator) CalculateImageCost(model, imageSize string, imageCount int, rateMultiplier float64) *CostBreakdown {
	if imageCount <= 0 {
		return purepricing.CalculateImageCost(0, imageCount, rateMultiplier)
	}
	imageSize = purepricing.NormalizeImageBillingTierOrDefault(imageSize)
	return purepricing.CalculateImageCost(s.DefaultImagePrice(model, imageSize), imageCount, rateMultiplier)
}

// CalculateVideoCost 保留原有按需查价，显式传递每秒单价和用量。
func (s *Calculator) CalculateVideoCost(model, resolution string, videoCount, durationSeconds int, rateMultiplier float64) *CostBreakdown {
	if videoCount <= 0 {
		return purepricing.CalculateVideoCost(0, videoCount, durationSeconds, rateMultiplier)
	}
	resolution = purepricing.NormalizeVideoBillingResolutionOrDefault(resolution)
	durationSeconds = purepricing.NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	return purepricing.CalculateVideoCost(s.DefaultVideoPrice(model, resolution), videoCount, durationSeconds, rateMultiplier)
}

// DefaultImagePrice 先执行已有平台价卡回退，再按需读取目录。
func (s *Calculator) DefaultImagePrice(model, imageSize string) float64 {
	if price, ok := purepricing.GetDefaultGrokImagineImagePrice(model, imageSize); ok {
		return price
	}
	var raw *LiteLLMModelPricing
	if s.catalog != nil {
		raw = s.catalog.GetModelPricing(model)
	}
	return purepricing.DefaultImagePrice(raw, imageSize)
}

func (s *Calculator) RawModelPricing(model string) *LiteLLMModelPricing {
	if s == nil || s.catalog == nil {
		return nil
	}
	return s.catalog.GetModelPricing(model)
}

// hasExplicitImageGenerationPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func hasExplicitImageGenerationPricing(pricing *LiteLLMModelPricing) bool {
	return purepricing.HasExplicitImageGenerationPricing(pricing)
}

// hasAnyDisplayTokenPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func hasAnyDisplayTokenPricing(pricing *ModelPricing) bool {
	return purepricing.HasAnyDisplayTokenPricing(pricing)
}

// looksLikeImageModel 委托纯定价实现，旧查询与配置投影保留在适配层。
func looksLikeImageModel(model string) bool { return purepricing.LooksLikeImageModel(model) }

func (s *Calculator) DefaultVideoPrice(model string, resolution string) float64 {
	if price, ok := getDefaultGrokImagineVideoPrice(model, resolution); ok {
		return price
	}

	// 内置 LiteLLM 数据没有视频输出价格，暂用历史模型默认价作为每秒单价兜底；
	// 分组视频价格仍可独立覆盖图片价格。
	return s.DefaultImagePrice(model, purepricing.ImageBillingSize2K)
}

// getDefaultGrokImagineVideoPrice 委托唯一媒体定价规则。
func getDefaultGrokImagineVideoPrice(model string, resolution string) (float64, bool) {
	return purepricing.GetDefaultGrokImagineVideoPrice(model, resolution)
}

// PriceCatalog 暴露目录读取及既有维护操作，不向核心暴露文件或网络客户端。
type PriceCatalog interface {
	GetModelPricing(string) *purepricing.LiteLLMModelPricing
	GetStatus() map[string]any
	ForceUpdate() error
}

// CalculatorOptions 由 app 投影默认倍率、取时点和平台模型身份。
type CalculatorOptions struct {
	DefaultRateMultiplier float64
	FallbackPrices        map[string]*ModelPricing
	ModelPolicy           func(string) purepricing.ModelPolicy
	Now                   func() time.Time
	LoadLocation          func(string) (*time.Location, error)
	FallbackWarning       func(string)
}

// Calculator 统一拥有查价及计费编排；价卡算法由 pricing 唯一实现。
type Calculator struct {
	catalog        PriceCatalog
	options        CalculatorOptions
	fallbackPrices map[string]*ModelPricing
}

func NewCalculator(catalog PriceCatalog, options CalculatorOptions) *Calculator {
	if options.FallbackPrices == nil {
		options.FallbackPrices = purepricing.DefaultFallbackPrices()
	}
	if options.ModelPolicy == nil {
		options.ModelPolicy = func(string) purepricing.ModelPolicy { return purepricing.ModelPolicy{} }
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LoadLocation == nil {
		options.LoadLocation = time.LoadLocation
	}
	if options.FallbackWarning == nil {
		options.FallbackWarning = func(string) {}
	}
	return &Calculator{catalog: catalog, options: options, fallbackPrices: options.FallbackPrices}
}

// ProjectCostInput 只投影已解析结果，保持查价懒加载与请求固定的取时点。
func (s *Calculator) ProjectCostInput(input CostInput, resolved *ResolvedPricing) purepricing.CostInput {
	var location *time.Location
	if resolved != nil && resolved.ChannelPricing != nil && resolved.ChannelPricing.TimePricing != nil {
		location, _ = s.options.LoadLocation(resolved.ChannelPricing.TimePricing.Timezone)
	}
	modelAt := input.PricingAt
	if modelAt.IsZero() {
		modelAt = s.options.Now()
	}
	return purepricing.CostInput{Model: input.Model, Tokens: input.Tokens, RequestCount: input.RequestCount, UsageUnits: input.UsageUnits, SizeTier: input.SizeTier, RateMultiplier: input.RateMultiplier, PricingAt: input.PricingAt, ModelPricingAt: modelAt, ModelPolicy: s.options.ModelPolicy(input.Model), ServiceTier: input.ServiceTier, ReasoningEffort: input.ReasoningEffort, TimePricingLocation: location}
}

type LiteLLMModelPricing = purepricing.LiteLLMModelPricing
type ChannelModelPricing = purepricing.ChannelModelPricing

// GetModelModalities 直接走目录的身份元数据查询，不能继承价格回退。
func (s *Calculator) GetModelModalities(model string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	if source, ok := s.catalog.(interface {
		GetModelModalities(string) ([]string, []string)
	}); ok {
		return source.GetModelModalities(model)
	}
	return nil, nil
}
