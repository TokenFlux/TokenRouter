package openai

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

// cache_write_usage.go validates Responses cache write counts and carries their
// lifetime breakdown through sparse or additive usage observations. The cache
// write total already belongs to the inclusive input count, and the breakdown
// supplies a pricing split of that same total.
//
// An omitted aggregate can retain a complete prior observation from the same
// response. An explicit contradictory breakdown removes the earlier split so
// the caller can charge the reported aggregate at its fallback rate. For
// independent observations, the combined split is usable only when the
// positive write totals on both sides have compatible complete details.

// CacheWriteTokensDetails partitions one write total by OpenAI cache lifetime.
type CacheWriteTokensDetails struct {
	Tokens30m int `json:"ttl_30m_tokens"` // thirty-minute write count
	Tokens12h int `json:"ttl_12h_tokens"` // twelve-hour write count
}

// Matches reports whether the split covers exactly the aggregate write count.
func (d *CacheWriteTokensDetails) Matches(total int) bool {
	return d != nil && d.Tokens30m >= 0 && d.Tokens12h >= 0 &&
		d.Tokens30m <= total && d.Tokens12h == total-d.Tokens30m
}

// ObserveCacheWriteDetails returns the current total and its validated split.
// Supply previous observations only for the same response. A sparse frame
// without write fields preserves known details, while a changed total keeps
// earlier details only when they still match. Invalid explicit detail leaves
// the authoritative aggregate available without a split.
func ObserveCacheWriteDetails(value gjson.Result, previousTotal int, previous *CacheWriteTokensDetails) (int, *CacheWriteTokensDetails) {
	total := OpenAICacheCreationTokensFromUsage(value)
	if previous != nil && !usageHasAny(value, cacheWriteDetailPaths...) && !usageHasAny(value, cacheWriteAliasPaths...) {
		total = previousTotal
	}
	raw := value.Get(cacheWriteDetailsPath)
	if !raw.Exists() {
		if previous.Matches(total) {
			return total, previous
		}
		return total, nil
	}
	var decoded struct {
		Tokens30m *int `json:"ttl_30m_tokens"` // reported short TTL count
		Tokens12h *int `json:"ttl_12h_tokens"` // reported long TTL count
	}
	if json.Unmarshal([]byte(raw.Raw), &decoded) != nil || decoded.Tokens30m == nil || decoded.Tokens12h == nil ||
		!usageHasAny(value, cacheWriteDetailPaths...) && !usageHasAny(value, cacheWriteAliasPaths...) && previous == nil {
		return total, nil
	}
	details := &CacheWriteTokensDetails{Tokens30m: *decoded.Tokens30m, Tokens12h: *decoded.Tokens12h}
	if !details.Matches(total) {
		return total, nil
	}
	return total, details
}

// AddCacheWriteDetails adds two disjoint accounting observations when any
// positive contribution has a complete breakdown. A positive unknown
// contribution makes the combined total unclassified; neutral zero usage
// preserves the other side's observation.
func AddCacheWriteDetails(leftTotal int, left *CacheWriteTokensDetails, rightTotal int, right *CacheWriteTokensDetails) *CacheWriteTokensDetails {
	if leftTotal == 0 && left == nil {
		return right
	}
	if rightTotal == 0 && right == nil {
		return left
	}
	if !left.Matches(leftTotal) || !right.Matches(rightTotal) {
		return nil
	}
	return &CacheWriteTokensDetails{Tokens30m: left.Tokens30m + right.Tokens30m, Tokens12h: left.Tokens12h + right.Tokens12h}
}

// IsExplicitZeroOpenAIUsage reports whether both aggregate token fields
// explicitly describe a complete zero observation without billable details.
// A missing field remains sparse. Callers use the original counters before
// merging and decide whether their transport resets or retains the prior snapshot.
func IsExplicitZeroOpenAIUsage(raw gjson.Result, counts ForwardUsage) bool {
	return usageHasAny(raw, "input_tokens", "prompt_tokens") &&
		usageHasAny(raw, "output_tokens", "completion_tokens") &&
		raw.Get("total_tokens").Int() == 0 &&
		counts.InputTokens == 0 && counts.OutputTokens == 0 &&
		counts.CacheCreationInputTokens == 0 && counts.CacheReadInputTokens == 0 &&
		counts.ImageInputTokens == 0 && counts.ImageCacheReadTokens == 0 && counts.ImageOutputTokens == 0
}

// applyObservedCacheWriteDetails joins sparse writes before stream terminal selection.
func applyObservedCacheWriteDetails(previous, current *ForwardUsage, raw gjson.Result) {
	// Leave a complete zero intact so each consumer can detect its existing reset
	// or placeholder rule before cached counters make the observation appear positive.
	if previous.CacheWriteDetails == nil || IsExplicitZeroOpenAIUsage(raw, *current) {
		return
	}
	// A terminal frame can contain only output usage. Retain observed input and
	// reads with the split so ordinary input stays consistent with the aggregate.
	if !usageHasAny(raw, "input_tokens", "prompt_tokens", "prompt_cache_miss_tokens") {
		current.InputTokens = previous.InputTokens
	}
	if !usageHasAny(raw, "output_tokens", "completion_tokens") {
		current.OutputTokens = previous.OutputTokens
	}
	if !usageHasAny(raw, cacheReadDetailPaths...) && !usageHasAny(raw, cacheReadAliasPaths...) {
		current.CacheReadInputTokens = previous.CacheReadInputTokens
	}
	current.CacheCreationInputTokens, current.CacheWriteDetails = ObserveCacheWriteDetails(raw, previous.CacheCreationInputTokens, previous.CacheWriteDetails)
	if current.CacheCreationInputTokens == 0 && current.CacheWriteDetails == nil {
		// A legacy zero does not change unrelated accounting, but still clears an
		// earlier classified write so positive-only merges cannot reuse stale counts.
		previous.CacheCreationInputTokens = 0
		previous.CacheWriteDetails = nil
	}
}

// equalCacheWriteDetails compares the presence and values of two observations.
func equalCacheWriteDetails(a, b *CacheWriteTokensDetails) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
