package openai

import (
	"github.com/tidwall/gjson"
)

// forward_usage.go extracts and combines token observations from Responses,
// embeddings, and image results for forwarded accounting.
// Responses, embeddings, and image results can report usage in different field
// layouts. Forwarded accounting retains input, output, cache, and image counts
// separately, while each extraction path chooses which observations are
// authoritative before handing the value to completion pricing.
//
// CopyForwardUsage trusts the canonical Responses cache-write count, including
// zero, and reads cached input from its nested detail. Embeddings accepts
// positive prompt/input/total aliases in order and uses cache-detail precedence
// from the shared extractors. Additive accumulation differs from image-response
// merging: the latter updates selected positive counts and leaves other
// aggregate fields available from earlier observations. The helpers expose
// these different update rules so callers can distinguish independent usage
// contributions from repeated cumulative snapshots.

// ForwardUsage holds token, cache, and image observations for downstream accounting.
type ForwardUsage struct {
	ImageFrameCount          int `json:"-"`                                     // independently observed animation frame units
	InputTokens              int `json:"input_tokens"`                          // reported input tokens
	ImageInputTokens         int `json:"image_input_tokens,omitempty"`          // reported image-input tokens
	ImageCacheReadTokens     int `json:"image_cache_read_tokens,omitempty"`     // explicit cached image token subset
	OutputTokens             int `json:"output_tokens"`                         // reported output tokens
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"` // reported cache-write tokens
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`     // reported cache-read tokens
	ImageOutputTokens        int `json:"image_output_tokens,omitempty"`         // reported image-output tokens
}

// CopyForwardUsage copies Responses input, output, and canonical cache-write
// counts plus nested cached input when present. Nil input returns zero usage;
// image counters remain zero because this typed extraction does not populate them.
func CopyForwardUsage(usage *ResponsesUsage) ForwardUsage {
	if usage == nil {
		return ForwardUsage{}
	}
	result := ForwardUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.InputTokensDetails != nil {
		result.CacheReadInputTokens = usage.InputTokensDetails.CachedTokens
	}
	return result
}

// AddForwardUsage adds every supplied counter to a mutable destination.
// Nil destinations are ignored. Repeated cumulative snapshots add their counts
// again, so callers choose independent contributions and serialize destination
// updates before completion reads the aggregate.
func AddForwardUsage(dst *ForwardUsage, usage ForwardUsage) {
	if dst == nil {
		return
	}
	dst.InputTokens += usage.InputTokens
	dst.ImageInputTokens += usage.ImageInputTokens
	dst.ImageCacheReadTokens += usage.ImageCacheReadTokens
	dst.OutputTokens += usage.OutputTokens
	dst.CacheCreationInputTokens += usage.CacheCreationInputTokens
	dst.CacheReadInputTokens += usage.CacheReadInputTokens
	dst.ImageOutputTokens += usage.ImageOutputTokens
}

// ExtractEmbeddingsUsage reads a top-level usage object into forwarded counters.
// Missing or nonobject usage returns zero. Input uses the first positive
// prompt_tokens, input_tokens, or total_tokens value; output uses completion_tokens
// before output_tokens, independently of the input fallback.
//
// Shared cache extractors give present nested detail precedence over flat
// aliases, including a nested zero. Image-input detail uses the prompt spelling
// before input detail. Callers validate the complete body separately because
// these gjson path reads can expose usage from partially parsed JSON.
func ExtractEmbeddingsUsage(body []byte) ForwardUsage {
	usage := gjson.GetBytes(body, "usage")
	if !usage.Exists() || !usage.IsObject() {
		return ForwardUsage{}
	}
	inputTokens := FirstPositiveGJSONInt(
		usage.Get("prompt_tokens"),
		usage.Get("input_tokens"),
		usage.Get("total_tokens"),
	)
	outputTokens := FirstPositiveGJSONInt(
		usage.Get("completion_tokens"),
		usage.Get("output_tokens"),
	)
	cacheReadTokens := OpenAICacheReadTokensFromUsage(usage)
	cacheCreationTokens := OpenAICacheCreationTokensFromUsage(usage)
	// Image detail can accompany a prompt total in multimodal embeddings.
	// Keep its positive count available separately so completion can apply
	// image-input pricing.
	imageInputTokens := FirstPositiveGJSONInt(
		usage.Get("prompt_tokens_details.image_tokens"),
		usage.Get("input_tokens_details.image_tokens"),
	)
	return ForwardUsage{
		InputTokens:              inputTokens,
		ImageInputTokens:         imageInputTokens,
		ImageCacheReadTokens:     OpenAIImageCacheReadTokens(usage),
		OutputTokens:             outputTokens,
		CacheReadInputTokens:     cacheReadTokens,
		CacheCreationInputTokens: cacheCreationTokens,
	}
}

// MergeImageResponseUsage merges a JSON response observation into previously
// recorded image usage. Positive input, output, cache-read, and image counts
// replace their corresponding counters; nil destinations, invalid bodies,
// and absent usage leave the destination unchanged.
//
// The shared extractor selects one usage envelope before merging. Each selected
// nonpositive counter retains its earlier value, so an observation with a
// positive input count and zero output can combine with previously recorded
// output. Cache-write counts stay untouched. Callers serialize destination
// updates and distinguish this cumulative-observation rule from AddForwardUsage
// when accounting for independent contributions.
func MergeImageResponseUsage(dst *ForwardUsage, body []byte) {
	if dst == nil {
		return
	}
	// A selected snapshot can omit individual positive counters. Update those
	// it supplies so earlier image observations remain available in the other
	// destination buckets.
	if parsed, ok := ExtractNativeImageUsage(body); ok {
		if parsed.InputTokens > 0 {
			dst.InputTokens = parsed.InputTokens
		}
		if parsed.OutputTokens > 0 {
			dst.OutputTokens = parsed.OutputTokens
		}
		if parsed.CacheReadInputTokens > 0 {
			dst.CacheReadInputTokens = parsed.CacheReadInputTokens
			dst.ImageCacheReadTokens = parsed.ImageCacheReadTokens
		}
		if parsed.ImageInputTokens > 0 {
			dst.ImageInputTokens = parsed.ImageInputTokens
		}
		if parsed.ImageOutputTokens > 0 {
			dst.ImageOutputTokens = parsed.ImageOutputTokens
		}
	}
}

// ExtractNativeImageUsage reads usage from a native Images response or completion.
// Only native image readers may use this function: without output-token details,
// their reported output tokens describe image generation rather than text output.
// An explicit details object, including zero image tokens, remains authoritative.
//
// The same envelope precedence as ordinary usage extraction selects the detail
// object. This prevents a wrapper's absent details from changing the meaning of
// a nested authoritative record. Responses carrier usage must continue using
// its ordinary extractor because that model can produce text alongside images.
func ExtractNativeImageUsage(body []byte) (ForwardUsage, bool) {
	usage, ok := ExtractOpenAIUsageFromJSONBytes(body)
	if !ok {
		return usage, false
	}
	for _, path := range []string{"usage", "response.usage", "data.usage", "data.response.usage"} {
		value := gjson.GetBytes(body, path)
		if !value.IsObject() {
			continue
		}
		if !value.Get("output_tokens_details").Exists() && !value.Get("completion_tokens_details").Exists() {
			usage.ImageOutputTokens = max(usage.OutputTokens, 0)
		}
		break
	}
	return usage, true
}
