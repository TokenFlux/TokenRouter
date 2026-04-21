package dto

import "github.com/TokenFlux/TokenRouter/internal/service"

type ModelMarketplacePricing struct {
	PricingMode              string  `json:"pricing_mode"`
	PriceStatus              string  `json:"price_status"`
	InputPricePerToken       float64 `json:"input_price_per_token,omitempty"`
	OutputPricePerToken      float64 `json:"output_price_per_token,omitempty"`
	CacheWritePricePerToken  float64 `json:"cache_write_price_per_token,omitempty"`
	CacheReadPricePerToken   float64 `json:"cache_read_price_per_token,omitempty"`
	ImageOutputPricePerToken float64 `json:"image_output_price_per_token,omitempty"`
	ImagePrice1K             float64 `json:"image_price_1k,omitempty"`
	ImagePrice2K             float64 `json:"image_price_2k,omitempty"`
	ImagePrice4K             float64 `json:"image_price_4k,omitempty"`
}

type ModelMarketplaceModel struct {
	ID          string                  `json:"id"`
	DisplayName string                  `json:"display_name"`
	Pricing     ModelMarketplacePricing `json:"pricing"`
}

type ModelMarketplaceGroup struct {
	ID             int64                   `json:"id"`
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	Platform       string                  `json:"platform"`
	RateMultiplier float64                 `json:"rate_multiplier"`
	ModelCount     int                     `json:"model_count"`
	Models         []ModelMarketplaceModel `json:"models"`
}

func ModelMarketplaceGroupsFromService(groups []service.ModelMarketplaceGroup) []ModelMarketplaceGroup {
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for _, group := range groups {
		models := make([]ModelMarketplaceModel, 0, len(group.Models))
		for _, model := range group.Models {
			models = append(models, ModelMarketplaceModel{
				ID:          model.ID,
				DisplayName: model.DisplayName,
				Pricing: ModelMarketplacePricing{
					PricingMode:              model.Pricing.PricingMode,
					PriceStatus:              model.Pricing.PriceStatus,
					InputPricePerToken:       model.Pricing.InputPricePerToken,
					OutputPricePerToken:      model.Pricing.OutputPricePerToken,
					CacheWritePricePerToken:  model.Pricing.CacheWritePricePerToken,
					CacheReadPricePerToken:   model.Pricing.CacheReadPricePerToken,
					ImageOutputPricePerToken: model.Pricing.ImageOutputPricePerToken,
					ImagePrice1K:             model.Pricing.ImagePrice1K,
					ImagePrice2K:             model.Pricing.ImagePrice2K,
					ImagePrice4K:             model.Pricing.ImagePrice4K,
				},
			})
		}

		out = append(out, ModelMarketplaceGroup{
			ID:             group.ID,
			Name:           group.Name,
			Description:    group.Description,
			Platform:       group.Platform,
			RateMultiplier: group.RateMultiplier,
			ModelCount:     group.ModelCount,
			Models:         models,
		})
	}

	return out
}
