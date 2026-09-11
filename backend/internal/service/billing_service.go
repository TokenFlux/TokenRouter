package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

// APIKeyRateLimitCacheData holds rate limit usage data cached in Redis.
type APIKeyRateLimitCacheData struct {
	Usage5h  float64 `json:"usage_5h"`
	Usage1d  float64 `json:"usage_1d"`
	Usage7d  float64 `json:"usage_7d"`
	Window5h int64   `json:"window_5h"` // unix timestamp, 0 = not started
	Window1d int64   `json:"window_1d"`
	Window7d int64   `json:"window_7d"`
}

// UserPlatformQuotaKey 标识一个 user×platform，用于脏集出入与批量读。
type UserPlatformQuotaKey struct {
	UserID   int64
	Platform string
}

// UserPlatformQuotaCacheEntry Redis hash 反序列化结果。
//
// SchemaVersion 用于向后兼容：
//   - 0（旧 entry，无 SchemaVersion 字段）→ 视为 cache MISS，强制 refresh
//   - 1（当前版本）→ 包含 limits 和 window_start，可免 DB 查询
//
// limit 字段为 nil 表示"无限额"（DB 中对应列为 NULL）。
const UserPlatformQuotaCacheSchemaV1 = int64(1)

type UserPlatformQuotaCacheEntry struct {
	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64
	Version         int64
	SchemaVersion   int64

	// 以下字段仅在 SchemaVersion >= 1 时有效
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time
}

// BillingCache defines cache operations for billing service
type BillingCache interface {
	// Balance operations
	GetUserBalance(ctx context.Context, userID int64) (float64, error)
	SetUserBalance(ctx context.Context, userID int64, balance float64) error
	DeductUserBalance(ctx context.Context, userID int64, amount float64) error
	InvalidateUserBalance(ctx context.Context, userID int64) error

	// API Key rate limit operations
	GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*APIKeyRateLimitCacheData, error)
	SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *APIKeyRateLimitCacheData) error
	UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error

	// user × platform quota 缓存
	GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error)
	SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *UserPlatformQuotaCacheEntry, ttl time.Duration) error
	DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error
	// IncrUserPlatformQuotaUsageCache 在缓存命中时累加用量；缓存未命中（key 不存在）静默返回 nil。
	// markDirty=true 时将该 key 的 member 写入 Redis 脏集，供 flusher 批量回写 DB。
	IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error

	// 脏集读写，供 flusher 使用。
	PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]UserPlatformQuotaKey, error)
	ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []UserPlatformQuotaKey) error
	BatchGetUserPlatformQuotaCache(ctx context.Context, keys []UserPlatformQuotaKey) ([]*UserPlatformQuotaCacheEntry, error)
}

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

// applyDeepSeekPeakPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func applyDeepSeekPeakPricing(model string, pricing *ModelPricing, pricingAt time.Time) *ModelPricing {
	if pricingAt.IsZero() {
		pricingAt = timezone.Now()
	}
	return purepricing.ApplyDeepSeekPeakPricing(model, pricing, pricingAt)
}

// BillingService 计费服务
type BillingService struct {
	cfg            *config.Config
	pricingService *PricingService
	fallbackPrices map[string]*ModelPricing // 硬编码回退价格

	// fallbackWarnSeen 记录已输出 fallback pricing 警告的模型名，避免热路径每次请求重复刷日志。
	pricingWarnings provider.PricingWarnings
}

// NewBillingService 创建计费服务实例
func NewBillingService(cfg *config.Config, pricingService *PricingService) *BillingService {
	s := &BillingService{
		cfg:             cfg,
		pricingService:  pricingService,
		fallbackPrices:  make(map[string]*ModelPricing),
		pricingWarnings: provider.PricingWarnings{},
	}

	// 初始化硬编码回退价格（当动态价格不可用时使用）
	s.initFallbackPricing()

	return s
}

// initFallbackPricing 初始化硬编码回退价格（当动态价格不可用时使用）
// 价格单位：USD per token（与LiteLLM格式一致）
func (s *BillingService) initFallbackPricing() {
	s.fallbackPrices = purepricing.DefaultFallbackPrices()
}

// GetModelPricing 获取模型价格配置
func (s *BillingService) GetModelPricing(model string) (*ModelPricing, error) {
	model = strings.ToLower(model)
	var raw *LiteLLMModelPricing
	if s.pricingService != nil {
		raw = s.pricingService.GetModelPricing(model)
	}
	price, fallback, err := purepricing.ResolveModelPricing(model, raw, s.fallbackPrices, pricingModelPolicy(model))
	if fallback {
		s.pricingWarnings.Fallback(model)
	}
	return price, err
}

// GetModelPricingWithChannel 保留目录查价失败语义，覆盖计算由纯包完成。
func (s *BillingService) GetModelPricingWithChannel(model string, channelPricing *ChannelModelPricing) (*ModelPricing, error) {
	pricing, err := s.GetModelPricing(model)
	if err != nil {
		return nil, err
	}
	return purepricing.ApplyChannelPrice(pricing, channelPricing), nil
}

// --- 统一计费入口 ---

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

// CalculateCostUnified 统一计费入口，支持三种计费模式。
// 使用 ModelPricingResolver 解析定价，然后根据 BillingMode 分发计算。
func (s *BillingService) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	if input.Resolver == nil {
		// 无 Resolver，回退到旧路径
		breakdown, err := s.calculateCostInternal(input.Model, input.Tokens, input.RateMultiplier, input.ServiceTier, nil)
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

	return purepricing.CalculateCost(resolved, pureCostInputWithResolved(input, resolved))
}

// computeTokenBreakdown 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) computeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	return purepricing.ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, applyLongCtx)
}

// CalculateCost 计算使用费用
func (s *BillingService) CalculateCost(model string, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, error) {
	return s.calculateCostInternal(model, tokens, rateMultiplier, "", nil)
}

func (s *BillingService) CalculateCostWithServiceTier(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.calculateCostInternal(model, tokens, rateMultiplier, serviceTier, nil)
}

func (s *BillingService) calculateCostInternal(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, ChannelPricing *ChannelModelPricing) (*CostBreakdown, error) {
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
		pricing = applyDeepSeekPeakPricing(model, pricing, time.Time{})
	}

	return s.computeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, true), nil
}

// applyModelSpecificPricingPolicy 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) applyModelSpecificPricingPolicy(model string, pricing *ModelPricing) *ModelPricing {
	return purepricing.ApplyModelSpecificPricingPolicy(model, pricing, pricingModelPolicy(model))
}

// CalculateCostWithConfig 使用配置中的默认倍率计算费用
func (s *BillingService) CalculateCostWithConfig(model string, tokens UsageTokens) (*CostBreakdown, error) {
	multiplier := s.cfg.Default.RateMultiplier
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
func (s *BillingService) CalculateCostWithLongContext(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64) (*CostBreakdown, error) {
	return s.CalculateCostWithLongContextAndServiceTier(model, tokens, rateMultiplier, threshold, extraMultiplier, "")
}

// CalculateCostWithLongContextAndServiceTier 保留两段查询及部分失败返回，分段/金额合并委托纯包。
func (s *BillingService) CalculateCostWithLongContextAndServiceTier(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64, serviceTier string) (*CostBreakdown, error) {
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
func (s *BillingService) ListSupportedModels() []string {
	models := make([]string, 0)
	// 返回回退价格支持的模型系列
	for model := range s.fallbackPrices {
		models = append(models, model)
	}
	return models
}

// IsModelSupported 检查模型是否支持（现在总是返回true，因为有模糊匹配回退）
func (s *BillingService) IsModelSupported(model string) bool {
	// 所有Claude模型都有回退价格支持
	modelLower := strings.ToLower(model)
	return strings.Contains(modelLower, "claude") ||
		strings.Contains(modelLower, "opus") ||
		strings.Contains(modelLower, "sonnet") ||
		strings.Contains(modelLower, "haiku")
}

// GetEstimatedCost 估算费用（用于前端展示）
func (s *BillingService) GetEstimatedCost(model string, estimatedInputTokens, estimatedOutputTokens int) (float64, error) {
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
func (s *BillingService) GetPricingServiceStatus() map[string]any {
	if s.pricingService != nil {
		return s.pricingService.GetStatus()
	}
	return map[string]any{
		"model_count":  len(s.fallbackPrices),
		"last_updated": "using fallback",
		"local_hash":   "N/A",
	}
}

// ForceUpdatePricing 强制更新价格数据
func (s *BillingService) ForceUpdatePricing() error {
	if s.pricingService != nil {
		return s.pricingService.ForceUpdate()
	}
	return fmt.Errorf("pricing service not initialized")
}

// ModelDisplayPricing 保留旧用量/定价类型入口。
type ModelDisplayPricing = purepricing.ModelDisplayPricing

// ModelDisplayPricingInterval 保留旧用量/定价类型入口。
type ModelDisplayPricingInterval = purepricing.ModelDisplayPricingInterval

// GetDisplayPricing 返回用于模型广场展示的价格信息。
// 它会优先识别图片模型并展示按图计费，否则展示按 token 计费。
func (s *BillingService) GetDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	return s.getDisplayPricing(model, rateMultiplier)
}

// getDisplayPricing 使用分组倍率计算模型广场展示价格。
func (s *BillingService) getDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	rawPricing := s.getRawModelPricing(model)
	if hasExplicitImageGenerationPricing(rawPricing) || looksLikeImageModel(model) {
		return buildImageDisplayPricing(
			s.getDefaultImagePrice(model, "1K")*rateMultiplier,
			s.getDefaultImagePrice(model, "2K")*rateMultiplier,
			s.getDefaultImagePrice(model, "4K")*rateMultiplier,
		)
	}

	pricing, err := s.GetModelPricing(model)
	if err != nil || pricing == nil || !hasAnyDisplayTokenPricing(pricing) {
		return unknownDisplayPricing()
	}

	return buildTokenDisplayPricing(pricing, rateMultiplier)
}

// getDisplayPricingWithResolvedMultipliers 优先使用已解析的渠道价格计算展示价格。
func (s *BillingService) getDisplayPricingWithResolvedMultipliers(model string, rateMultiplier float64, resolved *ResolvedPricing) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	if resolved.IsUnpriced() {
		return unknownDisplayPricing()
	}
	if pricing, ok := displayPricingFromResolved(model, rateMultiplier, resolved); ok {
		return pricing
	}
	return s.getDisplayPricing(model, rateMultiplier)
}

// displayPricingFromResolved 委托纯定价实现，旧查询与配置投影保留在适配层。
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	return purepricing.DisplayPricingFromResolved(model, rateMultiplier, resolved)
}

// buildTokenDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func buildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	return purepricing.BuildTokenDisplayPricing(pricing, rateMultiplier)
}

// resolvedHasFastModeDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func resolvedHasFastModeDisplayPricing(resolved *ResolvedPricing) bool {
	return purepricing.ResolvedHasFastModeDisplayPricing(resolved)
}

// buildImageDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func buildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return purepricing.BuildImageDisplayPricing(price1K, price2K, price4K)
}

// unknownDisplayPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func unknownDisplayPricing() ModelDisplayPricing { return purepricing.UnknownDisplayPricing() }

// CalculateWebSearchCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateWebSearchCost(callCount, groupPrice, rateMultiplier)
}

// CalculateSearchCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateSearchCost(numCalls, groupPricePer1k, rateMultiplier)
}

// audioPriceConfig 保留旧用量/定价类型入口。
type audioPriceConfig = purepricing.AudioPriceConfig

// CalculateAudioCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *audioPriceConfig, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateAudioCost(mode, durationOrUnits, groupConfig, rateMultiplier)
}

// CalculateImageCost 在确定需要计费后才读取单价，金额计算由纯包完成。
func (s *BillingService) CalculateImageCost(model, imageSize string, imageCount int, rateMultiplier float64) *CostBreakdown {
	if imageCount <= 0 {
		return purepricing.CalculateImageCost(0, imageCount, rateMultiplier)
	}
	imageSize = NormalizeImageBillingTierOrDefault(imageSize)
	return purepricing.CalculateImageCost(s.getDefaultImagePrice(model, imageSize), imageCount, rateMultiplier)
}

// CalculateVideoCost 保留原有按需查价，显式传递每秒单价和用量。
func (s *BillingService) CalculateVideoCost(model, resolution string, videoCount, durationSeconds int, rateMultiplier float64) *CostBreakdown {
	if videoCount <= 0 {
		return purepricing.CalculateVideoCost(0, videoCount, durationSeconds, rateMultiplier)
	}
	resolution = NormalizeVideoBillingResolutionOrDefault(resolution)
	durationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	return purepricing.CalculateVideoCost(s.getDefaultVideoPrice(model, resolution), videoCount, durationSeconds, rateMultiplier)
}

// getDefaultImagePrice 先执行已有平台价卡回退，再按需读取目录。
func (s *BillingService) getDefaultImagePrice(model, imageSize string) float64 {
	if price, ok := purepricing.GetDefaultGrokImagineImagePrice(model, imageSize); ok {
		return price
	}
	var raw *LiteLLMModelPricing
	if s.pricingService != nil {
		raw = s.pricingService.GetModelPricing(model)
	}
	return purepricing.DefaultImagePrice(raw, imageSize)
}

func (s *BillingService) getRawModelPricing(model string) *LiteLLMModelPricing {
	if s == nil || s.pricingService == nil {
		return nil
	}
	return s.pricingService.GetModelPricing(model)
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

func (s *BillingService) getDefaultVideoPrice(model string, resolution string) float64 {
	if price, ok := getDefaultGrokImagineVideoPrice(model, resolution); ok {
		return price
	}

	// 内置 LiteLLM 数据没有视频输出价格，暂用历史模型默认价作为每秒单价兜底；
	// 分组视频价格仍可独立覆盖图片价格。
	return s.getDefaultImagePrice(model, ImageBillingSize2K)
}

// getDefaultGrokImagineVideoPrice 委托唯一媒体定价规则。
func getDefaultGrokImagineVideoPrice(model string, resolution string) (float64, bool) {
	return purepricing.GetDefaultGrokImagineVideoPrice(model, resolution)
}

// pureCostInput 在旧边界完成时区加载，纯计算不接收配置或查询接口。
func pureCostInput(input CostInput) purepricing.CostInput {
	var location *time.Location
	resolved := input.Resolved
	if resolved != nil && resolved.ChannelPricing != nil && resolved.ChannelPricing.TimePricing != nil {
		location, _ = loadChannelTimePricingLocation(resolved.ChannelPricing.TimePricing.Timezone)
	}
	modelAt := input.PricingAt
	if modelAt.IsZero() {
		modelAt = timezone.Now()
	}
	return purepricing.CostInput{
		Model:               input.Model,
		Tokens:              input.Tokens,
		RequestCount:        input.RequestCount,
		UsageUnits:          input.UsageUnits,
		SizeTier:            input.SizeTier,
		RateMultiplier:      input.RateMultiplier,
		PricingAt:           input.PricingAt,
		ModelPricingAt:      modelAt,
		ModelPolicy:         pricingModelPolicy(input.Model),
		ServiceTier:         input.ServiceTier,
		ReasoningEffort:     input.ReasoningEffort,
		TimePricingLocation: location,
	}
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

// pureCostInputWithResolved 复用已经解析的价卡，禁止为了投影重复读取渠道。
func pureCostInputWithResolved(input CostInput, resolved *ResolvedPricing) purepricing.CostInput {
	input.Resolved = resolved
	return pureCostInput(input)
}
