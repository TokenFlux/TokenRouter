package pricing

import (
	"slices"
	"strings"
)

// provider_stats.go selects provider statistics rules and calculates their
// independent costs.
// Provider statistics prices define an independent cost base for provider
// reporting. Rule selectors match either provider or group, and the supplied
// rule order determines the first calculable cost for each complete model name,
// including an explicit zero override.
//
// Token statistics count input, output, and aggregate cache usage together for
// interval selection. A matching interval replaces the flat token buckets;
// omitted prices cost zero and service-tier, time, effort, and interval-factor
// rules are not applied here. Output and image-output counts are charged
// additively as supplied. Request and image modes use the flat unit amount and
// request count, with a nonpositive count defaulting to one; nil costs let
// callers continue to another rule or catalog pricing.

// ProviderStatsInput contains loaded rules, ordered model candidates, and
// custom-cost usage. ProviderID and GroupID are matched independently against
// each rule's alternative selectors; the first calculable cost remains handled
// even when its amount is zero.
type ProviderStatsInput struct {
	// Rules are tried in their supplied order for each model. The caller loads or
	// sorts them and keeps their nested cards stable during resolution.
	Rules       []ProviderStatsPricingRule // ordered provider-cost pricing rules
	ProviderID, // provider identifier for rule selection
	GroupID int64 // group identifier for rule selection
	// Models are tried in their supplied order, and the first calculable custom
	// cost ends selection. Callers prepare trimmed complete identities before
	// passing this list.
	Models []string    // ordered complete model candidates
	Tokens UsageTokens // reported provider-cost token buckets
	// RequestCount supplies flat request or image count. Nonpositive values become
	// one in custom unit pricing; token statistics uses its token buckets instead.
	RequestCount int // custom request or image count
}

// UniqueNonEmptyProviderStatsModels returns trimmed nonempty model names in
// first-occurrence order. Deduplication ignores case while retaining the first
// name's spelling. The returned slice is newly allocated and full versions and
// provider qualifications remain intact.
func UniqueNonEmptyProviderStatsModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	return out
}

// TryCustomRules returns the first applicable, calculable cost in supplied rule
// order. Callers keep rule data stable and supply trimmed model text and
// validated usage. Matching a selector alone is insufficient: an effective
// model card and a non-nil calculated amount are required.
//
// A zero calculated amount is usable and ends selection, while missing model
// cards or nil costs leave later rules eligible. Custom card prices establish
// their own cost base, so this path does not inherit catalog service tiers,
// user effort or time factors, or group charging rates.
func TryCustomRules(
	rules []ProviderStatsPricingRule, providerID, groupID int64,
	model string, tokens UsageTokens, requestCount int,
) *float64 {
	cost, _ := tryCustomRulesWithMode(rules, providerID, groupID, model, tokens, requestCount)
	return cost
}

// tryCustomRulesWithMode resolves a provider card together with its billing unit.
// A present zero amount remains a priced result; no matching calculable card
// returns nil and an empty mode so callers can apply their ordinary fallback.
//
// The card selection uses the same ordered provider and group rules as normal
// cost reporting. Keeping the selected mode beside its amount lets system
// callers reject operation prices when only incomplete token evidence survived,
// without guessing the mode from the model name or a numeric amount.
func tryCustomRulesWithMode(
	rules []ProviderStatsPricingRule, providerID, groupID int64,
	model string, tokens UsageTokens, requestCount int,
) (*float64, BillingMode) {
	modelLower := strings.ToLower(model)
	for _, rule := range rules {
		if !MatchProviderStatsRule(&rule, providerID, groupID) {
			continue
		}
		pricing := FindEffectivePricingForModel(rule.Pricing, modelLower)
		// The selector matches but this complete model has no effective card; later
		// ordered rules may supply its provider cost.
		if pricing == nil {
			continue
		}
		// These configured statistics amounts supply a separate provider cost base;
		// catalog and user-facing modifiers would change that independent amount.
		if cost := CalculateStatsCost(pricing, tokens, requestCount); cost != nil {
			return cost, pricing.BillingMode
		}
	}
	return nil, ""
}

// MatchProviderStatsRule reports whether either selector list contains its
// supplied identifier. The rule must be non-nil. ProviderIDs or GroupIDs can
// establish a match independently; both lists empty means no match.
func MatchProviderStatsRule(rule *ProviderStatsPricingRule, providerID, groupID int64) bool {
	if len(rule.ProviderIDs) == 0 && len(rule.GroupIDs) == 0 {
		return false
	}
	if slices.Contains(rule.ProviderIDs, providerID) {
		return true
	}
	return slices.Contains(rule.GroupIDs, groupID)
}

// FindEffectivePricingForModel returns an effective exact or trailing-star card
// for provider statistics. It skips placeholder rows and accepts explicit zero
// amounts. Returned cards reference pricingList, and modelLower supplies
// normalized lowercase text for wildcard matching.
func FindEffectivePricingForModel(pricingList []ModelPricingEntry, modelLower string) *ModelPricingEntry {
	return FindPricingForModelByPredicate(pricingList, modelLower, func(p *ModelPricingEntry) bool {
		return p != nil && p.HasEffectivePricing()
	})
}

// FindPricingForModelByPredicate returns an included exact card before
// considering wildcard cards. Nil include accepts every row. Exact comparison
// ignores case, while wildcard matching lowercases patterns and requires a
// lowercase candidate; neither path trims supplied text.
//
// A trailing star denotes prefix matching, with the first included matching row
// winning among wildcards. The predicate receives pointers into pricingList and
// returned pointers share those entries. Callers keep the supplied slice and
// nested card data stable throughout selection and use.
func FindPricingForModelByPredicate(pricingList []ModelPricingEntry, modelLower string, include func(*ModelPricingEntry) bool) *ModelPricingEntry {
	if include == nil {
		include = func(*ModelPricingEntry) bool { return true }
	}
	// An exact entry is specific to this full model, so search every included card
	// before using a prefix wildcard.
	for i := range pricingList {
		p := &pricingList[i]
		if !include(p) {
			continue
		}
		for _, m := range p.Models {
			if strings.EqualFold(m, modelLower) {
				return p
			}
		}
	}
	// Overlapping wildcard patterns use the supplied card order; this pass runs
	// only after every included exact match misses.
	for i := range pricingList {
		p := &pricingList[i]
		if !include(p) {
			continue
		}
		for _, m := range p.Models {
			ml := strings.ToLower(m)
			if !strings.HasSuffix(ml, "*") {
				continue
			}
			prefix := strings.TrimSuffix(ml, "*")
			if strings.HasPrefix(modelLower, prefix) {
				return p
			}
		}
	}
	return nil
}

// CalculateStatsCost returns a usable custom-card cost with its optional amount
// multiplier. Nil cards return nil. Request and image modes use flat unit-count
// pricing; all other modes dispatch to token statistics. A nil base cost
// remains unresolved, while a zero result stays present.
//
// PriceMultiplier applies after calculation and uses the same negative-to-zero
// normalization as request billing. Other user-facing service-tier, effort, and
// time modifiers are not applied on this path. Callers supply validated finite
// card amounts and usage, since direct calculations reject negative resulting
// costs but do not independently reject every nonfinite value.
func CalculateStatsCost(pricing *ModelPricingEntry, tokens UsageTokens, requestCount int) *float64 {
	if pricing == nil {
		return nil
	}
	var cost *float64
	switch pricing.BillingMode {
	case BillingModePerRequest, BillingModeImage:
		cost = CalculatePerRequestStatsCost(pricing, requestCount)
	default:
		cost = CalculateTokenStatsCost(pricing, tokens)
	}
	if cost == nil {
		return nil
	}
	// Apply the amount factor after a usable cost exists, so a zero factor retains
	// a handled free override.
	if multiplier, configured := NormalizedPriceMultiplier(pricing); configured {
		scaled := *cost * multiplier
		cost = &scaled
	}
	return cost
}

// CalculatePerRequestStatsCost returns the flat request or image amount times
// count. Pricing must be non-nil; an absent flat amount or negative result
// returns nil. Nonpositive counts become one, zero remains present, and callers
// validate amount finiteness.
func CalculatePerRequestStatsCost(pricing *ModelPricingEntry, requestCount int) *float64 {
	if pricing.PerRequestPrice == nil {
		return nil
	}
	if requestCount <= 0 {
		requestCount = 1
	}
	cost := *pricing.PerRequestPrice * float64(requestCount)
	if cost < 0 {
		return nil
	}
	return &cost
}

// CalculateTokenStatsCost returns an independent provider amount from supplied
// token counts. Callers provide a non-nil validated card and usage sums that
// fit int. Input, output, and aggregate cache counts select the first token
// interval; a matched interval replaces flat text and cache prices, while the
// card's image-output amount remains available.
//
// Missing bucket prices contribute zero and interval factors are not inherited.
// Output and image-output counts are added separately as supplied. An hourly
// cache pointer permits normalized TTL details; absent positive details retain
// aggregate short-rate charging. No price pointers, no positive recognized
// usage, or a negative resulting cost returns nil; an explicit free calculated
// cost returns a present zero.
func CalculateTokenStatsCost(pricing *ModelPricingEntry, tokens UsageTokens) *float64 {
	p := pricing
	if validIntervals := FilterValidTokenIntervals(pricing.Intervals); len(validIntervals) > 0 {
		totalTokens := tokens.InputTokens + tokens.OutputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		// The selected statistics interval replaces flat buckets; omitted amounts
		// remain zero instead of inheriting user-card defaults or catalog prices.
		if iv := FindMatchingInterval(validIntervals, totalTokens); iv != nil {
			p = &ModelPricingEntry{
				InputPrice:         iv.InputPrice,
				OutputPrice:        iv.OutputPrice,
				CacheWritePrice:    iv.CacheWritePrice,
				CacheWrite1hPrice:  iv.CacheWrite1hPrice,
				CacheWrite30mPrice: iv.CacheWrite30mPrice,
				CacheWrite12hPrice: iv.CacheWrite12hPrice,
				CacheReadPrice:     iv.CacheReadPrice,
				ImageOutputPrice:   pricing.ImageOutputPrice,
			}
		}
	}
	if !HasAnyTokenStatsPrice(p) || !HasAnyStatsTokenUsage(tokens) {
		return nil
	}
	deref := func(ptr *float64) float64 {
		if ptr == nil {
			return 0
		}
		return *ptr
	}
	cacheCreationCost := float64(tokens.CacheCreationTokens) * deref(p.CacheWritePrice)
	// An hourly amount enables detail pricing, including free hourly usage;
	// missing positive details retain the aggregate short-rate amount.
	if short, long, ok := openAIWriteBreakdown(tokens); ok && (p.CacheWrite30mPrice != nil || p.CacheWrite12hPrice != nil) {
		cacheCreationCost = float64(short)*cacheWriteRate(p.CacheWrite30mPrice, deref(p.CacheWritePrice)) +
			float64(long)*cacheWriteRate(p.CacheWrite12hPrice, deref(p.CacheWritePrice))
	} else if p.CacheWrite1hPrice != nil {
		cache5m, cache1h := NormalizeCacheCreationBreakdown(tokens)
		if cache5m > 0 || cache1h > 0 {
			cacheCreationCost = float64(cache5m)*deref(p.CacheWritePrice) +
				float64(cache1h)*deref(p.CacheWrite1hPrice)
		}
	}
	cost := float64(tokens.InputTokens)*deref(p.InputPrice) +
		float64(tokens.OutputTokens)*deref(p.OutputPrice) +
		cacheCreationCost +
		float64(tokens.CacheReadTokens)*deref(p.CacheReadPrice) +
		float64(tokens.ImageOutputTokens)*deref(p.ImageOutputPrice)
	if cost < 0 {
		return nil
	}
	return &cost
}

// HasAnyTokenStatsPrice reports a supplied statistics token amount pointer,
// including zero. Nil cards return false. Image-input prices and interval
// modifiers alone do not establish a flat statistics amount for this
// calculation.
func HasAnyTokenStatsPrice(pricing *ModelPricingEntry) bool {
	return pricing != nil && (pricing.InputPrice != nil ||
		pricing.OutputPrice != nil ||
		pricing.CacheWritePrice != nil ||
		pricing.CacheWrite1hPrice != nil || pricing.CacheWrite30mPrice != nil || pricing.CacheWrite12hPrice != nil ||
		pricing.CacheReadPrice != nil ||
		pricing.ImageOutputPrice != nil)
}

// HasAnyStatsTokenUsage reports positive recognized text, cache, or
// image-output usage. Image-input and TTL detail counts are subsets and do not
// establish usage alone. The aggregate fields therefore control this check.
func HasAnyStatsTokenUsage(tokens UsageTokens) bool {
	return tokens.InputTokens > 0 ||
		tokens.OutputTokens > 0 ||
		tokens.CacheCreationTokens > 0 ||
		tokens.CacheReadTokens > 0 ||
		tokens.ImageOutputTokens > 0
}

// ResolveProviderStatsOverride returns the first calculable custom cost and
// handled flag for the supplied model candidates. Models are tried in input
// order, and each candidate's rules are tried until a cost is found or its rule
// list ends before the next model is considered. Callers supply trimmed full
// identities, validated usage, and stable rule and card data.
//
// A selector match or effective card alone can still yield no usable cost.
// Those misses leave later models eligible, while a present zero cost ends
// selection as a handled free override. If every candidate misses, the caller
// can continue to catalog pricing.
func ResolveProviderStatsOverride(input ProviderStatsInput) (*float64, bool) {
	cost, _ := ResolveProviderStatsOverrideWithMode(input)
	return cost, cost != nil
}

// ResolveProviderStatsOverrideWithMode preserves the selected cost card's unit.
// Candidate model order still precedes configured rule order, and a zero cost
// remains a successful selection. Missing costs return nil and an empty mode.
//
// Internal usage may survive an interrupted operation with only partial token
// evidence. Returning the unit with its calculated amount allows that caller
// to reject per-request or image prices while retaining valid token costs.
// Customer settlement continues using the same amount through the existing API.
func ResolveProviderStatsOverrideWithMode(input ProviderStatsInput) (*float64, BillingMode) {
	for _, model := range input.Models {
		if cost, mode := tryCustomRulesWithMode(input.Rules, input.ProviderID, input.GroupID, model, input.Tokens, input.RequestCount); cost != nil {
			if mode == "" {
				mode = BillingModeToken
			}
			return cost, mode
		}
	}
	return nil, ""
}
