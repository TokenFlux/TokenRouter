package pricing

import (
	"encoding/json"
	"maps"
	"slices"
)

// catalog_types.go defines catalog representations, clones price records and
// decodes nested context prices.
// Catalog JSON uses optional numeric fields, while calculation records mostly
// use numeric amounts. Parsing carries presence flags alongside those amounts
// so supplied zero can remain a free price and an absent text bucket can keep
// token settlement unavailable.
//
// CatalogRawEntry represents the decoded values before conversion, and
// CatalogModelPricing retains price provenance, legacy capability data,
// supplemental rules, and nested absolute context tiers. CloneCatalogPrice
// copies those supported collections and nested records for mutable snapshots.
// CatalogContextPrice decoding delegates nested objects to the same parser and
// diagnostic checks as top-level entries, updating its receiver only after a
// usable nested price has been obtained.

// CatalogModelPricing contains catalog USD amounts, presence markers, and
// compatibility metadata. PriorityInputPresent, PriorityOutputPresent,
// PriorityCacheReadPresent, and PriorityCacheWritePresent require both a source
// label and a supplied amount in ParsePricingEntries; positive legacy amounts
// can still select priority prices without these markers.
type CatalogModelPricing struct {
	CatalogRules            // supplemental media and modifier rules
	InputPricePresent  bool `json:"-"` // input amount presence flag
	OutputPricePresent bool `json:"-"` // text-output amount presence flag
	// CacheCreation1hPricePresent records a supplied hourly cache amount,
	// including zero. ResolveModelPricing uses it to enable TTL detail pricing
	// independently of positive legacy amounts.
	// hourly cache amount supplied
	CacheCreation1hPricePresent bool `json:"-"`
	// sourced priority input amount supplied
	PriorityInputPresent bool `json:"-"`
	// sourced priority output amount supplied
	PriorityOutputPresent bool `json:"-"`
	// sourced priority cache-read amount supplied
	PriorityCacheReadPresent bool `json:"-"`
	// sourced priority cache-write amount supplied
	PriorityCacheWritePresent bool `json:"-"`
	// catalog entry provenance label
	Source string `json:"source,omitempty"`
	// PriceSources records per-bucket provenance for administrative default-price
	// display. CloneCatalogPrice copies the map, while unpublished derivation can
	// add rule_supplement entries.
	// per-bucket price provenance map
	PriceSources map[string]string `json:"price_sources,omitempty"`
	// ContextPrices holds nested absolute token tiers. ResolveModelPricing
	// converts supplied thresholds and records; callers prepare a stable ordered
	// tree before calculation or display.
	// absolute nested context-price tiers
	ContextPrices []CatalogContextPrice `json:"context_prices,omitempty"`
	// short-TTL cache amount supplied
	CacheCreationPricePresent bool `json:"-"`
	CacheReadPricePresent     bool `json:"-"` // cache-read amount presence flag
	ImagePricePresent         bool `json:"-"` // flat image amount supplied
	// image-input token amount supplied
	ImageInputPricePresent bool `json:"-"`
	// image-output token amount supplied
	ImageOutputPricePresent bool `json:"-"`
	// USD per standard input token
	InputCostPerToken float64 `json:"input_cost_per_token"`
	// USD per priority input token
	InputCostPerTokenPriority float64 `json:"input_cost_per_token_priority"`
	// USD per standard output token
	OutputCostPerToken float64 `json:"output_cost_per_token"`
	// USD per priority output token
	OutputCostPerTokenPriority float64 `json:"output_cost_per_token_priority"`
	// USD per short-TTL cache-write token
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
	// USD per priority cache-write token
	CacheCreationInputTokenCostPriority float64 `json:"cache_creation_input_token_cost_priority"`
	// USD per hourly cache-write token
	CacheCreationInputTokenCostAbove1hr float64 `json:"cache_creation_input_token_cost_above_1hr"`
	// USD per standard cache-read token
	CacheReadInputTokenCost      float64 `json:"cache_read_input_token_cost"`
	ImageCacheReadPricePresent   bool    `json:"-"`                                 // image cache amount supplied
	CacheReadInputImageTokenCost float64 `json:"cache_read_input_image_token_cost"` // USD per cached image token
	// USD per priority cache-read token
	CacheReadInputTokenCostPriority float64 `json:"cache_read_input_token_cost_priority"`
	// aggregate input-context token threshold
	LongContextInputTokenThreshold int `json:"long_context_input_token_threshold,omitempty"`
	// long-context input-side amount factor
	LongContextInputCostMultiplier float64 `json:"long_context_input_cost_multiplier,omitempty"`
	// long-context output-side amount factor
	LongContextOutputCostMultiplier float64 `json:"long_context_output_cost_multiplier,omitempty"`
	// catalog service-tier capability flag
	SupportsServiceTier bool   `json:"supports_service_tier"`
	Provider            string `json:"provider"` // catalog billing provider label
	Mode                string `json:"mode"`     // legacy catalog operation-mode label
	// catalog prompt-caching capability flag
	SupportsPromptCaching bool `json:"supports_prompt_caching"`
	// USD per generated output image
	OutputCostPerImage float64 `json:"output_cost_per_image"`
	// USD per image-output token
	OutputCostPerImageToken float64 `json:"output_cost_per_image_token"`
	// USD per image-input token
	InputCostPerImageToken float64 `json:"input_cost_per_image_token"`

	// SupportedModalities supplies preferred legacy input labels.
	// ParsePricingEntries uses SupportedInputModalities when this list is empty,
	// and compatibility display may derive additional labels.
	// preferred input modality labels
	SupportedModalities []string `json:"supported_modalities"`
	// supplied output modality labels
	SupportedOutputModalities []string `json:"supported_output_modalities"`
	// legacy image-input support flag
	SupportsVision bool `json:"supports_vision"`
	// legacy audio-input support flag
	SupportsAudioInput bool `json:"supports_audio_input"`
	// legacy audio-output support flag
	SupportsAudioOutput bool `json:"supports_audio_output"`
	// legacy video-input support flag
	SupportsVideoInput bool `json:"supports_video_input"`

	// TokenPricingAbsent keeps token settlement unavailable when required text
	// amounts are missing. models.dev requires both input and output; legacy data
	// accepts either, while independent media prices remain queryable.
	TokenPricingAbsent bool `json:"-"` // missing text-token pricing marker
}

// CatalogRawEntry represents decoded optional amounts before catalog price
// conversion. InputCostPerToken, InputCostPerTokenPriority, OutputCostPerToken,
// OutputCostPerTokenPriority, CacheCreationInputTokenCost,
// CacheCreationInputTokenCostPriority, CacheCreationInputTokenCostAbove1hr,
// CacheReadInputTokenCost, CacheReadInputTokenCostPriority, OutputCostPerImage,
// OutputCostPerImageToken, and InputCostPerImageToken retain pointer presence:
// nil means absent or JSON null and zero means a supplied free amount.
//
// LongContextInputTokenThreshold, LongContextInputCostMultiplier, and
// LongContextOutputCostMultiplier also retain presence, but select threshold
// rules. Any supplied one, including zero, makes ParsePricingEntries clear
// automatic absolute tiers before resolution.
type CatalogRawEntry struct {
	CatalogRules        // optional supplemental catalog price rules
	Source       string `json:"source,omitempty"` // source provenance label text
	// per-bucket source label map
	PriceSources map[string]string `json:"price_sources,omitempty"`
	// decoded absolute context-price tiers
	ContextPrices []CatalogContextPrice `json:"context_prices,omitempty"`
	// USD per standard input token
	InputCostPerToken *float64 `json:"input_cost_per_token"`
	// USD per priority input token
	InputCostPerTokenPriority *float64 `json:"input_cost_per_token_priority"`
	// USD per standard output token
	OutputCostPerToken *float64 `json:"output_cost_per_token"`
	// USD per priority output token
	OutputCostPerTokenPriority *float64 `json:"output_cost_per_token_priority"`
	// USD per short-TTL cache-write token
	CacheCreationInputTokenCost *float64 `json:"cache_creation_input_token_cost"`
	// USD per priority cache-write token
	CacheCreationInputTokenCostPriority *float64 `json:"cache_creation_input_token_cost_priority"`
	// USD per hourly cache-write token
	CacheCreationInputTokenCostAbove1hr *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	// USD per standard cache-read token
	CacheReadInputTokenCost      *float64 `json:"cache_read_input_token_cost"`
	CacheReadInputImageTokenCost *float64 `json:"cache_read_input_image_token_cost"` // optional USD per cached image token
	// USD per priority cache-read token
	CacheReadInputTokenCostPriority *float64 `json:"cache_read_input_token_cost_priority"`
	// aggregate input-context token threshold
	LongContextInputTokenThreshold *int `json:"long_context_input_token_threshold"`
	// long-context input-side amount factor
	LongContextInputCostMultiplier *float64 `json:"long_context_input_cost_multiplier"`
	// long-context output-side amount factor
	LongContextOutputCostMultiplier *float64 `json:"long_context_output_cost_multiplier"`
	// catalog service-tier capability flag
	SupportsServiceTier bool   `json:"supports_service_tier"`
	Provider            string `json:"provider"` // catalog billing provider label
	Mode                string `json:"mode"`     // legacy catalog operation-mode label
	// catalog prompt-caching capability flag
	SupportsPromptCaching bool `json:"supports_prompt_caching"`
	// USD per generated output image
	OutputCostPerImage *float64 `json:"output_cost_per_image"`
	// USD per image-output token
	OutputCostPerImageToken *float64 `json:"output_cost_per_image_token"`
	// USD per image-input token
	InputCostPerImageToken *float64 `json:"input_cost_per_image_token"`
	// preferred input modality labels
	SupportedModalities []string `json:"supported_modalities"`
	// SupportedInputModalities is the alternate legacy input list.
	// ParsePricingEntries uses it when SupportedModalities has no entries,
	// retaining supplied compatibility modality information.
	// alternate input modality labels
	SupportedInputModalities []string `json:"supported_input_modalities"`
	// supplied output modality labels
	SupportedOutputModalities []string `json:"supported_output_modalities"`
	// legacy image-input support flag
	SupportsVision bool `json:"supports_vision"`
	// legacy audio-input support flag
	SupportsAudioInput bool `json:"supports_audio_input"`
	// legacy audio-output support flag
	SupportsAudioOutput bool `json:"supports_audio_output"`
	// legacy video-input support flag
	SupportsVideoInput bool `json:"supports_video_input"`
}

// CatalogContextPrice holds nested absolute catalog prices and their original
// context threshold. Provider-specific inclusive behavior is interpreted during
// ResolveModelPricing conversion, while this representation keeps the supplied
// catalog threshold.
type CatalogContextPrice struct {
	// catalog input-context token threshold
	Threshold int `json:"threshold"`
	// Pricing holds nested catalog amounts interpreted through ParsePricingEntries
	// during JSON decoding. ResolveModelPricing later converts provider-specific
	// threshold behavior before calculation.
	Pricing *CatalogModelPricing `json:"pricing"` // nested absolute catalog amounts
}

// CloneCatalogPrice returns a recursive copy of supported catalog prices and
// mutable metadata. Nil input returns nil. Media maps, optional rule amounts,
// time periods, modality slices, provenance maps, and nested context records
// receive separate storage; scalar amounts and presence flags retain their
// values.
func CloneCatalogPrice(value *CatalogModelPricing) *CatalogModelPricing {
	if value == nil {
		return nil
	}
	result := *value
	result.CatalogRules = value.Clone()
	result.SupportedModalities = slices.Clone(value.SupportedModalities)
	result.SupportedOutputModalities = slices.Clone(value.SupportedOutputModalities)
	if value.PriceSources != nil {
		result.PriceSources = make(map[string]string, len(value.PriceSources))
		maps.Copy(result.PriceSources, value.PriceSources)
	}
	if value.ContextPrices != nil {
		result.ContextPrices = make([]CatalogContextPrice, len(value.ContextPrices))
	}
	for i, tier := range value.ContextPrices {
		result.ContextPrices[i] = CatalogContextPrice{Threshold: tier.Threshold, Pricing: CloneCatalogPrice(tier.Pricing)}
	}
	return &result
}

// UnmarshalJSON decodes a context object through the ordinary catalog price
// parser. The receiver must be non-nil and owned by the caller during decoding.
// Malformed nested data, invalid-entry diagnostics, or missing usable prices
// return an error, leaving the previous fields unchanged.
//
// Only successful parsing assigns the decoded Threshold and selected Pricing.
// The nested parser retains absent versus zero price information and validates
// its supplemental rules; this decoder itself does not validate threshold
// positivity or ordering among neighboring tiers.
func (c *CatalogContextPrice) UnmarshalJSON(body []byte) error {
	var raw struct {
		Threshold int             `json:"threshold"` // decoded context-token threshold
		Pricing   json.RawMessage `json:"pricing"`   // raw nested catalog price object
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	entries, diagnostics, err := ParsePricingEntries(map[string]json.RawMessage{"tier": raw.Pricing})
	if validationErr := diagnostics.ValidationError(); validationErr != nil {
		return validationErr
	}
	if err != nil {
		return err
	}
	c.Threshold, c.Pricing = raw.Threshold, entries["tier"]
	return nil
}
