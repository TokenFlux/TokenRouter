package billing

import (
	"maps"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// default_catalog.go builds administrator price displays from catalog rates
// and the charging arithmetic.
// Administrator catalog display needs unit prices that follow the same token
// and service-tier arithmetic as charging. Synthetic token usage selects
// long-context rules, and the resulting input cost is normalized back to one
// token before million-token display conversion. Optional buckets keep their
// absent values separate from configured or catalog free amounts.
//
// Media tables expose recognized image sizes and video resolutions with
// per-unit source labels. Token context intervals use their own price records
// and service-tier choices, while root catalog presence flags still identify
// optional buckets. WithPriceCatalog fixes one clock instant for a catalog
// query, keeping current-time quotes within that result on the same
// observation.

// WithPriceCatalog returns a calculator copy bound to the supplied catalog and
// one captured clock instant. The receiver and supplied catalog remain
// available to their owners; the copy shares the existing location loader and
// uses that frozen instant for every later current-time pricing observation.
func (s *Calculator) WithPriceCatalog(catalog PriceCatalog) *Calculator {
	out := *s
	out.catalog = catalog
	now := s.options.Now()
	out.options.Now = func() time.Time { return now }
	return &out
}

// DefaultModelPrice builds catalog display amounts for the full model with the
// supplied platform/mode metadata. Known video tables return every valid
// resolution quote first; eligible image prices can coexist with token
// quotes. Source maps and displayed amount storage are built for this
// response.
//
// Token display uses the calculation rules for standard, Fast, Flex and
// long-context amounts, preserving missing optional buckets as absent values
// and known zero amounts as priced. Context intervals retain their own amounts
// and tier choices. Valid video tables expose all their dimensions, while
// conflicting or invalid video tables retain an unpriced result.
func (s *Calculator) DefaultModelPrice(model, platform, mode string) pricing.DefaultModelPrice {
	result := pricing.DefaultModelPrice{Model: model, Platform: platform, BillingMode: "token", PriceStatus: "unpriced", Prices: []pricing.DefaultPriceValue{}}
	add := func(key string, value float64, unit string) {
		result.Prices = append(result.Prices, pricing.DefaultPriceValue{Key: key, Value: &value, Unit: unit})
	}
	raw := s.RawModelPricing(model)
	result.BillingMode = pricing.DefaultBillingMode(model, mode, raw)
	if raw != nil {
		result.Source = raw.Source
		result.PriceSources = maps.Clone(raw.PriceSources)
	}
	knownVideo := raw != nil && len(raw.VideoPrices) > 0
	if knownVideo {
		if result.PriceSources == nil {
			result.PriceSources = map[string]string{}
		}
		prices, err := pricing.NormalizeVideoPrices(raw.VideoPrices)
		if err != nil {
			// An invalid table cannot establish default-price availability; keep this
			// row unpriced so administrators can identify its missing usable amounts.
			return result
		}
		for _, size := range slices.Sorted(maps.Keys(prices)) {
			add(size, prices[size], "USD/s")
			source := raw.PriceSources["video_prices"]
			if source == "" {
				source = raw.Source
			}
			result.PriceSources[size] = source
		}
		result.PriceStatus = "priced"
		return result
	}
	knownImage := raw != nil && len(raw.ImagePrices) > 0
	imageModel := mode == "image" || pricing.HasExplicitImageGenerationPricing(raw)
	// Chat metadata can mention a per-image amount while retaining token pricing;
	// select image display units when catalog media tables or image/token-absence
	// classification establish that path.
	hasImagePrice := knownImage || pricing.HasImageUnitPrice(raw) && (imageModel || raw.TokenPricingAbsent)
	if hasImagePrice {
		if result.PriceSources == nil {
			result.PriceSources = map[string]string{}
		}
		imageSource := result.PriceSources["image"]
		if imageSource == "" {
			imageSource = result.Source
		}
		if knownImage {
			if source := raw.PriceSources["image_prices"]; source != "" {
				imageSource = source
			}
		}
		// Quote recognized image dimensions independently; missing size-map entries
		// remain absent instead of borrowing a flat amount outside that table's
		// available dimensions.
		for _, size := range []string{"1K", "2K", "4K"} {
			price, ok := pricing.DefaultImagePrice(raw, size)
			if !ok {
				continue
			}
			add(size, price, "USD/image")
			if imageSource != "" {
				result.PriceSources[size] = imageSource
			}
		}
		result.PriceStatus = "priced"
	}
	base, err := pricing.ResolveModelPricing(model, raw)
	if err != nil || base == nil {
		// Token conversion can fail while independent image amounts are already
		// available; retain those collected media quotes and their current
		// availability status.
		return result
	}
	result.PriceStatus = "priced"
	base = s.applyCatalogTimePricing(base, s.options.Now())
	result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "", "", false)...)
	hasFast := false
	if _, ok := pricing.FastModeDisplayPricing(base); ok {
		result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "fast_", "priority", false)...)
		hasFast = true
	}
	hasFlex := base.FlexMultiplier != nil
	if hasFlex {
		result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "flex_", "flex", false)...)
	}
	if base.MaxReasoningEffortMultiplier != nil {
		add("max_reasoning", *base.MaxReasoningEffortMultiplier, "multiplier")
	}
	// Publish long-context quotes only for a positive threshold and an increased
	// input or output factor; each service-tier quote then evaluates that same
	// long-context choice.
	if base.LongContextInputThreshold > 0 && (base.LongContextInputMultiplier > 1 || base.LongContextOutputMultiplier > 1) {
		result.LongContextThreshold = base.LongContextInputThreshold
		result.LongContextThresholdInclusive = base.LongContextThresholdInclusive
		result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "long_", "", true)...)
		if hasFast {
			result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "long_fast_", "priority", true)...)
		}
		if hasFlex {
			result.Prices = append(result.Prices, defaultTokenPrices(base, raw, "long_flex_", "flex", true)...)
		}
	}

	if len(base.ContextPrices) > 0 {
		start := 0
		for i := 0; i <= len(base.ContextPrices); i++ {
			var end *int
			if i < len(base.ContextPrices) {
				threshold := base.ContextPrices[i].Threshold
				end = &threshold
			}
			intervalPricing := base
			if i > 0 {
				intervalPricing = base.ContextPrices[i-1].Pricing
			}
			prices := defaultTokenPrices(intervalPricing, raw, "", "", false)
			if _, ok := pricing.FastModeDisplayPricing(intervalPricing); ok {
				prices = append(prices, defaultTokenPrices(intervalPricing, raw, "fast_", "priority", false)...)
			}
			if intervalPricing.SupportsServiceTier {
				prices = append(prices, defaultTokenPrices(intervalPricing, raw, "flex_", "flex", false)...)
			}
			result.ContextIntervals = append(result.ContextIntervals, pricing.DefaultPriceInterval{MinTokens: start, MaxTokens: end, Prices: prices})
			if end != nil {
				start = *end
			}
		}
	}
	return result
}

// defaultTokenPrices returns token-unit display amounts from a non-nil
// normalized price record. raw supplies root presence and modality hints,
// prefix labels the returned keys, and tier selects service pricing. Caller
// thresholds and synthetic token sums must fit int.
//
// For long-context quotes, synthetic input reaches the configured threshold
// and crosses an exclusive threshold by one extra token. Input cost is divided
// by the complete synthetic count before conversion to USD per million tokens.
// Optional cache/image buckets retain absent versus known-zero amounts using
// both calculated rates and raw presence hints.
func defaultTokenPrices(base *pricing.ModelPricing, raw *pricing.CatalogModelPricing, prefix, tier string, applyLongCtx bool) []pricing.DefaultPriceValue {
	prices := make([]pricing.DefaultPriceValue, 0, 7)
	add := func(key string, value float64, unit string) {
		prices = append(prices, pricing.DefaultPriceValue{Key: key, Value: &value, Unit: unit})
	}
	// Synthetic input selects the long-context tier before other bucket quotes;
	// an exclusive threshold needs an extra token so the displayed rates exercise
	// that increased-price rule.
	gate := 0
	if applyLongCtx && base.LongContextInputThreshold > 0 {
		gate = base.LongContextInputThreshold
		if !base.LongContextThresholdInclusive {
			gate++
		}
	}
	quote := func(tokens UsageTokens) *CostBreakdown {
		tokens.InputTokens += gate
		return pricing.ComputeTokenBreakdown(base, tokens, 1, tier, applyLongCtx)
	}
	perMTok := func(cost float64, tokens int) float64 {
		// Average the synthetic input charge before million-token display
		// conversion.
		return cost / float64(tokens) * 1e6
	}
	addOptional := func(key string, value float64, applicable bool) {
		if applicable {
			// Display the single-token amount in million-token units.
			add(prefix+key, value*1e6, "USD/MTok")
		} else {
			// An unavailable optional bucket retains a nil Value in JSON,
			// distinguishing missing amounts from explicit zero prices in administrator
			// display.
			prices = append(prices, pricing.DefaultPriceValue{Key: prefix + key, Unit: "USD/MTok"})
		}
	}
	add(prefix+"input", perMTok(quote(UsageTokens{InputTokens: 1}).InputCost, 1+gate), "USD/MTok")
	// Output display uses the same million-token amount unit.
	addOptional("output", quote(UsageTokens{OutputTokens: 1}).OutputCost, raw == nil || raw.OutputPricePresent || base.OutputPricePerToken > 0)
	readPresent := base.CacheReadPricePerToken > 0 || raw != nil && raw.CacheReadPricePresent
	writePresent := base.CacheCreationPricePerToken > 0 || base.SupportsCacheBreakdown || raw != nil && raw.CacheCreationPricePresent
	addOptional("cache_read", quote(UsageTokens{CacheReadTokens: 1}).CacheReadCost, readPresent)
	addOptional("cache_write", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation5mTokens: 1}).CacheCreationCost, writePresent)
	addOptional("cache_write_1h", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation1hTokens: 1}).CacheCreationCost, base.SupportsCacheBreakdown)
	addOptional("image_input", quote(UsageTokens{InputTokens: 1, ImageInputTokens: 1}).ImageInputCost, base.ImageInputPricePerToken > 0 || raw != nil && (raw.SupportsVision || raw.ImageInputPricePresent))
	if raw != nil && raw.ImageCacheReadPricePresent {
		addOptional("image_cache_read", quote(UsageTokens{CacheReadTokens: 1, ImageCacheReadTokens: 1}).CacheReadCost, true)
	}
	addOptional("image_output", quote(UsageTokens{OutputTokens: 1, ImageOutputTokens: 1}).ImageOutputCost, base.ImageOutputPricePerToken > 0 || raw != nil && raw.ImageOutputPricePresent)
	return prices
}
