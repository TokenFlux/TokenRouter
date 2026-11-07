package pricing

import (
	"fmt"
	"strings"
)

// model_price.go converts catalog token prices into calculation records.
// A catalog media price does not by itself establish text-token pricing.
// ResolveModelPricing rejects missing or marked-unpriced token records, then
// carries the accepted entry's amount presence, service-tier rules, cache TTL
// behavior, and context prices into calculation data.
//
// Catalog context tiers are recursively converted before use. xAI's inclusive
// threshold is lowered by one in these absolute tiers so the calculation's
// strict comparison and the displayed (min,max] ranges switch at the original
// threshold. Time and multiplier rules are cloned from the source catalog
// entry; failed nested conversions are skipped, while the valid base remains
// available to its caller.

// ResolveModelPricing returns calculation prices for the supplied catalog token
// entry. Missing records, unpriced sources, and TokenPricingAbsent return
// ErrModelPricingUnavailable. The model name is lowercased for diagnostics; the
// supplied catalog identity already determines which prices are converted.
//
// Accepted context tiers are recursively copied, with xAI absolute thresholds
// shifted for inclusive switching and failed tier conversions omitted. Presence
// flags retain explicit zero priority and image-output prices. An explicitly
// supplied hourly cache price enables TTL support even at zero; legacy entries
// infer support from a positive hourly price above the short rate.
func ResolveModelPricing(model string, catalogPrice *CatalogModelPricing) (*ModelPricing, error) {
	model = strings.ToLower(model)
	if catalogPrice == nil || catalogPrice.Source == "unpriced" || catalogPrice.TokenPricingAbsent {
		return nil, fmt.Errorf("%w for model: %s", ErrModelPricingUnavailable, model)
	}

	catalogPricing := catalogPrice
	rules := catalogPricing.Clone()
	inclusiveThreshold := strings.EqualFold(catalogPricing.Provider, "xai")
	var contextPrices []ContextModelPrice
	for _, tier := range catalogPricing.ContextPrices {
		if tier.Pricing == nil {
			continue
		}
		price, err := ResolveModelPricing(model, tier.Pricing)
		// Nested conversion errors omit that tier; the valid base remains usable
		// instead of borrowing an incomplete tier's buckets.
		if err == nil {
			threshold := tier.Threshold
			// Absolute tiers are selected strictly above their internal threshold. Lower
			// xAI's value by one so an integer count at the catalog threshold selects
			// its higher tier.
			if inclusiveThreshold && threshold > 0 {
				threshold--
			}
			contextPrices = append(contextPrices, ContextModelPrice{Threshold: threshold, Pricing: price})
		}
	}
	// An explicit 1h cache price enables TTL billing, including an explicit zero.
	// Legacy entries infer TTL support when the 1h price exceeds the 5m price.
	price5m := catalogPricing.CacheCreationInputTokenCost
	price1h := catalogPricing.CacheCreationInputTokenCostAbove1hr
	enableBreakdown := price1h > 0 && price1h > price5m || catalogPricing.CacheCreation1hPricePresent
	return &ModelPricing{
		FastMultiplier:                     rules.FastMultiplier,
		FlexMultiplier:                     rules.FlexMultiplier,
		TimePricing:                        rules.TimePricing,
		CatalogSource:                      catalogPricing.Source,
		CacheCreationPriceExplicit:         catalogPricing.Source != "" && catalogPricing.CacheCreationPricePresent && catalogPricing.CacheCreationInputTokenCost == 0,
		PriorityInputPresent:               catalogPricing.PriorityInputPresent,
		PriorityOutputPresent:              catalogPricing.PriorityOutputPresent,
		PriorityCacheReadPresent:           catalogPricing.PriorityCacheReadPresent,
		PriorityCacheWritePresent:          catalogPricing.PriorityCacheWritePresent,
		ContextPrices:                      contextPrices,
		InputPricePerToken:                 catalogPricing.InputCostPerToken,
		InputPricePerTokenPriority:         catalogPricing.InputCostPerTokenPriority,
		OutputPricePerToken:                catalogPricing.OutputCostPerToken,
		OutputPricePerTokenPriority:        catalogPricing.OutputCostPerTokenPriority,
		CacheCreationPricePerToken:         catalogPricing.CacheCreationInputTokenCost,
		CacheCreationPricePerTokenPriority: catalogPricing.CacheCreationInputTokenCostPriority,
		CacheReadPricePerToken:             catalogPricing.CacheReadInputTokenCost,
		CacheReadPricePerTokenPriority:     catalogPricing.CacheReadInputTokenCostPriority,
		CacheCreation5mPrice:               price5m,
		CacheCreation1hPrice:               price1h,
		SupportsCacheBreakdown:             enableBreakdown,
		SupportsServiceTier:                catalogPricing.SupportsServiceTier,
		LongContextThresholdInclusive:      inclusiveThreshold,
		LongContextInputThreshold:          catalogPricing.LongContextInputTokenThreshold,
		LongContextInputMultiplier:         catalogPricing.LongContextInputCostMultiplier,
		LongContextOutputMultiplier:        catalogPricing.LongContextOutputCostMultiplier,
		ImageInputPricePerToken:            catalogPricing.InputCostPerImageToken,
		ImageCacheReadPricePerToken:        catalogPricing.CacheReadInputImageTokenCost,
		ImageCacheReadPriceExplicit:        catalogPricing.ImageCacheReadPricePresent,
		ImageOutputPricePerToken:           catalogPricing.OutputCostPerImageToken,
		ImageOutputPriceExplicit:           catalogPricing.ImageOutputPricePresent,
		MaxReasoningEffortMultiplier:       rules.MaxReasoningEffortMultiplier,
	}, nil
}
