package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type ModelMarketplaceStats struct {
	TodayTokens int64 `json:"today_tokens"`
	TotalTokens int64 `json:"total_tokens"`
	TotalUsers  int64 `json:"total_users"`
}

type ModelMarketplacePricing struct {
	PricingMode                  string                            `json:"pricing_mode"`
	PriceStatus                  string                            `json:"price_status"`
	InputPricePerToken           float64                           `json:"input_price_per_token,omitempty"`
	OutputPricePerToken          float64                           `json:"output_price_per_token,omitempty"`
	CacheWritePricePerToken      float64                           `json:"cache_write_price_per_token,omitempty"`
	CacheReadPricePerToken       float64                           `json:"cache_read_price_per_token,omitempty"`
	ImageOutputPricePerToken     float64                           `json:"image_output_price_per_token,omitempty"`
	FastInputPricePerToken       float64                           `json:"fast_input_price_per_token,omitempty"`
	FastOutputPricePerToken      float64                           `json:"fast_output_price_per_token,omitempty"`
	FastCacheWritePricePerToken  float64                           `json:"fast_cache_write_price_per_token,omitempty"`
	FastCacheReadPricePerToken   float64                           `json:"fast_cache_read_price_per_token,omitempty"`
	FastImageOutputPricePerToken float64                           `json:"fast_image_output_price_per_token,omitempty"`
	ContextIntervals             []ModelMarketplacePricingInterval `json:"context_intervals,omitempty"`
	ImagePrice1K                 float64                           `json:"image_price_1k,omitempty"`
	ImagePrice2K                 float64                           `json:"image_price_2k,omitempty"`
	ImagePrice4K                 float64                           `json:"image_price_4k,omitempty"`
}

// ModelMarketplacePricingInterval 是前端模型广场展示用的上下文区间价格。
type ModelMarketplacePricingInterval struct {
	MinTokens                    int     `json:"min_tokens"`
	MaxTokens                    *int    `json:"max_tokens,omitempty"`
	InputPricePerToken           float64 `json:"input_price_per_token,omitempty"`
	OutputPricePerToken          float64 `json:"output_price_per_token,omitempty"`
	CacheWritePricePerToken      float64 `json:"cache_write_price_per_token,omitempty"`
	CacheReadPricePerToken       float64 `json:"cache_read_price_per_token,omitempty"`
	ImageOutputPricePerToken     float64 `json:"image_output_price_per_token,omitempty"`
	FastInputPricePerToken       float64 `json:"fast_input_price_per_token,omitempty"`
	FastOutputPricePerToken      float64 `json:"fast_output_price_per_token,omitempty"`
	FastCacheWritePricePerToken  float64 `json:"fast_cache_write_price_per_token,omitempty"`
	FastCacheReadPricePerToken   float64 `json:"fast_cache_read_price_per_token,omitempty"`
	FastImageOutputPricePerToken float64 `json:"fast_image_output_price_per_token,omitempty"`
}

type ModelMarketplaceModel struct {
	ID          string                  `json:"id"`
	DisplayName string                  `json:"display_name"`
	Pricing     ModelMarketplacePricing `json:"pricing"`
}

type ModelMarketplaceGroup struct {
	ID                 int64                   `json:"id"`
	Name               string                  `json:"name"`
	Description        string                  `json:"description"`
	Platform           string                  `json:"platform"`
	DisplayBrand       string                  `json:"display_brand"`
	SortOrder          int                     `json:"sort_order"`
	RateMultiplier     float64                 `json:"rate_multiplier"`
	OfficialPriceRatio *float64                `json:"official_price_ratio,omitempty"`
	ModelCount         int                     `json:"model_count"`
	Models             []ModelMarketplaceModel `json:"models"`
}

func ModelMarketplaceGroupsFromService(groups []service.ModelMarketplaceGroup) []ModelMarketplaceGroup {
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for _, group := range groups {
		models := make([]ModelMarketplaceModel, 0, len(group.Models))
		for _, model := range group.Models {
			models = append(models, ModelMarketplaceModel{
				ID:          model.ID,
				DisplayName: model.DisplayName,
				Pricing:     modelMarketplacePricingFromService(model.Pricing),
			})
		}

		out = append(out, ModelMarketplaceGroup{
			ID:                 group.ID,
			Name:               group.Name,
			Description:        group.Description,
			Platform:           group.Platform,
			DisplayBrand:       group.DisplayBrand,
			SortOrder:          group.SortOrder,
			RateMultiplier:     group.RateMultiplier,
			OfficialPriceRatio: group.OfficialPriceRatio,
			ModelCount:         group.ModelCount,
			Models:             models,
		})
	}

	return out
}

// modelMarketplacePricingFromService 将服务层价格快照转换为接口 DTO。
func modelMarketplacePricingFromService(pricing service.ModelDisplayPricing) ModelMarketplacePricing {
	intervals := make([]ModelMarketplacePricingInterval, 0, len(pricing.ContextIntervals))
	for _, interval := range pricing.ContextIntervals {
		intervals = append(intervals, ModelMarketplacePricingInterval{
			MinTokens:                    interval.MinTokens,
			MaxTokens:                    interval.MaxTokens,
			InputPricePerToken:           interval.InputPricePerToken,
			OutputPricePerToken:          interval.OutputPricePerToken,
			CacheWritePricePerToken:      interval.CacheWritePricePerToken,
			CacheReadPricePerToken:       interval.CacheReadPricePerToken,
			ImageOutputPricePerToken:     interval.ImageOutputPricePerToken,
			FastInputPricePerToken:       interval.FastInputPricePerToken,
			FastOutputPricePerToken:      interval.FastOutputPricePerToken,
			FastCacheWritePricePerToken:  interval.FastCacheWritePricePerToken,
			FastCacheReadPricePerToken:   interval.FastCacheReadPricePerToken,
			FastImageOutputPricePerToken: interval.FastImageOutputPricePerToken,
		})
	}

	return ModelMarketplacePricing{
		PricingMode:                  pricing.PricingMode,
		PriceStatus:                  pricing.PriceStatus,
		InputPricePerToken:           pricing.InputPricePerToken,
		OutputPricePerToken:          pricing.OutputPricePerToken,
		CacheWritePricePerToken:      pricing.CacheWritePricePerToken,
		CacheReadPricePerToken:       pricing.CacheReadPricePerToken,
		ImageOutputPricePerToken:     pricing.ImageOutputPricePerToken,
		FastInputPricePerToken:       pricing.FastInputPricePerToken,
		FastOutputPricePerToken:      pricing.FastOutputPricePerToken,
		FastCacheWritePricePerToken:  pricing.FastCacheWritePricePerToken,
		FastCacheReadPricePerToken:   pricing.FastCacheReadPricePerToken,
		FastImageOutputPricePerToken: pricing.FastImageOutputPricePerToken,
		ContextIntervals:             intervals,
		ImagePrice1K:                 pricing.ImagePrice1K,
		ImagePrice2K:                 pricing.ImagePrice2K,
		ImagePrice4K:                 pricing.ImagePrice4K,
	}
}

func ModelMarketplaceStatsFromService(stats *service.DashboardPublicStats) ModelMarketplaceStats {
	return ModelMarketplaceStats{
		TodayTokens: stats.TodayTokens,
		TotalTokens: stats.TotalTokens,
		TotalUsers:  stats.TotalUsers,
	}
}
