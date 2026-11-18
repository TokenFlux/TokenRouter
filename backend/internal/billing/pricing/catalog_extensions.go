package pricing

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"
)

// catalog_extensions.go validates supplemental catalog rules and derives
// missing cache prices.
// Catalog supplements can fill missing model prices with media amounts or token
// multipliers while retaining amounts already supplied, including zero.
// Operation defaults use a separate reserved record, with optional pointers
// preserving the difference between an unavailable operation price and free
// usage.
//
// CatalogRules and OperationPrices validate their supplied numeric fields
// before publication and clone mutable collections for snapshots. Cache
// derivation uses present input amounts and configured factors to fill missing
// standard, priority, and hourly cache buckets, marking their source as
// rule_supplement. The same factors can pass to nested context tiers when those
// tiers omit them. Derivation can modify an unpublished candidate before a
// later error, so publication callers discard failed candidates.

// BillingDefaultsKey is reserved for operation prices in local files, outside the model namespace.
const BillingDefaultsKey = "_billing_defaults"

// CatalogRules contains verified supplemental media prices and token pricing
// factors. FastMultiplier, FlexMultiplier, MaxReasoningEffortMultiplier,
// CacheWriteMultiplier, and CacheWrite1hMultiplier retain optional rule
// presence. Nil supplies no additional rule; catalog validation requires a
// finite positive value for each supplied factor.
type CatalogRules struct {
	ServiceTierPrices []CatalogServicePrice `json:"service_tier_prices,omitempty"` // directory tier adjustments
	// ImagePrices holds USD-per-image amounts under exact 1K, 2K, and 4K keys.
	// Present zero is free; absent dimensions stay unpriced during catalog lookup.
	// USD per image-size map
	ImagePrices MediaPrices `json:"image_prices,omitempty"`
	// VideoPrices holds USD-per-second amounts for canonical resolution labels.
	// Present zero is free; missing resolution keys retain unavailable status.
	// NormalizeVideoPrices validates custom labels and rejects alias collisions.
	// USD per video-resolution second map
	VideoPrices MediaPrices `json:"video_prices,omitempty"`
	// optional catalog Fast scaling factor
	FastMultiplier *float64 `json:"fast_multiplier,omitempty"`
	// optional catalog Flex scaling factor
	FlexMultiplier *float64 `json:"flex_multiplier,omitempty"`
	// optional max-effort token scaling factor
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier,omitempty"`
	// CacheWriteMultiplier derives missing standard and priority cache-write
	// prices from present input amounts. Already present cache amounts, including
	// zero, keep their supplied values.
	// input-to-cache-write derivation factor
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier,omitempty"`
	// CacheWrite1hMultiplier derives a missing hourly cache-write amount from
	// present standard input pricing. Existing hourly presence wins, including a
	// free value.
	// input-to-hourly-cache derivation factor
	CacheWrite1hMultiplier *float64 `json:"cache_write_1h_multiplier,omitempty"`
	// TimePricing holds a catalog daily token schedule. Validate checks its zone
	// and periods; callers load the zone before calculating its factor.
	// catalog daily token-price periods
	TimePricing *TimePricingConfig `json:"time_pricing,omitempty"`
	// pricing evidence source URL
	SourceURL string `json:"source_url,omitempty"`
	// price verification date text
	VerifiedAt string `json:"verified_at,omitempty"`
}

// OperationPrices contains optional USD prices and provenance for operation
// billing. WebSearchPricePerCall, SearchPricePer1k, AudioRealtimePricePerMin,
// AudioTTSPricePerMillionChars, and AudioSTTPricePerHour preserve availability:
// nil is missing, while a pointer to zero declares free usage in that operation
// unit.
type OperationPrices struct {
	// USD per web-search call
	WebSearchPricePerCall *float64 `json:"web_search_price_per_call,omitempty"`
	// USD per thousand search calls
	SearchPricePer1k *float64 `json:"search_price_per_1k,omitempty"`
	// USD per Realtime minute
	AudioRealtimePricePerMin *float64 `json:"audio_realtime_price_per_min,omitempty"`
	// USD per million TTS characters
	AudioTTSPricePerMillionChars *float64 `json:"audio_tts_price_per_million_chars,omitempty"`
	// USD per STT hour
	AudioSTTPricePerHour *float64 `json:"audio_stt_price_per_hour,omitempty"`
	// per-operation provenance label map
	Sources map[string]string `json:"sources,omitempty"`
	// operation-price verification date text
	VerifiedAt string `json:"verified_at,omitempty"`
}

// MediaPrices maps declared media dimensions to their USD unit amounts.
type MediaPrices map[string]float64

// copyOptional returns new top-level storage for the supplied optional value.
// Nil input remains nil. Nested references inside T retain their sharing, so
// callers clone those collections separately when an independent record is
// required.
func copyOptional[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Clone returns supplemental rules with copied media maps, amount pointers, and
// time periods. Nil optional values retain their absence. The cloned
// collections and supported pointer fields can be changed independently of the
// source rule.
func (r CatalogRules) Clone() CatalogRules {
	r.ImagePrices = maps.Clone(r.ImagePrices)
	r.VideoPrices = maps.Clone(r.VideoPrices)
	r.ServiceTierPrices = slices.Clone(r.ServiceTierPrices)
	for i := range r.ServiceTierPrices {
		r.ServiceTierPrices[i].Factor = copyOptional(r.ServiceTierPrices[i].Factor)
		r.ServiceTierPrices[i].TextFactor = copyOptional(r.ServiceTierPrices[i].TextFactor)
		r.ServiceTierPrices[i].Amounts = maps.Clone(r.ServiceTierPrices[i].Amounts)
		r.ServiceTierPrices[i].Factors = maps.Clone(r.ServiceTierPrices[i].Factors)
	}
	r.FastMultiplier = copyOptional(r.FastMultiplier)
	r.FlexMultiplier = copyOptional(r.FlexMultiplier)
	r.MaxReasoningEffortMultiplier = copyOptional(r.MaxReasoningEffortMultiplier)
	r.CacheWriteMultiplier = copyOptional(r.CacheWriteMultiplier)
	r.CacheWrite1hMultiplier = copyOptional(r.CacheWrite1hMultiplier)
	r.TimePricing = copyOptional(r.TimePricing)
	if r.TimePricing != nil {
		r.TimePricing.Periods = slices.Clone(r.TimePricing.Periods)
	}
	return r
}

// Validate checks media keys, finite amounts, positive factors, and any
// supplied time rule. Explicit media zero is valid; multiplier zero is
// rejected. Time rules also require an accepted, loadable zone, even when their
// periods are empty. Map-based checks report the first encountered invalid
// field.
func (r CatalogRules) Validate() error {
	if _, err := NormalizeVideoPrices(r.VideoPrices); err != nil {
		return err
	}
	for key, price := range r.ImagePrices {
		valid := key == "1K" || key == "2K" || key == "4K"
		if !valid || !validAmount(price) {
			return fmt.Errorf("invalid image_prices.%s", key)
		}
	}
	for name, value := range map[string]*float64{"fast_multiplier": r.FastMultiplier, "flex_multiplier": r.FlexMultiplier, "max_reasoning_effort_multiplier": r.MaxReasoningEffortMultiplier, "cache_write_multiplier": r.CacheWriteMultiplier, "cache_write_1h_multiplier": r.CacheWrite1hMultiplier} {
		if value != nil && (!validAmount(*value) || *value == 0) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	if r.TimePricing != nil {
		if err := ValidateTimezoneName(r.TimePricing.Timezone); err != nil {
			return err
		}
		if _, err := time.LoadLocation(r.TimePricing.Timezone); err != nil {
			return fmt.Errorf("invalid time_pricing.timezone: %w", err)
		}
		return ValidateTimePricingConfig(r.TimePricing)
	}
	return nil
}

// validAmount reports whether an amount is finite and nonnegative.
func validAmount(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Clone returns operation defaults with copied optional amounts and source
// labels. Nil prices remain absent and zero remains present. The result's
// amount storage and source map can be changed independently.
func (p OperationPrices) Clone() OperationPrices {
	p.WebSearchPricePerCall = copyOptional(p.WebSearchPricePerCall)
	p.SearchPricePer1k = copyOptional(p.SearchPricePer1k)
	p.AudioRealtimePricePerMin = copyOptional(p.AudioRealtimePricePerMin)
	p.AudioTTSPricePerMillionChars = copyOptional(p.AudioTTSPricePerMillionChars)
	p.AudioSTTPricePerHour = copyOptional(p.AudioSTTPricePerHour)
	p.Sources = maps.Clone(p.Sources)
	return p
}

// Validate rejects supplied operation prices that are negative or nonfinite.
// Omitted values and explicit zero are valid for partial records. Map iteration
// determines which invalid field is reported first when several fields fail.
func (p OperationPrices) Validate() error {
	for name, value := range map[string]*float64{"web_search_price_per_call": p.WebSearchPricePerCall, "search_price_per_1k": p.SearchPricePer1k, "audio_realtime_price_per_min": p.AudioRealtimePricePerMin, "audio_tts_price_per_million_chars": p.AudioTTSPricePerMillionChars, "audio_stt_price_per_hour": p.AudioSTTPricePerHour} {
		if value != nil && !validAmount(*value) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	return nil
}

// deriveCatalogCachePrices fills missing cache amounts from present input
// prices and factors. Existing amount presence wins even at zero, and missing
// input amounts cannot establish a derived cache price. Each accepted
// derivation sets the presence flag and records rule_supplement provenance.
//
// Context tiers inherit missing cache factors through copied pointers before
// the same derivation runs recursively. Callers keep these records unpublished
// during mutation: an invalid derived amount can return an error after earlier
// buckets or tiers were filled. A nil record succeeds without changes.
func deriveCatalogCachePrices(price *CatalogModelPricing) error {
	if price == nil {
		return nil
	}
	if price.PriceSources == nil {
		price.PriceSources = map[string]string{}
	}
	// Fill a cache bucket only from a present base and an omitted target,
	// preserving explicit zero prices from earlier layers.
	derive := func(base float64, basePresent bool, multiplier *float64, target *float64, present *bool, label string) error {
		if !basePresent || multiplier == nil || *present {
			return nil
		}
		value := base * *multiplier
		if !validAmount(value) {
			return fmt.Errorf("invalid derived %s", label)
		}
		*target, *present = value, true
		price.PriceSources[label] = "rule_supplement"
		return nil
	}
	if err := derive(price.InputCostPerToken, price.InputPricePresent, price.CacheWriteMultiplier, &price.CacheCreationInputTokenCost, &price.CacheCreationPricePresent, "cache_write"); err != nil {
		return err
	}
	if err := derive(price.InputCostPerTokenPriority, price.PriorityInputPresent || price.InputCostPerTokenPriority > 0, price.CacheWriteMultiplier, &price.CacheCreationInputTokenCostPriority, &price.PriorityCacheWritePresent, "priority_cache_write"); err != nil {
		return err
	}
	if err := derive(price.InputCostPerToken, price.InputPricePresent, price.CacheWrite1hMultiplier, &price.CacheCreationInputTokenCostAbove1hr, &price.CacheCreation1hPricePresent, "cache_write_1h"); err != nil {
		return err
	}
	// Missing tier factors inherit copied base factors before recursive
	// derivation, so each context tier uses its own input amount.
	for _, tier := range price.ContextPrices {
		if tier.Pricing == nil {
			continue
		}
		if tier.Pricing.CacheWriteMultiplier == nil {
			tier.Pricing.CacheWriteMultiplier = copyOptional(price.CacheWriteMultiplier)
		}
		if tier.Pricing.CacheWrite1hMultiplier == nil {
			tier.Pricing.CacheWrite1hMultiplier = copyOptional(price.CacheWrite1hMultiplier)
		}
		if err := deriveCatalogCachePrices(tier.Pricing); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalJSON decodes a media amount map while retaining numeric zero and
// rejecting null-valued entries. The receiver must be non-nil and owned by the
// caller during decoding. A top-level null sets the map to nil; malformed JSON
// or a null entry returns an error before replacing a prior map.
//
// A successfully built numeric map is assigned after every entry is examined.
// Dimension spelling and nonnegative finite amounts are checked separately by
// CatalogRules.Validate, so decoding alone does not establish a published valid
// media-price record.
func (p *MediaPrices) UnmarshalJSON(body []byte) error {
	var values map[string]*float64
	if err := json.Unmarshal(body, &values); err != nil {
		return err
	}
	if values == nil {
		*p = nil
		return nil
	}
	result := make(MediaPrices, len(values))
	for key, value := range values {
		// A null map value carries no numeric price; rejecting it keeps an unknown
		// dimension from becoming free by default.
		if value == nil {
			return fmt.Errorf("media price %s must be a number", key)
		}
		result[key] = *value
	}
	*p = result
	return nil
}
