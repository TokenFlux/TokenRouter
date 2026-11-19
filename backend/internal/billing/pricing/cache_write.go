package pricing

import "math"

// cache_write.go resolves cache-write TTL prices, partitions reported cache
// creation into billed TTL buckets, and projects display rates for Claude and
// OpenAI cache lifetimes.
// Upstream responses can omit TTL subdivisions, update aggregate writes in a
// later stream event without repeating subdivisions, omit the outer aggregate,
// or report contradictory counts exceeding the aggregate. Settlement needs a
// single partition rule so user charges, provider statistics, and stored usage
// buckets agree on every token without leaving untagged remainders or charging
// duplicate tokens beyond a positive aggregate.
//
// SettleCacheWrite selects either the 5m/1h or 30m/12h lifetime family from
// reported counters, configured TTL prices, and the caller's protocol default.
// Complete and partial details assign missing tokens to the family's default
// short bucket, while overreported details cap total billed tokens at the
// aggregate and prioritize the more expensive bucket. Price card overrides keep
// uniform cache_write_price distinct from independent per-TTL fields so editing
// a short-TTL rate preserves catalog long-TTL pricing.

const (
	// CacheWriteFamilyClaude and CacheWriteFamilyOpenAI identify which two-tier
	// lifetime pair classifies billed cache-creation tokens.
	CacheWriteFamilyClaude CacheWriteFamily = "5m_1h"   // five-minute and one-hour lifetimes
	CacheWriteFamilyOpenAI CacheWriteFamily = "30m_12h" // thirty-minute and twelve-hour lifetimes

	// CacheWriteAnomalyPartialBreakdown, CacheWriteAnomalyExceedsTotal, and
	// CacheWriteAnomalyMissingTotal classify upstream cache-write discrepancies
	// recorded for administrator diagnostics.
	CacheWriteAnomalyPartialBreakdown = "partial_breakdown" // positive details cover part of aggregate
	CacheWriteAnomalyExceedsTotal     = "exceeds_total"     // details exceed aggregate or are incomplete/negative
	CacheWriteAnomalyMissingTotal     = "missing_total"     // positive details arrived with zero aggregate
)

// CacheWriteFamily names the two-tier TTL scheme used for cache-write billing
// and usage-log bucket assignment.
type CacheWriteFamily string

// CacheWriteRates holds resolved USD-per-token cache-write rates before
// request-level multipliers are applied. Nil optional pointers inherit Default,
// while non-nil zero pointers represent explicit free usage for that TTL.
type CacheWriteRates struct {
	Default float64  // fallback USD per token when a TTL rate is nil
	TTL5m   *float64 // explicit five-minute rate, or nil
	TTL1h   *float64 // explicit one-hour rate, or nil
	TTL30m  *float64 // explicit thirty-minute rate, or nil
	TTL12h  *float64 // explicit twelve-hour rate, or nil
}

// CacheWriteAnomaly records raw upstream cache-write counters when reported TTL
// details do not cleanly partition the reported aggregate. Normal requests
// leave Anomaly nil so storage incurs no overhead on consistent usage.
type CacheWriteAnomaly struct {
	Kind          string `json:"kind"`                   // anomaly classification label
	ReportedTotal int    `json:"reported_total"`         // raw aggregate cache-creation count
	Reported5m    int    `json:"reported_5m,omitempty"`  // raw five-minute count when nonzero
	Reported1h    int    `json:"reported_1h,omitempty"`  // raw one-hour count when nonzero
	Reported30m   *int   `json:"reported_30m,omitempty"` // raw thirty-minute pointer when supplied
	Reported12h   *int   `json:"reported_12h,omitempty"` // raw twelve-hour pointer when supplied
}

// CacheWriteSettlement holds the USD cache-creation charge, the billed token
// partition for usage persistence, and optional upstream anomaly diagnostics.
type CacheWriteSettlement struct {
	// Billed5m and Billed1h hold the partition for CacheWriteFamilyClaude;
	// Billed30m and Billed12h hold the partition for CacheWriteFamilyOpenAI when
	// BilledTotal is positive. The active pair sums to BilledTotal.
	Cost        float64            // USD cache-creation cost after multiplier
	BilledTotal int                // total billed cache-creation tokens
	Family      CacheWriteFamily   // selected two-tier lifetime family
	Billed5m    int                // billed five-minute tokens
	Billed1h    int                // billed one-hour tokens
	Billed30m   *int               // billed thirty-minute tokens when positive total
	Billed12h   *int               // billed twelve-hour tokens when positive total
	Anomaly     *CacheWriteAnomaly // nil when reported counters were consistent
}

// EffectiveCacheWriteTotal returns the authoritative cache-write token count
// for context-window checks. A positive aggregate wins; when the aggregate is
// zero, positive TTL details supply the missing total without overflowing int.
func EffectiveCacheWriteTotal(tokens UsageTokens) int {
	if tokens.CacheCreationTokens > 0 {
		return tokens.CacheCreationTokens
	}
	if tokens.CacheCreationTokens < 0 {
		return 0
	}
	if tokens.CacheCreation30mTokens != nil && tokens.CacheCreation12hTokens != nil {
		short := max(*tokens.CacheCreation30mTokens, 0)
		long := max(*tokens.CacheCreation12hTokens, 0)
		return saturatingAddInt(short, long)
	}
	return saturatingAddInt(max(tokens.CacheCreation5mTokens, 0), max(tokens.CacheCreation1hTokens, 0))
}

// SettleCacheWrite computes cache-creation cost, billed TTL buckets, and
// anomaly diagnostics from resolved rates and reported usage. Callers supply
// finite nonnegative rates and multiplier; empty defaultFamily falls back to
// CacheWriteFamilyClaude when neither usage nor rates identify OpenAI.
//
// Untagged writes and positive gaps below the aggregate use the selected
// family's short bucket and rate. Contradictory details above the aggregate cap
// total billed tokens at the aggregate and allocate to the strictly pricier
// tier first, breaking equal-rate ties toward the default short tier. An
// incomplete OpenAI pointer pair with only one non-nil side falls back to the
// aggregate short rate while recording an anomaly.
// @project-doc docs/domains/routing_and_billing.md#cache_write_pricing
func SettleCacheWrite(rates CacheWriteRates, tokens UsageTokens, defaultFamily CacheWriteFamily, multiplier float64) CacheWriteSettlement {
	family := selectCacheWriteFamily(rates, tokens, defaultFamily)
	hasNegative := tokens.CacheCreationTokens < 0 ||
		tokens.CacheCreation5mTokens < 0 ||
		tokens.CacheCreation1hTokens < 0 ||
		(tokens.CacheCreation30mTokens != nil && *tokens.CacheCreation30mTokens < 0) ||
		(tokens.CacheCreation12hTokens != nil && *tokens.CacheCreation12hTokens < 0)

	total := max(tokens.CacheCreationTokens, 0)
	var short, long int
	var incompleteOpenAISplit bool
	shortRate := cacheWriteRate(rates.TTL5m, rates.Default)
	longRate := cacheWriteRate(rates.TTL1h, rates.Default)
	if family == CacheWriteFamilyOpenAI {
		shortRate = cacheWriteRate(rates.TTL30m, rates.Default)
		longRate = cacheWriteRate(rates.TTL12h, rates.Default)
		if tokens.CacheCreation30mTokens != nil && tokens.CacheCreation12hTokens != nil {
			short = max(*tokens.CacheCreation30mTokens, 0)
			long = max(*tokens.CacheCreation12hTokens, 0)
		} else if tokens.CacheCreation30mTokens != nil || tokens.CacheCreation12hTokens != nil {
			// A single non-nil OpenAI detail pointer omits the companion bucket needed
			// to verify the split; charge the full aggregate at the default short rate.
			incompleteOpenAISplit = true
		}
	} else {
		short = max(tokens.CacheCreation5mTokens, 0)
		long = max(tokens.CacheCreation1hTokens, 0)
	}

	var billedTotal, billedShort, billedLong int
	var anomalyKind string

	switch {
	case incompleteOpenAISplit:
		billedTotal = total
		billedShort = total
		if total > 0 || hasNegative ||
			(tokens.CacheCreation30mTokens != nil && *tokens.CacheCreation30mTokens != 0) ||
			(tokens.CacheCreation12hTokens != nil && *tokens.CacheCreation12hTokens != 0) {
			anomalyKind = CacheWriteAnomalyExceedsTotal
		}
	case total == 0 && short == 0 && long == 0:
		if hasNegative {
			anomalyKind = CacheWriteAnomalyExceedsTotal
		}
	case total == 0 && (short > 0 || long > 0):
		billedShort = short
		billedLong = long
		billedTotal = saturatingAddInt(short, long)
		anomalyKind = CacheWriteAnomalyMissingTotal
		if hasNegative {
			anomalyKind = CacheWriteAnomalyExceedsTotal
		}
	case short == 0 && long == 0 && !hasNegative:
		billedTotal = total
		billedShort = total
	case short <= total && long == total-short && !hasNegative:
		billedTotal = total
		billedShort = short
		billedLong = long
	case short <= total && long < total-short && !hasNegative:
		// Stream deltas can advance aggregate cache writes without repeating TTL
		// breakdown; assign the untagged remainder to the default short bucket.
		billedTotal = total
		billedShort = total - long
		billedLong = long
		anomalyKind = CacheWriteAnomalyPartialBreakdown
	default:
		// Contradictory details exceed the aggregate or include negative values;
		// cap billed tokens at total and allocate to the strictly pricier tier first.
		billedTotal = total
		if longRate > shortRate {
			billedLong = min(long, total)
			billedShort = total - billedLong
		} else {
			billedShort = min(short, total)
			billedLong = total - billedShort
		}
		anomalyKind = CacheWriteAnomalyExceedsTotal
	}

	out := CacheWriteSettlement{
		Cost:        (float64(billedShort)*shortRate + float64(billedLong)*longRate) * multiplier,
		BilledTotal: billedTotal,
		Family:      family,
	}
	if billedTotal > 0 {
		if family == CacheWriteFamilyOpenAI {
			out.Billed30m = new(billedShort)
			out.Billed12h = new(billedLong)
		} else {
			out.Billed5m = billedShort
			out.Billed1h = billedLong
		}
	}
	if anomalyKind != "" {
		out.Anomaly = &CacheWriteAnomaly{
			Kind:          anomalyKind,
			ReportedTotal: tokens.CacheCreationTokens,
			Reported5m:    tokens.CacheCreation5mTokens,
			Reported1h:    tokens.CacheCreation1hTokens,
			Reported30m:   copyOptional(tokens.CacheCreation30mTokens),
			Reported12h:   copyOptional(tokens.CacheCreation12hTokens),
		}
	}
	return out
}

// modelCacheWriteRates extracts resolved cache-write rates from ModelPricing.
// A supplied fallbackDefault overrides zero non-explicit base aggregate rates
// so direct ComputeCacheCreationCost callers can supply custom fallback prices.
func modelCacheWriteRates(pricing *ModelPricing, fallbackDefault float64) CacheWriteRates {
	if pricing == nil {
		return CacheWriteRates{Default: fallbackDefault}
	}
	def := fallbackDefault
	if def == 0 && !pricing.CacheCreationPriceExplicit && (pricing.SupportsCacheBreakdown || pricing.CacheCreation5mPriceExplicit) {
		def = pricing.CacheCreation5mPrice
	}
	var ttl5m, ttl1h *float64
	if pricing.CacheCreation5mPriceExplicit || (pricing.SupportsCacheBreakdown && pricing.CacheCreation5mPrice > 0) {
		ttl5m = new(pricing.CacheCreation5mPrice)
	}
	if pricing.SupportsCacheBreakdown {
		ttl1h = new(pricing.CacheCreation1hPrice)
	}
	return CacheWriteRates{
		Default: def,
		TTL5m:   ttl5m,
		TTL1h:   ttl1h,
		TTL30m:  pricing.CacheCreation30mPrice,
		TTL12h:  pricing.CacheCreation12hPrice,
	}
}

// statsCacheWriteRates extracts provider-statistics cache-write rates from a
// statistics card entry, using the configured short-TTL rate as default when no
// uniform CacheWritePrice is supplied.
func statsCacheWriteRates(p *ModelPricingEntry) CacheWriteRates {
	if p == nil {
		return CacheWriteRates{}
	}
	var def float64
	switch {
	case p.CacheWritePrice != nil:
		def = *p.CacheWritePrice
	case p.CacheWrite5mPrice != nil:
		def = *p.CacheWrite5mPrice
	case p.CacheWrite30mPrice != nil:
		def = *p.CacheWrite30mPrice
	}
	return CacheWriteRates{
		Default: def,
		TTL5m:   p.CacheWrite5mPrice,
		TTL1h:   p.CacheWrite1hPrice,
		TTL30m:  p.CacheWrite30mPrice,
		TTL12h:  p.CacheWrite12hPrice,
	}
}

// selectCacheWriteFamily chooses between 5m/1h and 30m/12h lifetimes using
// reported counters first, configured TTL rates second, and protocol fallback last.
func selectCacheWriteFamily(rates CacheWriteRates, tokens UsageTokens, defaultFamily CacheWriteFamily) CacheWriteFamily {
	hasOpenAIUsage := tokens.CacheCreation30mTokens != nil || tokens.CacheCreation12hTokens != nil
	hasClaudeUsage := tokens.CacheCreation5mTokens != 0 || tokens.CacheCreation1hTokens != 0
	if hasOpenAIUsage && !hasClaudeUsage {
		return CacheWriteFamilyOpenAI
	}
	if hasClaudeUsage && !hasOpenAIUsage {
		return CacheWriteFamilyClaude
	}
	hasOpenAIRates := rates.TTL30m != nil || rates.TTL12h != nil
	hasClaudeRates := rates.TTL5m != nil || rates.TTL1h != nil
	if hasOpenAIRates && !hasClaudeRates {
		return CacheWriteFamilyOpenAI
	}
	if hasClaudeRates && !hasOpenAIRates {
		return CacheWriteFamilyClaude
	}
	if defaultFamily == CacheWriteFamilyOpenAI {
		return CacheWriteFamilyOpenAI
	}
	return CacheWriteFamilyClaude
}

// cacheWriteRate selects an explicit optional amount or its aggregate fallback.
func cacheWriteRate(rate *float64, fallback float64) float64 {
	if rate == nil {
		return fallback
	}
	return *rate
}

// saturatingAddInt adds two nonnegative ints and clamps at math.MaxInt on overflow.
func saturatingAddInt(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

// applyCacheWriteCardOverrides applies uniform and per-TTL cache-write card or
// interval amounts at one pricing level. A uniform override replaces inherited
// TTL amounts unless that specific TTL is also supplied at the same level,
// while independent 5m or 30m overrides leave sibling long-TTL prices intact.
func applyCacheWriteCardOverrides(pricing *ModelPricing, uniform, p5m, p1h, p30m, p12h *float64) {
	if uniform != nil {
		priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *uniform)
		pricing.CacheCreationPricePerToken = *uniform
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *uniform
		pricing.CacheCreation5mPriceExplicit = false
		if p1h == nil {
			// A uniform card override without an accompanying 1h rate flattens both
			// Claude buckets so catalog 1h rates do not survive a uniform override.
			pricing.CacheCreation1hPrice = *uniform
			pricing.SupportsCacheBreakdown = false
		}
		if pricing.CacheCreation30mPrice != nil && p30m == nil {
			pricing.CacheCreation30mPrice = copyOptional(uniform)
		}
		if pricing.CacheCreation12hPrice != nil && p12h == nil {
			pricing.CacheCreation12hPrice = copyOptional(uniform)
		}
	}
	if p5m != nil {
		if uniform == nil && p30m == nil && pricing.CacheCreation30mPrice == nil && pricing.CacheCreation12hPrice == nil {
			priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *p5m)
			pricing.CacheCreationPricePerToken = *p5m
			pricing.CacheCreationPricePerTokenPriority = priority
			pricing.CacheCreationPriceExplicit = true
		}
		pricing.CacheCreation5mPrice = *p5m
		pricing.CacheCreation5mPriceExplicit = true
	}
	if p1h != nil {
		pricing.CacheCreation1hPrice = *p1h
		pricing.SupportsCacheBreakdown = true
	}
	if p30m != nil {
		if uniform == nil && p5m == nil && !pricing.SupportsCacheBreakdown && !pricing.CacheCreation5mPriceExplicit {
			priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *p30m)
			pricing.CacheCreationPricePerToken = *p30m
			pricing.CacheCreationPricePerTokenPriority = priority
			pricing.CacheCreationPriceExplicit = true
		}
		pricing.CacheCreation30mPrice = copyOptional(p30m)
	}
	if p12h != nil {
		pricing.CacheCreation12hPrice = copyOptional(p12h)
	}
}

// displayClaudeWriteRates returns rate-scaled 5m and 1h pointers when the price
// record exposes Claude TTL split pricing, preserving explicit zero amounts.
func displayClaudeWriteRates(pricing *ModelPricing, multiplier float64) (*float64, *float64) {
	if pricing == nil || (!pricing.SupportsCacheBreakdown && !pricing.CacheCreation5mPriceExplicit) {
		return nil, nil
	}
	short := pricing.CacheCreationPricePerToken
	if pricing.CacheCreation5mPriceExplicit || (pricing.SupportsCacheBreakdown && pricing.CacheCreation5mPrice > 0) {
		short = pricing.CacheCreation5mPrice
	}
	long := short
	if pricing.SupportsCacheBreakdown {
		long = pricing.CacheCreation1hPrice
	}
	return new(short * multiplier), new(long * multiplier)
}

// displayOpenAIWriteRates includes effective amounts only for prices that
// expose the GPT split. An absent sibling uses the same uniform fallback as
// settlement, and both pointers preserve an effective zero for presentation.
func displayOpenAIWriteRates(pricing *ModelPricing, multiplier float64) (*float64, *float64) {
	if pricing == nil || (pricing.CacheCreation30mPrice == nil && pricing.CacheCreation12hPrice == nil) {
		return nil, nil
	}
	return new(cacheWriteRate(pricing.CacheCreation30mPrice, pricing.CacheCreationPricePerToken) * multiplier),
		new(cacheWriteRate(pricing.CacheCreation12hPrice, pricing.CacheCreationPricePerToken) * multiplier)
}
