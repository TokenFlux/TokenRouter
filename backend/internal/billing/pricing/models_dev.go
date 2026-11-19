package pricing

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// models_dev.go converts models.dev entries into billing catalog price JSON.
// models.dev quotes prices in USD per million tokens, while settlement consumes
// USD per token. Conversion retains optional-value presence so a declared zero
// remains distinct from a missing text, cache, or Fast price, including within
// absolute context tiers.
//
// Tier amounts replace supplied buckets and inherit omitted buckets from the
// entry's base cost. Original Google Gemini image output is routed to the
// image-output token bucket only when its identity and output modalities meet
// that specific rule; text and thinking prices then require their own
// supplemental data. Other entries retain ordinary output token units, and
// provider labels are mapped for existing billing consumers.

// ModelsDevPrices converts each priced catalog identity into raw billing JSON
// fields. Callers supply a non-nil catalog and keep entries stable during
// conversion. Entries missing both input and output amounts are skipped;
// supplied zero amounts still produce records.
//
// Each absolute tier starts with base costs and replaces only its supplied
// input, output, and cache buckets. Threshold values and entry identities
// retain their catalog meaning for later parsing and billing conversion. JSON
// encoding failures leave nil entry bytes for the publication parser to
// diagnose rather than producing a partial valid price object.
func ModelsDevPrices(catalog *modelcatalog.Catalog) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for key, entry := range catalog.Entries {
		if entry.Cost.Input == nil && entry.Cost.Output == nil && entry.Cost.ImageInput == nil && entry.Cost.ImageOutput == nil && entry.Cost.PerImage == nil && len(entry.ImagePrices) == 0 && len(entry.VideoPrices) == 0 {
			continue
		}
		fields := modelsDevPriceFields(entry, entry.Cost)
		var tiers []map[string]any
		cost := entry.Cost
		for _, tier := range entry.Tiers {
			if !tier.Exclusive {
				cost = entry.Cost
			}
			if tier.Input != nil {
				cost.Input = tier.Input
			}
			if tier.Output != nil {
				cost.Output = tier.Output
			}
			if tier.CacheRead != nil {
				cost.CacheRead = tier.CacheRead
			}
			if tier.CacheWrite != nil {
				cost.CacheWrite = tier.CacheWrite
			}
			if tier.CacheWrite5m != nil || tier.CacheWrite1h != nil || tier.CacheWrite30m != nil || tier.CacheWrite12h != nil {
				cost.CacheWrite5m = tier.CacheWrite5m
				cost.CacheWrite1h = tier.CacheWrite1h
				cost.CacheWrite30m, cost.CacheWrite12h = tier.CacheWrite30m, tier.CacheWrite12h
			}
			tiers = append(tiers, map[string]any{"threshold": tier.Tier.Size, "exclusive": tier.Exclusive, "pricing": modelsDevPriceFields(entry, cost)})
		}
		if len(tiers) > 0 {
			fields["context_prices"] = tiers
		}
		// Encoding failures leave nil entry bytes, which catalog parsing rejects
		// before the candidate can become published prices.
		body, _ := json.Marshal(fields)
		result[key] = body
	}
	return result
}

// modelsDevPriceFields returns billing fields for one entry and selected cost
// record. Present amounts are converted from USD per million tokens to USD per
// token, retaining explicit zero and modality metadata. Provider labels are
// mapped to names already used by billing.
//
// Original Google Gemini image identities with image output modalities place
// output into the image-output token field. Other records use text output; Fast
// amounts are emitted only on that text-output path. The returned map contains
// supplied modality slices by reference until JSON encoding, so callers keep
// entry data stable.
func modelsDevPriceFields(entry modelcatalog.Entry, cost modelcatalog.Cost) map[string]any {
	provider := entry.Provider
	// Billing consumers use these established provider labels, so map catalog
	// spellings before deriving provider-sensitive pricing behavior.
	switch provider {
	case "google":
		provider = "gemini"
	case "moonshotai", "moonshotai-cn":
		provider = "moonshot"
	case "zai", "zhipuai":
		provider = "zhipu"
	}
	fields := map[string]any{"source": "models.dev", "provider": provider, "mode": "chat"}
	if len(entry.ImagePrices) > 0 {
		fields["image_prices"] = entry.ImagePrices
	}
	if len(entry.VideoPrices) > 0 {
		fields["video_prices"] = entry.VideoPrices
	}
	if cost.ImageOutput != nil && entry.Attributes.OutputOnly("image") ||
		cost.Input == nil && cost.Output == nil && (cost.PerImage != nil || len(entry.ImagePrices) > 0) {
		fields["mode"] = "image_generation"
	}
	if entry.Attributes.InputModalities != nil {
		fields["supported_modalities"] = *entry.Attributes.InputModalities
	}
	if entry.Attributes.OutputModalities != nil {
		fields["supported_output_modalities"] = *entry.Attributes.OutputModalities
	}
	// Original Google Gemini image output uses image-token units when its full
	// identity and output modalities qualify. Text and thinking prices remain
	// independent supplemental buckets.
	imageOutput := entry.FirstParty && entry.Provider == "google" && strings.HasPrefix(entry.Model, "gemini-") &&
		strings.Contains(entry.Model, "-image") && entry.Attributes.OutputModalities != nil &&
		slices.Contains(*entry.Attributes.OutputModalities, "image")
	put := func(key string, value *float64) {
		if value != nil {
			// Convert the catalog's million-token quote into settlement's per-token
			// unit.
			fields[key] = *value / 1e6
		}
	}
	put("input_cost_per_token", cost.Input)
	if imageOutput && cost.ImageOutput == nil {
		put("output_cost_per_image_token", cost.Output)
	} else {
		put("output_cost_per_token", cost.Output)
	}
	put("cache_read_input_token_cost", cost.CacheRead)
	put("cache_creation_input_token_cost", cost.CacheWrite)
	put("cache_creation_input_token_cost_5m", cost.CacheWrite5m)
	put("cache_creation_input_token_cost_above_1hr", cost.CacheWrite1h)
	put("cache_creation_input_token_cost_30m", cost.CacheWrite30m)
	put("cache_creation_input_token_cost_12h", cost.CacheWrite12h)
	put("input_cost_per_image_token", cost.ImageInput)
	put("cache_read_input_image_token_cost", cost.ImageRead)
	put("output_cost_per_image_token", cost.ImageOutput)
	put("output_cost_per_image", cost.PerImage)
	if len(entry.ServicePrices) > 0 {
		rules := normalizeCatalogServicePrices(entry.ServicePrices, entry.Cost)
		fields["service_tier_prices"] = rules
		for _, rule := range rules {
			if rule.Tier != "priority" || rule.Before != "" || rule.After != "" {
				continue
			}
			// Legacy consumers inspect the equivalent Fast rates for these buckets.
			// Compute those values from the selected context band as well.
			fast := map[string]*float64{
				"input": cost.Input, "output": cost.Output,
				"cache_read": cost.CacheRead, "cache_write": cost.CacheWrite,
			}
			for key, destination := range map[string]string{
				"input":       "input_cost_per_token_priority",
				"output":      "output_cost_per_token_priority",
				"cache_read":  "cache_read_input_token_cost_priority",
				"cache_write": "cache_creation_input_token_cost_priority",
			} {
				if amount := fast[key]; amount != nil {
					fields[destination] = rule.adjust(key, *amount/1e6)
				} else if amount, ok := rule.Amounts[key]; ok {
					fields[destination] = amount
				}
			}
			break
		}
		fields["supports_service_tier"] = true
	}
	fields["supports_prompt_caching"] = cost.CacheRead != nil || cost.CacheWrite != nil
	// These Fast fields represent text-token amounts; image-output conversion
	// omits them so text-tier pricing is not misassigned to image tokens.
	if entry.Fast != nil && !imageOutput {
		fields["supports_service_tier"] = true
		put("input_cost_per_token_priority", entry.Fast.Input)
		put("output_cost_per_token_priority", entry.Fast.Output)
		put("cache_read_input_token_cost_priority", entry.Fast.CacheRead)
		put("cache_creation_input_token_cost_priority", entry.Fast.CacheWrite)
	}
	return fields
}
