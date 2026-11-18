package pricing

import (
	"maps"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// catalog_service_tier.go converts the directory's supported mode prices into
// bucket adjustments and evaluates them at the request's pricing instant.
// Per-bucket prices serve both settlement and displayed service-tier quotes.
//
// Explicit mode prices take precedence over a corresponding multiplier. A
// known base price lets the explicit mode amount retain its relative effect
// after a context or administrator override. If the directory supplies only
// the mode amount, that amount can establish the applicable bucket. Calendar
// comparisons use the directory rule's market timezone and do not depend on
// the server's local settings.

// CatalogServicePrice holds token price adjustments limited by service tier or date.
type CatalogServicePrice struct {
	Tier       string             `json:"tier"`                  // normalized service tier
	After      string             `json:"after,omitempty"`       // first applicable date
	Before     string             `json:"before,omitempty"`      // first excluded date
	Zone       string             `json:"zone,omitempty"`        // billing date timezone
	Factor     *float64           `json:"factor,omitempty"`      // factor for all token buckets
	TextFactor *float64           `json:"text_factor,omitempty"` // text token price factor
	Factors    map[string]float64 `json:"factors,omitempty"`     // bucket factors by name
	Amounts    map[string]float64 `json:"amounts,omitempty"`     // standalone USD per token
}

// ServiceTierTokenQuote exposes the priced token buckets of a service tier.
type ServiceTierTokenQuote struct {
	Input         float64  `json:"input_price_per_token,omitempty"`           // USD per ordinary input token
	Output        float64  `json:"output_price_per_token,omitempty"`          // USD per output token
	CacheRead     float64  `json:"cache_read_price_per_token,omitempty"`      // USD per cache read
	CacheWrite    float64  `json:"cache_write_price_per_token,omitempty"`     // USD per aggregate cache write
	CacheWrite1h  float64  `json:"cache_write_1h_price_per_token,omitempty"`  // USD per hourly cache write
	CacheWrite30m *float64 `json:"cache_write_30m_price_per_token,omitempty"` // USD per thirty-minute cache write
	CacheWrite12h *float64 `json:"cache_write_12h_price_per_token,omitempty"` // USD per twelve-hour cache write
	ImageInput    float64  `json:"image_input_price_per_token,omitempty"`     // USD per image input token
	ImageOutput   float64  `json:"image_output_price_per_token,omitempty"`    // USD per image output token
}

// normalizeCatalogServicePrices converts v2 service amounts to billing units.
// Explicit mode amounts become relative bucket factors when their base is
// positive. A zero or missing base retains the explicit amount without risking
// division by zero. Each rule remains separate until the service tier and date
// can be determined for the request.
func normalizeCatalogServicePrices(source []modelcatalog.ServicePrice, base modelcatalog.Cost) []CatalogServicePrice {
	rows := make([]CatalogServicePrice, 0, len(source))
	baseBuckets := catalogServiceBuckets(base)
	for _, price := range source {
		row := CatalogServicePrice{Tier: price.Tier, After: price.After, Before: price.Before, Zone: price.Zone, Factor: price.Factor, TextFactor: price.TextFactor}
		if price.Cost != nil {
			row.Factors, row.Amounts = map[string]float64{}, map[string]float64{}
			for key, amount := range catalogServiceBuckets(*price.Cost) {
				if amount == nil {
					continue
				}
				if base := baseBuckets[key]; base != nil && *base > 0 {
					row.Factors[key] = *amount / *base
				} else {
					row.Amounts[key] = *amount / 1e6
				}
			}
		}
		if price.Factors != nil {
			row.Factors = map[string]float64{}
			for key, factor := range catalogServiceBuckets(*price.Factors) {
				if factor != nil {
					row.Factors[key] = *factor
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// catalogServiceBuckets names the source fields represented by token settlement.
func catalogServiceBuckets(cost modelcatalog.Cost) map[string]*float64 {
	return map[string]*float64{
		"input": cost.Input, "output": cost.Output,
		"cache_read": cost.CacheRead, "cache_write": cost.CacheWrite,
		"cache_write_1h": cost.CacheWrite1h, "cache_write_30m": cost.CacheWrite30m, "cache_write_12h": cost.CacheWrite12h,
		"image_input": cost.ImageInput, "image_read": cost.ImageRead, "image_output": cost.ImageOutput,
	}
}

// catalogServiceTierPricing selects one matching rule and returns adjusted
// prices by value. Manual Fast and Flex factors take priority. Explicit quotes
// are already ordered before catalog multipliers, with date ranges determining
// which multiplier matches. The caller must avoid applying legacy tier
// adjustments again after a successful selection.
func catalogServiceTierPricing(pricing *ModelPricing, tier string, at time.Time) (*ModelPricing, bool) {
	if pricing == nil {
		return nil, false
	}
	tier = NormalizeBillingServiceTier(tier)
	if tier == "fast" {
		tier = "priority"
	}
	if tier == "" || tier == "priority" && (pricing.FastMultiplier != nil || pricing.FastModeMultiplier != nil) ||
		tier == "flex" && pricing.FlexMultiplier != nil {
		return nil, false
	}
	for _, row := range pricing.ServiceTierPrices {
		if row.Tier != tier || !row.appliesAt(at) {
			continue
		}
		return row.applyToTokenRates(pricing), true
	}
	return nil, false
}

// applyCatalogCalendarPricing applies date-only rules to the selected tier.
// These rates compose with the tier already selected by the caller. A copy
// is made only for an applicable rule, leaving the published prices stable
// across requests and quotes evaluated at different instants.
func applyCatalogCalendarPricing(pricing *ModelPricing, at time.Time) *ModelPricing {
	for _, row := range pricing.ServiceTierPrices {
		if isCatalogCalendarRule(row) && row.appliesAt(at) {
			pricing = row.applyToTokenRates(pricing)
		}
	}
	return pricing
}

// isCatalogCalendarRule identifies a date adjustment shared by all service tiers.
func isCatalogCalendarRule(row CatalogServicePrice) bool {
	return row.Tier == "" && (row.After != "" || row.Before != "")
}

// retainFixedCalendarRates keeps explicitly configured amounts independent of
// directory date changes. Only the buckets supplied at this card level are
// protected, so partly configured prices still inherit the current directory
// rates for omitted amounts. Copies keep the shared catalog rules immutable.
func retainFixedCalendarRates(pricing *ModelPricing, card *ModelPricingEntry) {
	if !slices.ContainsFunc(pricing.ServiceTierPrices, isCatalogCalendarRule) {
		return
	}
	fixed := map[string]bool{
		"input":           card.InputPrice != nil,
		"output":          card.OutputPrice != nil,
		"cache_read":      card.CacheReadPrice != nil,
		"cache_write":     card.CacheWritePrice != nil,
		"cache_write_1h":  card.CacheWritePrice != nil || card.CacheWrite1hPrice != nil,
		"cache_write_30m": card.CacheWritePrice != nil || card.CacheWrite30mPrice != nil,
		"cache_write_12h": card.CacheWritePrice != nil || card.CacheWrite12hPrice != nil,
		"image_input":     card.ImageInputPrice != nil,
		"image_read":      card.CacheReadPrice != nil,
		"image_output":    card.ImageOutputPrice != nil,
	}
	rules := slices.Clone(pricing.ServiceTierPrices)
	for i := range rules {
		if !isCatalogCalendarRule(rules[i]) {
			continue
		}
		factors := make(map[string]float64, len(rules[i].Factors)+len(fixed))
		maps.Copy(factors, rules[i].Factors)
		for key, configured := range fixed {
			if configured {
				factors[key] = 1
			}
		}
		rules[i].Factors = factors
	}
	pricing.ServiceTierPrices = rules
}

// applyToTokenRates adjusts a copy of each token bucket, preserving absence
// for optional write prices. The caller can select another rule from the
// original record without sharing any mutated prices.
func (r CatalogServicePrice) applyToTokenRates(pricing *ModelPricing) *ModelPricing {
	out := *pricing
	out.InputPricePerToken = r.adjust("input", out.InputPricePerToken)
	out.OutputPricePerToken = r.adjust("output", out.OutputPricePerToken)
	out.CacheReadPricePerToken = r.adjust("cache_read", out.CacheReadPricePerToken)
	out.CacheCreationPricePerToken = r.adjust("cache_write", out.CacheCreationPricePerToken)
	out.CacheCreation5mPrice = r.adjust("cache_write", out.CacheCreation5mPrice)
	out.CacheCreation1hPrice = r.adjust("cache_write_1h", out.CacheCreation1hPrice)
	out.CacheCreation30mPrice = r.adjustOptional("cache_write_30m", out.CacheCreation30mPrice)
	out.CacheCreation12hPrice = r.adjustOptional("cache_write_12h", out.CacheCreation12hPrice)
	out.ImageInputPricePerToken = r.adjust("image_input", out.ImageInputPricePerToken)
	out.ImageCacheReadPricePerToken = r.adjust("image_read", out.ImageCacheReadPricePerToken)
	out.ImageOutputPricePerToken = r.adjust("image_output", out.ImageOutputPricePerToken)
	return &out
}

// appliesAt reports whether the request's market date falls in this price rule.
// An undated rule applies without a request time. Dated rules require a valid
// market timezone and use a closed start and an open end to avoid overlap.
func (r CatalogServicePrice) appliesAt(at time.Time) bool {
	if r.Before == "" && r.After == "" {
		return true
	}
	if at.IsZero() {
		return false
	}
	location, err := time.LoadLocation(r.Zone)
	if err != nil {
		return false
	}
	date := at.In(location).Format(time.DateOnly)
	return (r.After == "" || date >= r.After) && (r.Before == "" || date < r.Before)
}

// adjust applies this rule to one ordinary or media token price.
func (r CatalogServicePrice) adjust(key string, base float64) float64 {
	if value, ok := r.Amounts[key]; ok {
		return value
	}
	if value, ok := r.Factors[key]; ok {
		return base * value
	}
	switch key {
	case "cache_write_1h", "cache_write_30m", "cache_write_12h":
		if value, ok := r.Factors["cache_write"]; ok {
			return base * value
		}
	case "image_input", "image_output", "image_read":
		if r.Factor == nil {
			return base
		}
		return base * *r.Factor
	}
	if r.TextFactor != nil {
		return base * *r.TextFactor
	}
	if r.Factor != nil {
		return base * *r.Factor
	}
	return base
}

// adjustOptional retains an unknown rate unless the rule supplies its own amount.
func (r CatalogServicePrice) adjustOptional(key string, base *float64) *float64 {
	if base != nil {
		return new(r.adjust(key, *base))
	}
	if value, ok := r.Amounts[key]; ok {
		return new(value)
	}
	return nil
}

// hasCatalogServiceTier reports whether any compiled adjustment declares the tier.
func hasCatalogServiceTier(pricing *ModelPricing, tier string) bool {
	for _, row := range pricing.ServiceTierPrices {
		if row.Tier == tier {
			return true
		}
	}
	return false
}

// displayServiceTierQuote returns a service-tier quote for the current market
// date. Settlement uses the same catalog rule with the recorded request time.
// An unavailable tier is omitted from display instead of copying baseline rates.
func displayServiceTierQuote(pricing *ModelPricing, tier string, multiplier float64, at time.Time) *ServiceTierTokenQuote {
	selected, ok := catalogServiceTierPricing(pricing, tier, at)
	if !ok {
		if tier != "flex" || pricing.FlexMultiplier == nil {
			return nil
		}
		selected = MultiplyModelPricing(pricing, *pricing.FlexMultiplier)
	}
	selected = applyCatalogCalendarPricing(selected, at)
	short, long := displayOpenAIWriteRates(selected, multiplier)
	cacheWrite, cacheWrite1h := CacheCreationDisplayPrices(selected)
	return &ServiceTierTokenQuote{
		Input:         selected.InputPricePerToken * multiplier,
		Output:        selected.OutputPricePerToken * multiplier,
		CacheRead:     selected.CacheReadPricePerToken * multiplier,
		CacheWrite:    cacheWrite * multiplier,
		CacheWrite1h:  cacheWrite1h * multiplier,
		CacheWrite30m: short,
		CacheWrite12h: long,
		ImageInput:    selected.ImageInputPricePerToken * multiplier,
		ImageOutput:   selected.ImageOutputPricePerToken * multiplier,
	}
}
