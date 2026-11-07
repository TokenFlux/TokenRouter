package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// catalog_parse.go decodes catalog entries into price records and validation
// diagnostics.
// A catalog entry can contain model prices, descriptive metadata, or the
// reserved operation defaults. Price parsing retains supplied zero amounts as
// known prices and records malformed entries so the publication loader can
// reject an entire candidate while keeping its previous catalog.
//
// DecodeCatalogEntries normalizes the legacy provider field before merging
// layers, with present provider fields taking precedence even when empty or
// null. ParsePricingEntries then validates amounts and supplemental rules,
// converts optional values into amounts plus presence flags, and derives cache
// or long-context rules. Its usable-price map can coexist with invalid-entry
// diagnostics, so callers inspect both the returned error and ValidationError
// before publishing results.

// ErrNoPricingEntries reports an empty usable-price map; loaders still inspect
// invalid-entry diagnostics before accepting a catalog candidate.
var ErrNoPricingEntries = errors.New("no valid pricing entries found")

// CatalogDiagnostics describes parsing issues for the provider to report.
type CatalogDiagnostics struct {
	Skipped        int      // entry decoding failure count
	InvalidEntries []string // sorted publication rejection messages
	// OrphanCacheTiers identifies models with positive cache above-price keys
	// lacking an accepted base. LopsidedLadders identifies models with a context
	// surcharge increasing exactly one text side; after successful validation the
	// loader reports both lists as warnings.
	// OrphanCacheTiers: model-qualified orphan cache keys.
	// LopsidedLadders: model-qualified one-sided text multipliers.
	OrphanCacheTiers,
	LopsidedLadders []string
}

// ValidationError returns one error containing every invalid-entry diagnostic.
// No invalid entries returns nil; skipped counts and warning lists alone
// produce no error. Publication callers use this check alongside
// ParsePricingEntries' ordinary error.
func (d CatalogDiagnostics) ValidationError() error {
	if len(d.InvalidEntries) == 0 {
		return nil
	}
	return fmt.Errorf("invalid pricing entries: %s", strings.Join(d.InvalidEntries, "; "))
}

// DecodeCatalogEntries returns decoded model objects with legacy provider
// fields normalized. It requires a non-null top-level JSON object, skips
// sample_spec, and rejects any remaining non-object or malformed entry. The
// returned map and normalized bytes belong to this call.
func DecodeCatalogEntries(body []byte) (map[string]json.RawMessage, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("expected a pricing JSON object")
	}
	for name, entry := range entries {
		// The legacy format's sample_spec describes fields; removing that reserved
		// object keeps documentation separate from entries that require price-object
		// validation.
		if name == "sample_spec" {
			delete(entries, name)
			continue
		}
		normalized, err := normalizeCatalogEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid pricing entry %s: %w", name, err)
		}
		entries[name] = normalized
	}
	return entries, nil
}

// normalizeCatalogEntry normalizes provider identity in one catalog object.
// It rejects malformed JSON and null objects, while objects lacking the legacy
// field return their supplied bytes. Callers retain those returned bytes as
// read-only entry data until their catalog layer has been consumed.
//
// A present provider key wins even when its value is empty or null. The legacy
// value fills only an absent current key, then its old spelling is removed
// before re-encoding. Checking presence keeps an intentional empty provider
// from acquiring a different identity during layer merging.
func normalizeCatalogEntry(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("expected an object")
	}
	legacy, exists := fields["litellm_provider"]
	if !exists {
		return raw, nil
	}
	// A present current key owns provider identity even when empty or null; the
	// legacy spelling only fills an absent current key.
	if _, present := fields["provider"]; !present {
		fields["provider"] = legacy
	}
	delete(fields, "litellm_provider")
	return json.Marshal(fields)
}

// ParsePricingEntries returns usable prices, validation diagnostics, and an
// empty-price error when necessary. Malformed entries and invalid amounts or
// rules are recorded separately, so a nonempty result can still require
// publication rejection. Callers inspect diagnostics.ValidationError even after
// a nil ordinary error.
//
// Price pointers distinguish absent fields from free amounts before conversion,
// with stricter two-sided text pricing for models.dev entries. Cache rules fill
// absent buckets, and supplied long-context fields replace automatic absolute
// tiers. Metadata-only entries and reserved operation defaults stay outside the
// model price map, while warning lists describe cache and one-sided ladder
// conditions.
func ParsePricingEntries(rawData map[string]json.RawMessage) (map[string]*CatalogModelPricing, CatalogDiagnostics, error) {
	result := make(map[string]*CatalogModelPricing)
	skipped := 0
	var invalidEntries []string
	var orphanCacheTiers, lopsidedLadders []string

	for modelName, rawEntry := range rawData {
		// The legacy sample_spec object describes the input format; excluding it
		// keeps that documentation record out of model price lookup and validation.
		if modelName == "sample_spec" {
			continue
		}

		// Normalize the legacy provider spelling before decoding so both input
		// formats populate the same provider field and retain current-key precedence.
		normalized, decodeErr := normalizeCatalogEntry(rawEntry)
		var entry CatalogRawEntry
		if decodeErr == nil {
			decodeErr = json.Unmarshal(normalized, &entry)
		}
		// Record this malformed entry and continue collecting candidate diagnostics;
		// the publication loader rejects invalid entries even when other models are
		// usable.
		if decodeErr != nil {
			skipped++
			invalidEntries = append(invalidEntries, fmt.Sprintf("%s: %v", modelName, decodeErr))
			continue
		}

		// Reflect over direct optional amount fields so invalid supplied values are
		// reported with their JSON field names while explicit zero remains priced.
		invalidAmount := false
		value := reflect.ValueOf(entry)
		fields := value.Type()
		for i := range value.NumField() {
			field := value.Field(i)
			if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Float64 && !field.IsNil() && !validAmount(field.Elem().Float()) {
				invalidEntries = append(invalidEntries, fmt.Sprintf("%s: invalid %s", modelName, fields.Field(i).Tag.Get("json")))
				invalidAmount = true
			}
		}
		if invalidAmount {
			continue
		}
		// Supplemental rules are validated even on metadata-only or defaults records,
		// so invalid candidate fields cannot escape publication checks by omitting a
		// base price.
		if err := entry.Validate(); err != nil {
			invalidEntries = append(invalidEntries, fmt.Sprintf("%s: %v", modelName, err))
			continue
		}
		// Validation has rejected alias collisions; store canonical keys so all
		// consumers observe the same video dimensions in the published catalog.
		entry.VideoPrices, _ = NormalizeVideoPrices(entry.VideoPrices)
		// Reserved operation defaults need their own amount validation before
		// publication, but they remain outside model lookup even when valid.
		if modelName == BillingDefaultsKey {
			var defaults OperationPrices
			if err := json.Unmarshal(rawEntry, &defaults); err != nil {
				invalidEntries = append(invalidEntries, fmt.Sprintf("%s: %v", modelName, err))
			} else if err := defaults.Validate(); err != nil {
				invalidEntries = append(invalidEntries, fmt.Sprintf("%s: %v", modelName, err))
			}
			continue
		}
		// Metadata and cache-only patches do not establish a model price here; retain
		// them outside the usable-price map unless token or media amounts exist.
		if entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil && entry.OutputCostPerImage == nil && entry.OutputCostPerImageToken == nil && entry.InputCostPerImageToken == nil && len(entry.ImagePrices) == 0 && len(entry.VideoPrices) == 0 {
			continue
		}

		pricing := &CatalogModelPricing{
			InputPricePresent:           entry.InputCostPerToken != nil,
			OutputPricePresent:          entry.OutputCostPerToken != nil,
			CatalogRules:                entry.Clone(),
			CacheCreation1hPricePresent: entry.CacheCreationInputTokenCostAbove1hr != nil,
			PriorityInputPresent:        entry.Source != "" && entry.InputCostPerTokenPriority != nil,
			PriorityOutputPresent:       entry.Source != "" && entry.OutputCostPerTokenPriority != nil,
			PriorityCacheReadPresent:    entry.Source != "" && entry.CacheReadInputTokenCostPriority != nil,
			PriorityCacheWritePresent:   entry.Source != "" && entry.CacheCreationInputTokenCostPriority != nil,
			Source:                      entry.Source,
			PriceSources:                entry.PriceSources,
			ContextPrices:               entry.ContextPrices,
			CacheCreationPricePresent:   entry.CacheCreationInputTokenCost != nil,
			CacheReadPricePresent:       entry.CacheReadInputTokenCost != nil,
			ImageCacheReadPricePresent:  entry.CacheReadInputImageTokenCost != nil,
			ImagePricePresent:           entry.OutputCostPerImage != nil,
			ImageInputPricePresent:      entry.InputCostPerImageToken != nil,
			ImageOutputPricePresent:     entry.OutputCostPerImageToken != nil,
			Provider:                    entry.Provider,
			Mode:                        entry.Mode,
			SupportsPromptCaching:       entry.SupportsPromptCaching,
			SupportsServiceTier:         entry.SupportsServiceTier,
			SupportedModalities:         entry.SupportedModalities,
			SupportedOutputModalities:   entry.SupportedOutputModalities,
			SupportsVision:              entry.SupportsVision,
			SupportsAudioInput:          entry.SupportsAudioInput,
			SupportsAudioOutput:         entry.SupportsAudioOutput,
			SupportsVideoInput:          entry.SupportsVideoInput,
			TokenPricingAbsent:          entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil,
		}
		// models.dev publishes independent text buckets; an absent side keeps text
		// settlement unavailable while separately declared media amounts remain usable.
		if entry.Source == "models.dev" && (entry.InputCostPerToken == nil || entry.OutputCostPerToken == nil) {
			pricing.TokenPricingAbsent = true
		}
		// Legacy files may use supported_input_modalities. Use that list when the
		// preferred list is empty so compatibility modality lookup can still identify inputs.
		if len(pricing.SupportedModalities) == 0 {
			pricing.SupportedModalities = entry.SupportedInputModalities
		}

		if entry.InputCostPerToken != nil {
			pricing.InputCostPerToken = *entry.InputCostPerToken
		}
		if entry.InputCostPerTokenPriority != nil {
			pricing.InputCostPerTokenPriority = *entry.InputCostPerTokenPriority
		}
		if entry.OutputCostPerToken != nil {
			pricing.OutputCostPerToken = *entry.OutputCostPerToken
		}
		if entry.OutputCostPerTokenPriority != nil {
			pricing.OutputCostPerTokenPriority = *entry.OutputCostPerTokenPriority
		}
		if entry.CacheCreationInputTokenCost != nil {
			pricing.CacheCreationInputTokenCost = *entry.CacheCreationInputTokenCost
		}
		if entry.CacheCreationInputTokenCostPriority != nil {
			pricing.CacheCreationInputTokenCostPriority = *entry.CacheCreationInputTokenCostPriority
		}
		if entry.CacheCreationInputTokenCostAbove1hr != nil {
			pricing.CacheCreationInputTokenCostAbove1hr = *entry.CacheCreationInputTokenCostAbove1hr
		}
		if entry.CacheReadInputImageTokenCost != nil {
			pricing.CacheReadInputImageTokenCost = *entry.CacheReadInputImageTokenCost
		}
		if entry.CacheReadInputTokenCost != nil {
			pricing.CacheReadInputTokenCost = *entry.CacheReadInputTokenCost
		}
		if entry.CacheReadInputTokenCostPriority != nil {
			pricing.CacheReadInputTokenCostPriority = *entry.CacheReadInputTokenCostPriority
		}
		if entry.LongContextInputTokenThreshold != nil {
			pricing.LongContextInputTokenThreshold = *entry.LongContextInputTokenThreshold
		}
		if entry.LongContextInputCostMultiplier != nil {
			pricing.LongContextInputCostMultiplier = *entry.LongContextInputCostMultiplier
		}
		if entry.LongContextOutputCostMultiplier != nil {
			pricing.LongContextOutputCostMultiplier = *entry.LongContextOutputCostMultiplier
		}
		if entry.OutputCostPerImage != nil {
			pricing.OutputCostPerImage = *entry.OutputCostPerImage
		}
		if entry.OutputCostPerImageToken != nil {
			pricing.OutputCostPerImageToken = *entry.OutputCostPerImageToken
		}
		if entry.InputCostPerImageToken != nil {
			pricing.InputCostPerImageToken = *entry.InputCostPerImageToken
		}

		if err := deriveCatalogCachePrices(pricing); err != nil {
			invalidEntries = append(invalidEntries, fmt.Sprintf("%s: %v", modelName, err))
			continue
		}
		// Supplied long-context field presence, including zero, selects the
		// administrator's threshold rule before automatic catalog tiers or
		// above-price inference can apply.
		hasExplicitLongContext := entry.LongContextInputTokenThreshold != nil ||
			entry.LongContextInputCostMultiplier != nil ||
			entry.LongContextOutputCostMultiplier != nil
		if hasExplicitLongContext {
			// A supplied long-context field chooses the threshold-and-multiplier rule.
			// Clear absolute tiers so their selection cannot override that supplied choice.
			pricing.ContextPrices = nil
		} else {
			DeriveLongContextFromAboveTierFields(rawEntry, pricing)
			if IsLopsidedLongContextLadder(pricing) {
				lopsidedLadders = append(lopsidedLadders, fmt.Sprintf("%s(input x%.2f, output x%.2f)", modelName,
					pricing.LongContextInputCostMultiplier, pricing.LongContextOutputCostMultiplier))
			}
		}
		if orphans := OrphanCacheTierFields(rawEntry); len(orphans) > 0 {
			orphanCacheTiers = append(orphanCacheTiers, modelName+"("+strings.Join(orphans, ",")+")")
		}

		result[modelName] = pricing
	}

	sort.Strings(invalidEntries)
	diagnostics := CatalogDiagnostics{Skipped: skipped, InvalidEntries: invalidEntries, OrphanCacheTiers: orphanCacheTiers, LopsidedLadders: lopsidedLadders}

	if len(result) == 0 {
		return nil, diagnostics, ErrNoPricingEntries
	}

	return result, diagnostics, nil
}
