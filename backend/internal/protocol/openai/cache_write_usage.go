package openai

import (
	"encoding/json"
	"math"

	"github.com/tidwall/gjson"
)

// cache_write_usage.go extracts Responses cache-write counts and carries raw
// lifetime breakdowns through sparse or additive usage observations. The cache
// write total belongs to the inclusive input count, while nested lifetime
// details supply 30m and 12h observations for downstream settlement and
// anomaly diagnostics.
//
// Sparse terminal frames without write fields retain earlier same-response
// totals and lifetime details. Explicit nonmatching or outer-zero breakdowns
// remain attached to forwarded accounting so SettleCacheWrite can reconcile
// partial, missing-total, or overreported splits, while Matches gates client
// wire synthesis so converted responses emit only consistent splits.

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

// HasTokens reports whether either cache-write lifetime bucket is positive.
func (d *CacheWriteTokensDetails) HasTokens() bool {
	return d != nil && (d.Tokens30m > 0 || d.Tokens12h > 0)
}

// EffectiveCacheWriteTokens returns the authoritative cache-write count from an
// outer aggregate and optional lifetime details. A positive aggregate wins,
// while zero aggregates fall back to the nonnegative detail sum without
// overflowing int.
func EffectiveCacheWriteTokens(total int, details *CacheWriteTokensDetails) int {
	if total > 0 {
		return total
	}
	if total < 0 || details == nil {
		return 0
	}
	short := max(details.Tokens30m, 0)
	long := max(details.Tokens12h, 0)
	if short > math.MaxInt-long {
		return math.MaxInt
	}
	return short + long
}

// ObserveCacheWriteDetails returns the observed outer total and raw TTL split
// for accounting. Supply previous observations only for the same response.
//
// Sparse frames without write fields retain the prior total and split, and
// positive aggregate updates without nested details preserve earlier splits so
// settlement can classify untagged increments. Explicit object-shaped details
// with at least one TTL key retain their raw counts even when they differ from
// total or omit the outer aggregate, while explicit zero writes and malformed
// detail objects clear the split.
func ObserveCacheWriteDetails(value gjson.Result, previousTotal int, previous *CacheWriteTokensDetails) (int, *CacheWriteTokensDetails) {
	hasWrite := usageHasAny(value, cacheWriteDetailPaths...) || usageHasAny(value, cacheWriteAliasPaths...)
	total := OpenAICacheCreationTokensFromUsage(value)
	if previous != nil && !hasWrite {
		total = previousTotal
	}
	raw := value.Get(cacheWriteDetailsPath)
	if !raw.Exists() {
		if !hasWrite || (total > 0 && previous != nil) {
			return total, previous
		}
		return total, nil
	}
	if !raw.IsObject() {
		return total, nil
	}
	var decoded struct {
		Tokens30m *int `json:"ttl_30m_tokens"` // reported short TTL count
		Tokens12h *int `json:"ttl_12h_tokens"` // reported long TTL count
	}
	if json.Unmarshal([]byte(raw.Raw), &decoded) != nil || (decoded.Tokens30m == nil && decoded.Tokens12h == nil) {
		return total, nil
	}
	var tokens30m, tokens12h int
	if decoded.Tokens30m != nil {
		tokens30m = *decoded.Tokens30m
	}
	if decoded.Tokens12h != nil {
		tokens12h = *decoded.Tokens12h
	}
	if total == 0 && tokens30m == 0 && tokens12h == 0 {
		return 0, nil
	}
	return total, &CacheWriteTokensDetails{Tokens30m: tokens30m, Tokens12h: tokens12h}
}

// AddCacheWriteDetails combines two disjoint accounting observations across
// multi-turn or multi-step sessions. Neutral zero usage preserves the other
// side's observation, untagged positive totals contribute to the default 30m
// bucket when paired with a detailed turn, and negative details drop the split.
func AddCacheWriteDetails(leftTotal int, left *CacheWriteTokensDetails, rightTotal int, right *CacheWriteTokensDetails) *CacheWriteTokensDetails {
	_, details := combineCacheWriteObservations(leftTotal, left, rightTotal, right)
	return details
}

// combineCacheWriteObservations merges two independent cache-write totals and
// optional lifetime splits while preserving single-turn missing-total evidence.
func combineCacheWriteObservations(leftTotal int, left *CacheWriteTokensDetails, rightTotal int, right *CacheWriteTokensDetails) (int, *CacheWriteTokensDetails) {
	if leftTotal == 0 && left == nil {
		if right == nil {
			return rightTotal, nil
		}
		return rightTotal, new(*right)
	}
	if rightTotal == 0 && right == nil {
		if left == nil {
			return leftTotal, nil
		}
		return leftTotal, new(*left)
	}
	effLeft := EffectiveCacheWriteTokens(leftTotal, left)
	effRight := EffectiveCacheWriteTokens(rightTotal, right)
	total := effLeft + effRight
	if left == nil && right == nil {
		return total, nil
	}
	if (left != nil && (left.Tokens30m < 0 || left.Tokens12h < 0)) ||
		(right != nil && (right.Tokens30m < 0 || right.Tokens12h < 0)) {
		return total, nil
	}
	l30, l12 := max(effLeft, 0), 0
	if left != nil {
		l30, l12 = left.Tokens30m, left.Tokens12h
	}
	r30, r12 := max(effRight, 0), 0
	if right != nil {
		r30, r12 = right.Tokens30m, right.Tokens12h
	}
	return total, &CacheWriteTokensDetails{Tokens30m: l30 + r30, Tokens12h: l12 + r12}
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
		counts.CacheCreationInputTokens == 0 && !counts.CacheWriteDetails.HasTokens() &&
		counts.CacheReadInputTokens == 0 &&
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
