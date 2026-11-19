package anthropic

import (
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// usage_events.go parses Messages usage observations and reconciles compatible
// prompt and cache counters.
// Anthropic-compatible providers can use input_tokens for a prompt total in
// an initial event and for uncached work in a later event. Billing needs those
// observations resolved into separate uncached, cache-read, and cache-write
// buckets before downstream code consumes the completed request's usage.
//
// The passthrough parser establishes aggregate prompt counts at message_start
// and selectively replaces positive aggregates at message_delta. Cache TTL
// details retain explicit zero observations, and compatible prompt totals or
// hit/miss aliases can authoritatively recalculate uncached input to zero.
// Buffered response parsing starts with a fresh destination and uses the same
// compatible-provider normalization. Cache-read aliases and TTL detail totals
// fill empty canonical buckets before that normalization resolves overlapping
// prompt quantities.

// ParseSSEUsagePassthrough merges one Messages SSE data payload into usage.
// Nil destinations, empty input, and the exact DONE sentinel are ignored.
// Initial prompt aggregates establish zero defaults; positive delta aggregates
// replace previous counts, while explicit TTL details can clear earlier buckets.
//
// Compatible cached-token fields and TTL totals fill empty cache buckets,
// including usage nodes on other event types. Prompt totals or hit/miss aliases
// then normalize the selected usage node into disjoint billing buckets.
// Callers serialize updates and validate stream framing separately; gjson
// path extraction does not establish complete JSON validity.
func ParseSSEUsagePassthrough(data string, usage *protocol.TokenUsage) {
	if usage == nil || data == "" || data == "[DONE]" {
		return
	}
	if wirejson.ValidateUsageEnvelope(unsafe.Slice(unsafe.StringData(data), len(data))) != nil {
		return
	}

	parsed := gjson.Parse(data)
	switch parsed.Get("type").String() {
	case "message_start":
		msgUsage := parsed.Get("message.usage")
		if msgUsage.Exists() {
			if speed := strings.TrimSpace(msgUsage.Get("speed").String()); speed != "" {
				usage.Speed = speed
			}
			usage.InputTokens = int(msgUsage.Get("input_tokens").Int())
			usage.CacheCreationInputTokens = int(msgUsage.Get("cache_creation_input_tokens").Int())
			usage.CacheReadInputTokens = int(msgUsage.Get("cache_read_input_tokens").Int())

			// An initial TTL breakdown establishes both categories together. When
			// either field is present, the other missing field resets to zero as well.
			cc5m := msgUsage.Get("cache_creation.ephemeral_5m_input_tokens")
			cc1h := msgUsage.Get("cache_creation.ephemeral_1h_input_tokens")
			if cc5m.Exists() || cc1h.Exists() {
				usage.CacheCreation5mTokens = int(cc5m.Int())
				usage.CacheCreation1hTokens = int(cc1h.Int())
			}
		}
	case "message_delta":
		deltaUsage := parsed.Get("usage")
		if deltaUsage.Exists() {
			if speed := strings.TrimSpace(deltaUsage.Get("speed").String()); speed != "" {
				usage.Speed = speed
			}
			if v := deltaUsage.Get("input_tokens").Int(); v > 0 {
				usage.InputTokens = int(v)
			}
			if v := deltaUsage.Get("output_tokens").Int(); v > 0 {
				usage.OutputTokens = int(v)
			}
			if v := deltaUsage.Get("cache_creation_input_tokens").Int(); v > 0 {
				usage.CacheCreationInputTokens = int(v)
			}
			if v := deltaUsage.Get("cache_read_input_tokens").Int(); v > 0 {
				usage.CacheReadInputTokens = int(v)
			}

			// Deltas update each TTL category independently, including explicit
			// zeros, so an omitted category retains its earlier stream observation.
			cc5m := deltaUsage.Get("cache_creation.ephemeral_5m_input_tokens")
			cc1h := deltaUsage.Get("cache_creation.ephemeral_1h_input_tokens")
			if cc5m.Exists() {
				usage.CacheCreation5mTokens = int(cc5m.Int())
			}
			if cc1h.Exists() {
				usage.CacheCreation1hTokens = int(cc1h.Int())
			}
		}
	}

	// Passthrough providers may expose cached_tokens beside either usage node.
	// Resolve that alias only for an empty cache-read bucket so canonical counts win.
	if usage.CacheReadInputTokens == 0 {
		if cached := parsed.Get("message.usage.cached_tokens").Int(); cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
		if cached := parsed.Get("usage.cached_tokens").Int(); usage.CacheReadInputTokens == 0 && cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
	}
	// Some streams report TTL categories without their aggregate. Their sum
	// fills an empty cache-write bucket while a supplied aggregate remains authoritative.
	if usage.CacheCreationInputTokens == 0 {
		cc5m := parsed.Get("message.usage.cache_creation.ephemeral_5m_input_tokens").Int()
		cc1h := parsed.Get("message.usage.cache_creation.ephemeral_1h_input_tokens").Int()
		if cc5m == 0 && cc1h == 0 {
			cc5m = parsed.Get("usage.cache_creation.ephemeral_5m_input_tokens").Int()
			cc1h = parsed.Get("usage.cache_creation.ephemeral_1h_input_tokens").Int()
		}
		total := cc5m + cc1h
		if total > 0 {
			usage.CacheCreationInputTokens = int(total)
		}
	}

	// Kimi's initial input_tokens can include cached work, unlike its later
	// uncached count. Normalize the stable prompt total after cache aliases are resolved.
	usageNode := parsed.Get("usage")
	if parsed.Get("type").String() == "message_start" {
		usageNode = parsed.Get("message.usage")
	}
	NormalizeAnthropicCompatiblePromptUsage(usageNode, usage)
}

// NormalizeAnthropicCompatiblePromptUsage converts compatible prompt totals
// and hit/miss aliases into uncached, cache-read, and cache-write usage.
// It returns false for nil or missing inputs and when no positive prompt total
// or hit/miss field is present, leaving the destination unchanged.
//
// Cache-read aliases fill a zero canonical count in their declared order.
// A supplied cache-miss field establishes uncached input, including zero;
// otherwise cache reads and writes are subtracted from the prompt total.
// The calculated uncached bucket is clamped to zero, and normalized cache
// observations replace the destination's corresponding aggregate buckets.
func NormalizeAnthropicCompatiblePromptUsage(usageNode gjson.Result, usage *protocol.TokenUsage) bool {
	if usage == nil || !usageNode.Exists() {
		return false
	}
	promptTokens := usageNode.Get("prompt_tokens")
	promptCacheHitTokens := usageNode.Get("prompt_cache_hit_tokens")
	promptCacheMissTokens := usageNode.Get("prompt_cache_miss_tokens")
	if (!promptTokens.Exists() || promptTokens.Int() <= 0) &&
		!promptCacheHitTokens.Exists() && !promptCacheMissTokens.Exists() {
		return false
	}

	// A present canonical field supplies this event's observation, even when
	// zero. Aliases then fill that zero in precedence order, preventing duplicate cache buckets.
	cacheReadTokens := usage.CacheReadInputTokens
	if v := usageNode.Get("cache_read_input_tokens"); v.Exists() {
		cacheReadTokens = int(v.Int())
	}
	if cacheReadTokens == 0 {
		if v := usageNode.Get("cached_tokens"); v.Exists() {
			cacheReadTokens = int(v.Int())
		}
	}
	if cacheReadTokens == 0 {
		if v := usageNode.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
			cacheReadTokens = int(v.Int())
		}
	}
	if cacheReadTokens == 0 && promptCacheHitTokens.Exists() {
		cacheReadTokens = max(int(promptCacheHitTokens.Int()), 0)
	}

	cacheCreationTokens := usage.CacheCreationInputTokens
	if v := usageNode.Get("cache_creation_input_tokens"); v.Exists() {
		cacheCreationTokens = int(v.Int())
	}
	// TTL detail can supply a missing aggregate cache-write count. Keep the
	// total separate from uncached input so prompt normalization subtracts cached work once.
	if cacheCreationTokens == 0 {
		cc5m := usageNode.Get("cache_creation.ephemeral_5m_input_tokens").Int()
		cc1h := usageNode.Get("cache_creation.ephemeral_1h_input_tokens").Int()
		if cc5m > 0 || cc1h > 0 {
			cacheCreationTokens = int(cc5m + cc1h)
		}
	}

	// An explicit miss bucket already excludes cached tokens. Its zero value
	// clears initial totals for fully cached prompts instead of retaining stale uncached usage.
	if promptCacheMissTokens.Exists() {
		usage.InputTokens = max(int(promptCacheMissTokens.Int()), 0)
	} else {
		// Prompt totals overlap both cache buckets. Subtract their observations
		// and clamp inconsistent reports so the uncached result cannot become negative.
		usage.InputTokens = max(int(promptTokens.Int())-cacheReadTokens-cacheCreationTokens, 0)
	}
	usage.CacheReadInputTokens = cacheReadTokens
	usage.CacheCreationInputTokens = cacheCreationTokens
	return true
}

// ParseClaudeUsageFromResponseBody returns a fresh usage value from a buffered
// Messages response, including speed and TTL cache details. Empty bodies or
// absent usage nodes return an allocated zero value; callers validate response
// JSON separately from this path-based extraction.
//
// Positive TTL details fill a zero cache-write aggregate, and cached_tokens
// fills a zero cache-read aggregate. Compatible prompt-total and hit/miss
// fields then delegate to NormalizeAnthropicCompatiblePromptUsage so cached
// work is separated from uncached input before the result reaches billing.
func ParseClaudeUsageFromResponseBody(body []byte) *protocol.TokenUsage {
	usage := &protocol.TokenUsage{}
	if len(body) == 0 || wirejson.ValidateUsageEnvelope(body) != nil {
		return usage
	}

	parsed := gjson.ParseBytes(body)
	usageNode := parsed.Get("usage")
	if !usageNode.Exists() {
		return usage
	}

	usage.InputTokens = int(usageNode.Get("input_tokens").Int())
	usage.OutputTokens = int(usageNode.Get("output_tokens").Int())
	usage.CacheCreationInputTokens = int(usageNode.Get("cache_creation_input_tokens").Int())
	usage.CacheReadInputTokens = int(usageNode.Get("cache_read_input_tokens").Int())
	usage.Speed = strings.TrimSpace(usageNode.Get("speed").String())

	cc5m := usageNode.Get("cache_creation.ephemeral_5m_input_tokens").Int()
	cc1h := usageNode.Get("cache_creation.ephemeral_1h_input_tokens").Int()
	if cc5m > 0 || cc1h > 0 {
		usage.CacheCreation5mTokens = int(cc5m)
		usage.CacheCreation1hTokens = int(cc1h)
	}
	if usage.CacheCreationInputTokens == 0 && (cc5m > 0 || cc1h > 0) {
		// A response can report cache-write TTL categories without an aggregate.
		// Sum those categories only when the canonical aggregate has no nonzero observation.
		usage.CacheCreationInputTokens = int(cc5m + cc1h)
	}
	if usage.CacheReadInputTokens == 0 {
		// Compatible buffered Messages responses can name cache reads cached_tokens.
		// A positive alias fills a zero canonical bucket before prompt-total normalization.
		if cached := usageNode.Get("cached_tokens").Int(); cached > 0 {
			usage.CacheReadInputTokens = int(cached)
		}
	}
	NormalizeAnthropicCompatiblePromptUsage(usageNode, usage)
	return usage
}
