package service

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

// 以下为 groups.video_model_prices JSONB 使用的标准视频价格族键。
const (
	VideoPriceFamilyGrokImagineVideo   = "grok-imagine-video"
	VideoPriceFamilyGrokImagineVideo15 = "grok-imagine-video-1.5"
)

// CanonicalGrokImagineVideoPriceFamily 将模型别名、预览版与旧版 ID
// 归一化为 video_model_prices 中存储的价格族键。
func CanonicalGrokImagineVideoPriceFamily(model string) string {
	if model == "" {
		return ""
	}
	// 已知别名优先使用共享 xAI 辅助函数；未来新增的原生 Imagine 模型保持独立，
	// 便于运营方分别配置价格。
	if c := xai.CanonicalImagineVideoModel(model); c != "" {
		switch c {
		case xai.DefaultImagineVideo15Model:
			return VideoPriceFamilyGrokImagineVideo15
		case xai.DefaultImagineVideoModel:
			return VideoPriceFamilyGrokImagineVideo
		}
		if strings.HasPrefix(c, "grok-imagine-video-") {
			return c
		}
	}
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
		if strings.HasPrefix(m, prefix) {
			m = strings.TrimPrefix(m, prefix)
			break
		}
	}
	switch {
	case m == "grok-imagine-video-1.5" || m == "grok-imagine-video-1.5-preview" ||
		m == "grok-video-1.5" || strings.Contains(m, "video-1.5"):
		return VideoPriceFamilyGrokImagineVideo15
	case m == "grok-imagine-video" || m == "grok-imagine-video-preview" ||
		m == "grok-video" || m == "grok-video-latest":
		return VideoPriceFamilyGrokImagineVideo
	default:
		return ""
	}
}

// NormalizeVideoModelPrices 清理并标准化按模型划分的分辨率价格映射。
// 键会转换为价格族，档位采用 480p、720p 与 1080p，负价格会被丢弃。
//
// 模型键按排序结果遍历，而不是依赖 Go 映射顺序。多个别名可能归一化到同一价格族，
// 无序遍历会导致冲突档位的最终价格在不同进程间变化。
// 无法识别的档位会记录警告并丢弃，不会静默归入 480p 档位。
func NormalizeVideoModelPrices(in map[string]map[string]float64) map[string]map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	modelKeys := make([]string, 0, len(in))
	for modelKey := range in {
		modelKeys = append(modelKeys, modelKey)
	}
	sort.Strings(modelKeys)
	out := make(map[string]map[string]float64)
	for _, modelKey := range modelKeys {
		tierPrices := in[modelKey]
		if len(tierPrices) == 0 {
			continue
		}
		family := CanonicalGrokImagineVideoPriceFamily(modelKey)
		if family == "" {
			key := strings.ToLower(strings.TrimSpace(modelKey))
			switch key {
			case VideoPriceFamilyGrokImagineVideo, VideoPriceFamilyGrokImagineVideo15:
				family = key
			default:
				if key == "" {
					continue
				}
				family = key
			}
		}
		normalizedTiers := out[family]
		if normalizedTiers == nil {
			normalizedTiers = make(map[string]float64)
		}
		tierKeys := make([]string, 0, len(tierPrices))
		for tierKey := range tierPrices {
			tierKeys = append(tierKeys, tierKey)
		}
		sort.Strings(tierKeys)
		for _, tierKey := range tierKeys {
			price := tierPrices[tierKey]
			if price < 0 {
				continue
			}
			tier, ok := LookupVideoBillingResolution(tierKey)
			if !ok {
				slog.Warn("video_model_prices_unknown_resolution_dropped",
					"model_key", modelKey,
					"family", family,
					"resolution", tierKey)
				continue
			}
			if existing, exists := normalizedTiers[tier]; exists && existing != price {
				slog.Warn("video_model_prices_conflicting_tier_price",
					"model_key", modelKey,
					"family", family,
					"resolution", tier,
					"previous_price", existing,
					"price", price)
			}
			normalizedTiers[tier] = price
		}
		if len(normalizedTiers) > 0 {
			out[family] = normalizedTiers
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LookupVideoModelPrice 从模型与分辨率映射中返回每秒价格，未命中时返回 nil。
func LookupVideoModelPrice(prices map[string]map[string]float64, model, resolution string) *float64 {
	if len(prices) == 0 {
		return nil
	}
	family := CanonicalGrokImagineVideoPriceFamily(model)
	if family == "" {
		family = strings.ToLower(strings.TrimSpace(model))
	}
	if family == "" {
		return nil
	}
	tierPrices, ok := prices[family]
	if !ok || len(tierPrices) == 0 {
		return nil
	}
	tier := NormalizeVideoBillingResolutionOrDefault(resolution)
	if price, ok := tierPrices[tier]; ok {
		p := price
		return &p
	}
	return nil
}
