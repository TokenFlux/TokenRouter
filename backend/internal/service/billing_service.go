// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	strings "strings"
	time "time"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type APIKeyRateLimitCacheData = billing.APIKeyRateLimitCacheData

type UserPlatformQuotaKey = billing.UserPlatformQuotaKey

const UserPlatformQuotaCacheSchemaV1 = billing.UserPlatformQuotaCacheSchemaV1

type UserPlatformQuotaCacheEntry = billing.UserPlatformQuotaCacheEntry

type BillingCache = billing.BillingCache

// ModelPricing 保留旧用量/定价类型入口。
type ModelPricing = purepricing.ModelPricing

// normalizeBillingServiceTier 委托纯定价实现，旧查询与配置投影保留在适配层。
func normalizeBillingServiceTier(serviceTier string) string {
	return purepricing.NormalizeBillingServiceTier(serviceTier)
}

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

// GetModelPricing 委托唯一 billing 计费实例。
func (s *BillingService) GetModelPricing(model string) (*ModelPricing, error) {
	return s.Calculator.GetModelPricing(model)
}

// GetModelPricingWithChannel 委托唯一 billing 计费实例。
func (s *BillingService) GetModelPricingWithChannel(model string, channelPricing *ChannelModelPricing) (*ModelPricing, error) {
	return s.Calculator.GetModelPricingWithChannel(model, channelPricing)
}

// CostInput 统一计费输入
type CostInput struct {
	Ctx             context.Context
	Model           string
	GroupID         *int64 // 用于渠道定价查找
	Group           *Group
	Tokens          UsageTokens
	RequestCount    int     // 按次计费时使用
	UsageUnits      float64 // 音频等连续计量单位（分钟/小时/百万字符）
	SizeTier        string  // 按次/图片模式的层级标签（"1K","2K","4K","HD" 等）
	RateMultiplier  float64
	PricingAt       time.Time             // 渠道分时定价使用的计费时刻
	ServiceTier     string                // "priority","flex","" 等
	ReasoningEffort string                // 最终转发的推理档位；max 可触发模型/渠道倍率
	Resolver        *ModelPricingResolver // 定价解析器
	Resolved        *ResolvedPricing      // 可选：预解析的定价结果（避免重复 Resolve 调用）
}

// CalculateCostUnified 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	return s.Calculator.CalculateCostUnified(projectBillingCost(input))
}

// computeTokenBreakdown 委托唯一 billing 计费实例。
func (s *BillingService) computeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	return s.ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, applyLongCtx)
}

// CalculateCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCost(model string, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, error) {
	return s.Calculator.CalculateCost(model, tokens, rateMultiplier)
}

// CalculateCostWithServiceTier 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCostWithServiceTier(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.Calculator.CalculateCostWithServiceTier(model, tokens, rateMultiplier, serviceTier)
}

// applyModelSpecificPricingPolicy 委托唯一 billing 计费实例。
func (s *BillingService) applyModelSpecificPricingPolicy(model string, pricing *ModelPricing) *ModelPricing {
	return s.ApplyModelSpecificPricingPolicy(model, pricing)
}

// CalculateCostWithConfig 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCostWithConfig(model string, tokens UsageTokens) (*CostBreakdown, error) {
	return s.Calculator.CalculateCostWithConfig(model, tokens)
}

// CalculateCostWithLongContext 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCostWithLongContext(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64) (*CostBreakdown, error) {
	return s.Calculator.CalculateCostWithLongContext(model, tokens, rateMultiplier, threshold, extraMultiplier)
}

// CalculateCostWithLongContextAndServiceTier 委托唯一 billing 计费实例。
func (s *BillingService) CalculateCostWithLongContextAndServiceTier(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.Calculator.CalculateCostWithLongContextAndServiceTier(model, tokens, rateMultiplier, threshold, extraMultiplier, serviceTier)
}

// ListSupportedModels 委托唯一 billing 计费实例。
func (s *BillingService) ListSupportedModels() []string { return s.Calculator.ListSupportedModels() }

// IsModelSupported 委托唯一 billing 计费实例。
func (s *BillingService) IsModelSupported(model string) bool {
	return s.Calculator.IsModelSupported(model)
}

// GetEstimatedCost 委托唯一 billing 计费实例。
func (s *BillingService) GetEstimatedCost(model string, estimatedInputTokens, estimatedOutputTokens int) (float64, error) {
	return s.Calculator.GetEstimatedCost(model, estimatedInputTokens, estimatedOutputTokens)
}

// GetPricingServiceStatus 委托唯一 billing 计费实例。
func (s *BillingService) GetPricingServiceStatus() map[string]any {
	return s.Calculator.GetPricingServiceStatus()
}

// ForceUpdatePricing 委托唯一 billing 计费实例。
func (s *BillingService) ForceUpdatePricing() error { return s.Calculator.ForceUpdatePricing() }

// ModelDisplayPricing 保留旧用量/定价类型入口。
type ModelDisplayPricing = purepricing.ModelDisplayPricing

// ModelDisplayPricingInterval 保留旧用量/定价类型入口。
type ModelDisplayPricingInterval = purepricing.ModelDisplayPricingInterval

// GetDisplayPricing 委托唯一 billing 计费实例。
func (s *BillingService) GetDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	return s.Calculator.GetDisplayPricing(model, rateMultiplier)
}

// buildTokenDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func buildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	return purepricing.BuildTokenDisplayPricing(pricing, rateMultiplier)
}

// CalculateWebSearchCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	return s.Calculator.CalculateWebSearchCost(callCount, groupPrice, rateMultiplier)
}

// CalculateSearchCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	return s.Calculator.CalculateSearchCost(numCalls, groupPricePer1k, rateMultiplier)
}

// audioPriceConfig 保留旧用量/定价类型入口。
type audioPriceConfig = purepricing.AudioPriceConfig

// CalculateAudioCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *audioPriceConfig, rateMultiplier float64) *CostBreakdown {
	return s.Calculator.CalculateAudioCost(mode, durationOrUnits, groupConfig, rateMultiplier)
}

// CalculateImageCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateImageCost(model, imageSize string, imageCount int, rateMultiplier float64) *CostBreakdown {
	return s.Calculator.CalculateImageCost(model, imageSize, imageCount, rateMultiplier)
}

// CalculateVideoCost 委托唯一 billing 计费实例。
func (s *BillingService) CalculateVideoCost(model, resolution string, videoCount, durationSeconds int, rateMultiplier float64) *CostBreakdown {
	return s.Calculator.CalculateVideoCost(model, resolution, videoCount, durationSeconds, rateMultiplier)
}

// getDefaultImagePrice 委托唯一 billing 计费实例。
func (s *BillingService) getDefaultImagePrice(model, imageSize string) float64 {
	return s.DefaultImagePrice(model, imageSize)
}

// pricingModelPolicy 保持旧模型识别规则，纯定价只消费明确的模型策略投影。
func pricingModelPolicy(model string) purepricing.ModelPolicy {
	normalized := normalizeKnownOpenAICodexModel(model)
	return purepricing.ModelPolicy{
		NativeGrokModel:       strings.ToLower(strings.TrimSpace(xai.StripGrokProviderPrefix(model))),
		NormalizedOpenAIModel: normalized,
		IsGPT56:               isOpenAIGPT56Model(normalized),
	}
}

// BillingService 保留旧签名和输入投影，算法与运行状态由 Calculator 持有。
type BillingService struct {
	*billing.Calculator
	// 与构造时传给 Calculator 的同一份价卡表，禁止建立第二份缓存。
	fallbackPrices map[string]*ModelPricing
}

func NewBillingService(cfg *config.Config, catalog *PricingService) *BillingService {
	return newBillingServiceWithPrices(cfg, catalog, purepricing.DefaultFallbackPrices())
}
func newBillingServiceWithPrices(cfg *config.Config, catalog *PricingService, prices map[string]*ModelPricing) *BillingService {
	var source billing.PriceCatalog
	if catalog != nil {
		source = catalog
	}
	multiplier := 0.0
	if cfg != nil {
		multiplier = cfg.Default.RateMultiplier
	}
	warnings := &provider.PricingWarnings{}
	core := billing.NewCalculator(source, billing.CalculatorOptions{DefaultRateMultiplier: multiplier, FallbackPrices: prices, ModelPolicy: pricingModelPolicy, Now: timezone.Now, LoadLocation: loadChannelTimePricingLocation, FallbackWarning: warnings.Fallback})
	return &BillingService{Calculator: core, fallbackPrices: prices}
}
func WrapBillingCalculator(core *billing.Calculator) *BillingService {
	return &BillingService{Calculator: core}
}

// PricingModelPolicy 仅导出旧平台身份投影，供 app 桥接注入，S09 退出。
func PricingModelPolicy(model string) purepricing.ModelPolicy { return pricingModelPolicy(model) }
func projectBillingCost(in CostInput) billing.CostInput {
	var resolver *billing.PriceResolver
	if in.Resolver != nil {
		resolver = in.Resolver.PriceResolver
	}
	return billing.CostInput{Ctx: in.Ctx, Model: in.Model, GroupID: in.GroupID, Group: projectPriceGroup(in.Group), Tokens: in.Tokens, RequestCount: in.RequestCount, UsageUnits: in.UsageUnits, SizeTier: in.SizeTier, RateMultiplier: in.RateMultiplier, PricingAt: in.PricingAt, ServiceTier: in.ServiceTier, ReasoningEffort: in.ReasoningEffort, Resolver: resolver, Resolved: in.Resolved}
}
