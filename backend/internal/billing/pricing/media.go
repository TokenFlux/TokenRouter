package pricing

import (
	"strings"
)

func GetDefaultGrokImagineImagePrice(model string, imageSize string) (float64, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "grok-imagine-image-2.0":
		return GetGrokImagineImageTierPrice(
			imageSize,
			DefaultGrokImagineImage20Price1K,
			DefaultGrokImagineImage20Price2K,
		), true
	case "grok-imagine-image-quality":
		return GetGrokImagineImageTierPrice(
			imageSize,
			DefaultGrokImagineImageQualityPrice1K,
			DefaultGrokImagineImageQualityPrice2K,
		), true
	case "grok-imagine", "grok-imagine-image", "grok-imagine-edit":
		return GetGrokImagineImageTierPrice(
			imageSize,
			DefaultGrokImagineImagePrice1K,
			DefaultGrokImagineImagePrice2K,
		), true
	default:
		return 0, false
	}
}

func GetGrokImagineImageTierPrice(imageSize string, price1K float64, price2K float64) float64 {
	switch NormalizeImageBillingTierOrDefault(imageSize) {
	case ImageBillingSize1K:
		return price1K
	case ImageBillingSize2K, ImageBillingSize4K:
		return price2K
	default:
		return price2K
	}
}

func GetDefaultGrokImagineVideoPrice(model string, resolution string) (float64, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "grok-imagine-video-1.5"):
		switch NormalizeVideoBillingResolutionOrDefault(resolution) {
		case VideoBillingResolution480P:
			return DefaultGrokImagineVideo15Price480P, true
		case VideoBillingResolution720P:
			return DefaultGrokImagineVideo15Price720P, true
		case VideoBillingResolution1080P:
			return DefaultGrokImagineVideo15Price1080P, true
		default:
			return DefaultGrokImagineVideo15Price480P, true
		}
	case strings.HasPrefix(model, "grok-imagine-video"):
		switch NormalizeVideoBillingResolutionOrDefault(resolution) {
		case VideoBillingResolution480P:
			return DefaultGrokImagineVideoPrice480P, true
		case VideoBillingResolution720P, VideoBillingResolution1080P:
			return DefaultGrokImagineVideoPrice720P, true
		default:
			return DefaultGrokImagineVideoPrice480P, true
		}
	default:
		return 0, false
	}
}

// CalculateImageCost 对显式单张价格计算费用，保持乘法顺序与负倍率回退。
func CalculateImageCost(unitPrice float64, imageCount int, rateMultiplier float64) *CostBreakdown {
	if imageCount <= 0 {
		return &CostBreakdown{}
	}
	// 计算总费用
	totalCost := unitPrice * float64(imageCount)

	// 应用倍率（保存时强制 > 0；负数按 0 处理避免按 1x 误扣）
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	actualCost := totalCost * rateMultiplier

	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  actualCost,
		BillingMode: string(BillingModeImage),
	}
}

// CalculateVideoCost 对显式每秒价格计算费用，时长规则使用同一纯定义。
func CalculateVideoCost(perSecondPrice float64, videoCount, durationSeconds int, rateMultiplier float64) *CostBreakdown {
	if videoCount <= 0 {
		return &CostBreakdown{}
	}
	durationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	totalCost := perSecondPrice * float64(durationSeconds) * float64(videoCount)

	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	actualCost := totalCost * rateMultiplier

	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  actualCost,
		BillingMode: string(BillingModeVideo),
	}
}

// DefaultImagePrice 保留目录正价与历史尺寸倍率，不执行目录查询。
func DefaultImagePrice(catalogPrice *LiteLLMModelPricing, imageSize string) float64 {
	basePrice := 0.0

	// 从 PricingService 获取 output_cost_per_image
	if catalogPrice != nil {
		pricing := catalogPrice
		if pricing != nil && pricing.OutputCostPerImage > 0 {
			basePrice = pricing.OutputCostPerImage
		}
	}

	// 如果没有找到价格，使用硬编码默认值（$0.134，来自 gemini-3-pro-image-preview）
	if basePrice <= 0 {
		basePrice = DefaultImageGenerationPrice
	}

	// 2K 尺寸 1.5 倍，4K 尺寸翻倍
	if imageSize == "2K" {
		return basePrice * 1.5
	}
	if imageSize == "4K" {
		return basePrice * 2
	}

	return basePrice
}
