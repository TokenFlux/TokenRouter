package anthropic

import (
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// usage_merge.go merges Anthropic usage observations into disjoint prompt and
// cache accounting buckets.
// Anthropic-compatible responses can describe prompt work as uncached tokens,
// a total prompt count, or separate cache hit and miss buckets. Converted
// Messages streams need disjoint input and cache buckets for billing, even
// when later events revise how the same prompt work is represented.
//
// MergeAnthropicUsage uses total or hit/miss evidence when supplied, subtracts
// cached work from a total, and clamps calculated uncached counts to zero.
// The ordinary Anthropic path updates positive counters and retains previous
// observations for omitted or zero counters. Positive canonical cache fields
// take precedence over compatible aliases, and nonblank speed observations
// survive alongside the token counts for completion-time pricing.
// Lifetime buckets independently retain presence-sensitive zero updates and
// supply the write aggregate only when that observation omits a positive total.

// MergeAnthropicUsage merges a Messages usage observation into billing buckets.
// A nil destination is ignored. Prompt totals or hit/miss pointers select the
// compatible-provider calculation, where a supplied miss bucket is
// authoritative even when it is zero.
//
// Otherwise, positive Anthropic counters update the destination while omitted
// or zero counters retain prior observations. Canonical cache counters override
// aliases when positive, and a nonblank speed string is copied as supplied.
// Callers serialize updates to the shared destination across stream events.
// Lifetime details replace their supplied buckets, including zero; their sum
// supplies cache creation only when no positive aggregate accompanies the update.
func MergeAnthropicUsage(dst *protocol.TokenUsage, src AnthropicUsage) {
	if dst == nil {
		return
	}
	if details := src.CacheCreation; details != nil {
		// Lifetime observations replace their own buckets before deriving a missing
		// aggregate; adding both the aggregate and its breakdown would double-charge writes.
		if details.Ephemeral5mInputTokens != nil {
			dst.CacheCreation5mTokens = max(*details.Ephemeral5mInputTokens, 0)
		}
		if details.Ephemeral1hInputTokens != nil {
			dst.CacheCreation1hTokens = max(*details.Ephemeral1hInputTokens, 0)
		}
		if src.CacheCreationInputTokens == 0 {
			src.CacheCreationInputTokens = dst.CacheCreation5mTokens + dst.CacheCreation1hTokens
			dst.CacheCreationInputTokens = src.CacheCreationInputTokens
		}
	}

	// Compatible prompt totals include cached work; split those totals before
	// billing so cache hits and cache writes are not charged as uncached input.
	if src.PromptTokens > 0 || src.PromptCacheHitTokens != nil || src.PromptCacheMissTokens != nil {
		cacheReadTokens := src.CacheReadInputTokens
		// Canonical cache reads take precedence. Resolve missing reads through
		// compatible aliases in order so one observation contributes one cache bucket.
		if cacheReadTokens == 0 && src.CachedTokens > 0 {
			cacheReadTokens = src.CachedTokens
		}
		if cacheReadTokens == 0 && src.PromptTokensDetails != nil && src.PromptTokensDetails.CachedTokens > 0 {
			cacheReadTokens = src.PromptTokensDetails.CachedTokens
		}
		if cacheReadTokens == 0 && src.PromptCacheHitTokens != nil {
			cacheReadTokens = max(*src.PromptCacheHitTokens, 0)
		}

		if src.PromptCacheMissTokens != nil {
			// A supplied miss count already excludes cached work, including the
			// fully cached zero case; subtracting cache reads again would erase valid input.
			dst.InputTokens = max(*src.PromptCacheMissTokens, 0)
		} else {
			// Cache read and creation tokens belong to separate billing buckets.
			// Clamp inconsistent totals so normalization cannot create negative uncached usage.
			dst.InputTokens = max(src.PromptTokens-cacheReadTokens-src.CacheCreationInputTokens, 0)
		}
		dst.CacheReadInputTokens = cacheReadTokens
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	} else {
		if src.InputTokens > 0 {
			dst.InputTokens = src.InputTokens
		}
		if src.CacheReadInputTokens > 0 {
			dst.CacheReadInputTokens = src.CacheReadInputTokens
		} else if src.CachedTokens > 0 {
			// Some compatible Messages responses expose cache reads as cached_tokens.
			// Use that alias when the canonical count contributes no positive observation.
			dst.CacheReadInputTokens = src.CachedTokens
		}
		if src.CacheCreationInputTokens > 0 {
			dst.CacheCreationInputTokens = src.CacheCreationInputTokens
		}
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if strings.TrimSpace(src.Speed) != "" {
		dst.Speed = src.Speed
	}
}

// NormalizeUsage resolves one complete Messages usage value into canonical
// disjoint buckets. Compatible prompt aliases and lifetime-only writes are
// normalized through the same merger used by accounting; no input is invented
// when the upstream supplies neither totals nor individual buckets.
//
// Returned values omit overlapping compatibility aliases, so another bridge
// does not subtract the same cached prompt twice. Explicitly reported zero
// lifetime details remain present in the result rather than becoming absent
// observations during subsequent serialization.
func NormalizeUsage(source AnthropicUsage) AnthropicUsage {
	var usage protocol.TokenUsage
	MergeAnthropicUsage(&usage, source)
	result := UsageFromTokens(usage)
	if source.CacheCreation != nil && result.CacheCreation == nil {
		// Retain explicitly reported zero lifetimes in the canonical wire value;
		// otherwise a later consumer could mistake a cleared bucket for absent metadata.
		zero5m, zero1h := 0, 0
		result.CacheCreation = &AnthropicCacheCreation{Ephemeral5mInputTokens: &zero5m, Ephemeral1hInputTokens: &zero1h}
	}
	return result
}

// UsageFromTokens projects observed disjoint token buckets into Messages usage.
// Positive lifetime detail remains available without being added to its aggregate
// again. The returned pointers own copied counts rather than the supplied snapshot.
func UsageFromTokens(usage protocol.TokenUsage) AnthropicUsage {
	result := AnthropicUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		Speed:                    usage.Speed,
	}
	if usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0 {
		fiveMinutes, oneHour := usage.CacheCreation5mTokens, usage.CacheCreation1hTokens
		result.CacheCreation = &AnthropicCacheCreation{Ephemeral5mInputTokens: &fiveMinutes, Ephemeral1hInputTokens: &oneHour}
	}
	return result
}

// NormalizeStreamUsage reconciles a decoded event with its raw usage observation
// and writes the accumulated canonical snapshot back into that event. It returns
// false for unrelated events or absent usage objects, preserving prior counters.
//
// Accounting and client conversion therefore receive the same prompt partition,
// including compatible aliases, cache-only observations, and explicit lifetime
// zeroes. Raw reconciliation follows the typed merge to retain existing alias
// precedence and fields whose presence matters. Only usage nodes are changed;
// the response identity, content, stop reason, and failure evidence remain intact.
func NormalizeStreamUsage(payload []byte, event *AnthropicStreamEvent, usage *protocol.TokenUsage) bool {
	if event == nil || usage == nil {
		return false
	}
	var target *AnthropicUsage
	var path string
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			target, path = &event.Message.Usage, "message.usage"
		}
	case "message_delta":
		target, path = event.Usage, "usage"
	}
	if target == nil || !gjson.GetBytes(payload, path).IsObject() {
		return false
	}
	hadLifetime := target.CacheCreation != nil
	MergeAnthropicUsage(usage, *target)
	ParseSSEUsagePassthrough(string(payload), usage)
	*target = UsageFromTokens(*usage)
	if hadLifetime && target.CacheCreation == nil {
		zero5m, zero1h := 0, 0
		target.CacheCreation = &AnthropicCacheCreation{Ephemeral5mInputTokens: &zero5m, Ephemeral1hInputTokens: &zero1h}
	}
	return true
}
