package protocol

// usage.go represents token and audio usage and reconciles cache-duration
// categories and independently reported reasoning tokens.
// Token and audio usage feed different pricing dimensions after upstream
// execution. Audio quantities depend on their processing mode, while cache
// writes can carry both an aggregate token count and a TTL-specific breakdown
// used by separate five-minute and one-hour prices.
//
// Reclassifying cache TTL moves the category counts while retaining other
// usage fields, including the aggregate. A positive aggregate takes precedence
// over partial or contradictory detail counts so forced TTL reclassification
// neither drops untagged delta writes nor doubles duplicate buckets, while
// detail-only projections fall back to the detail sum and filling absent
// five-minute detail alone does not report a reclassification. Independently
// reported reasoning tokens need another overlap rule: add only the observed
// gap between the total and existing input/output, capped by the reported
// reasoning count, so inclusive output totals are not counted again.

// AudioUsage records the processing mode and quantity consumed by audio pricing.
type AudioUsage struct {
	// Mode selects DurationOrUnits' unit in CalculateAudioCost: realtime uses
	// minutes, tts uses millions of characters, and stt uses hours. The producer
	// computes the quantity before passing both fields to pricing.
	Mode            string  // audio processing mode
	DurationOrUnits float64 // mode-specific billable quantity
}

// TokenUsage holds input, output, and cache token counts.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`                // reported input tokens
	OutputTokens             int `json:"output_tokens"`               // reported output tokens
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"` // aggregate cache-write tokens
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`     // reported cache-read tokens
	// CacheCreation5mTokens and CacheCreation1hTokens hold TTL details beneath
	// CacheCreationInputTokens. ApplyCacheTTLOverride assigns a positive aggregate
	// (or the nonnegative detail sum when the aggregate is omitted) to the chosen
	// bucket while leaving the aggregate field unchanged.
	CacheCreation5mTokens int // five-minute cache-write tokens
	CacheCreation1hTokens int // one-hour cache-write tokens
	ImageOutputTokens     int `json:"image_output_tokens,omitempty"` // generated image tokens
	// Speed records Claude's processing mode. Completion's ClaudeServiceTier
	// maps a trimmed, case-insensitive "fast" value to the internal priority billing tier.
	Speed string `json:"speed,omitempty"`
}

// ApplyCacheTTLOverride moves cache-write detail into the requested TTL bucket
// on a nonnil usage value. The exact target "1h" selects one hour; every other
// value selects five minutes, including differently cased or padded targets.
//
// When both detail buckets are zero, a positive aggregate first fills the
// five-minute bucket, and that initialization alone returns false. A positive
// CacheCreationInputTokens value determines the reclassified total so partial
// stream details and duplicate upstream buckets cannot undercount or double the
// override; nonpositive aggregates fall back to the nonnegative detail sum for
// nested JSON rewrites that project only the detail object. The aggregate and
// unrelated usage fields remain unchanged, and callers serialize this in-place
// reclassification before pricing consumes the TTL categories.
func ApplyCacheTTLOverride(usage *TokenUsage, target string) bool {
	// Older observations can provide only the aggregate cache-write count.
	// Fill the default five-minute detail so TTL pricing can classify that known quantity.
	if usage.CacheCreation5mTokens == 0 && usage.CacheCreation1hTokens == 0 && usage.CacheCreationInputTokens > 0 {
		usage.CacheCreation5mTokens = usage.CacheCreationInputTokens
	}

	total := usage.CacheCreationInputTokens
	if total <= 0 {
		// Nested SSE cache_creation rewrites inspect only the detail object without
		// copying the outer aggregate; preserve their nonnegative detail sum here.
		total = max(usage.CacheCreation5mTokens, 0) + max(usage.CacheCreation1hTokens, 0)
	}
	if total <= 0 {
		return false
	}
	switch target {
	case "1h":
		if usage.CacheCreation1hTokens == total && usage.CacheCreation5mTokens == 0 {
			return false // already entirely in the one-hour bucket
		}
		usage.CacheCreation1hTokens = total
		usage.CacheCreation5mTokens = 0
	default: // "5m"
		// Unrecognized targets follow the five-minute category, including "1H".
		// The caller's selected TTL therefore needs exact matching to request the one-hour price.
		if usage.CacheCreation5mTokens == total && usage.CacheCreation1hTokens == 0 {
			return false // already entirely in the five-minute bucket
		}
		usage.CacheCreation5mTokens = total
		usage.CacheCreation1hTokens = 0
	}
	return true
}

// IncludeIndependentReasoningTokens adds separately reported reasoning to
// output within the positive gap between total and input plus output. Missing
// reasoning or total counts, negative input/output, and totals that already
// include all observed output leave the supplied output unchanged.
//
// An xAI Chat observation with input=32, output=9, reasoning=94, and total=135
// yields output=103. Inclusive OpenAI output already closes that total gap and
// receives no addition. When the gap is smaller than the reasoning count, the
// gap caps the addition; when reasoning is smaller, its count caps the addition.
func IncludeIndependentReasoningTokens(input, output, total, reasoning int64) int64 {
	if input < 0 || output < 0 || reasoning <= 0 || total <= 0 {
		return output
	}
	if total == input+output {
		return output
	}
	gap := total - input - output
	if gap <= 0 {
		return output
	}
	if reasoning < gap {
		// The total gap can include work beyond the separately reported reasoning.
		// Cap the addition at that reasoning observation so the helper does not invent extra output.
		gap = reasoning
	}
	return output + gap
}

// HasObservedTokens reports whether any stored token bucket is positive.
// Nil receivers and all-zero counts return false; speed labels alone and
// explicit zero-token usage observations do not establish positive token work.
func (u *TokenUsage) HasObservedTokens() bool {
	if u == nil {
		return false
	}
	return u.InputTokens > 0 || u.OutputTokens > 0 ||
		u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0 ||
		u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 ||
		u.ImageOutputTokens > 0
}
