package service

import "strings"

const qoderBillingModelPrefix = "qoder:"

var qoderCreditMultipliers = map[string]float64{
	"auto":        1.0,
	"ultimate":    1.6,
	"performance": 1.1,
	"efficient":   0.3,
	"lite":        0,

	"qmodel_latest": 0.5,
	"qmodel":        0.1,
	"q35model":      0.1,
	"dmodel":        0.5,
	"dfmodel":       0.1,
	"gmodel":        0.6,
	"gm51model":     0.6,
	"kmodel":        0.3,
	"mmodel":        0.4,
}

var qoderBasePricing = &ModelPricing{
	InputPricePerToken:         1e-6,
	OutputPricePerToken:        1e-6,
	CacheCreationPricePerToken: 1e-6,
	CacheReadPricePerToken:     1e-6,
	SupportsCacheBreakdown:     false,
}

func qoderBillingModel(requestedModel, upstreamModel string) string {
	key := qoderRouteKeyForPricing(requestedModel)
	if key == "" {
		key = qoderRouteKeyForPricing(upstreamModel)
	}
	if key == "" {
		return ""
	}
	return qoderBillingModelPrefix + key
}

func qoderRouteKeyForPricing(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	model = strings.TrimPrefix(model, qoderBillingModelPrefix)
	if _, ok := qoderCreditMultipliers[model]; ok {
		return model
	}
	if info, ok := lookupQoderModelAlias(model); ok {
		key := strings.ToLower(strings.TrimSpace(info.Key))
		if _, ok := qoderCreditMultipliers[key]; ok {
			return key
		}
	}
	return ""
}

func qoderPricingForBillingModel(model string) (*ModelPricing, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(model, qoderBillingModelPrefix) {
		return nil, false
	}
	key := strings.TrimPrefix(model, qoderBillingModelPrefix)
	multiplier, ok := qoderCreditMultipliers[key]
	if !ok {
		return nil, false
	}
	pricing := *qoderBasePricing
	scaleModelPricing(&pricing, multiplier)
	return &pricing, true
}

func scaleModelPricing(pricing *ModelPricing, multiplier float64) {
	if pricing == nil {
		return
	}
	pricing.InputPricePerToken *= multiplier
	pricing.InputPricePerTokenPriority *= multiplier
	pricing.OutputPricePerToken *= multiplier
	pricing.OutputPricePerTokenPriority *= multiplier
	pricing.CacheCreationPricePerToken *= multiplier
	pricing.CacheReadPricePerToken *= multiplier
	pricing.CacheReadPricePerTokenPriority *= multiplier
	pricing.CacheCreation5mPrice *= multiplier
	pricing.CacheCreation1hPrice *= multiplier
	pricing.ImageOutputPricePerToken *= multiplier
}
