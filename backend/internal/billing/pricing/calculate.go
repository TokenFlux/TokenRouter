package pricing

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// calculate.go calculates usage charges and builds display prices from
// resolved pricing.
// Token amounts depend on the selected context range, service tier, cache
// detail, and reported image subsets. A request can contain overlapping
// representations, such as image input inside aggregate input or TTL details
// inside cache creation, so adding every reported count independently would
// charge those tokens twice.
//
// ComputeTokenBreakdown chooses the applicable price record and splits the
// overlapping image counts before totaling USD component amounts. Catalog tiers
// or eligible built-in multipliers price the whole usage, while configured
// intervals suppress those built-in rules. Display construction uses the same
// price selection inputs and preserves unpriced gaps rather than flattening
// them into free ranges. Time and effort factors are applied to token costs
// separately from request or media-unit charging.

// These OpenAI wire labels identify Fast service tiers for billing consumers;
// priority is used when querying the calculated Fast display view.
const (
	OpenAIFastTierPriority  = openai.ServiceTierPriority  // priority service-tier wire label
	OpenAIFastTierUltrafast = openai.ServiceTierUltrafast // ultrafast service-tier wire label
)

// ErrModelPricingUnavailable lets callers identify that the chosen model or
// requested pricing bucket has no usable amount available.
var ErrModelPricingUnavailable = errors.New("pricing not found")

// ResolvedTokenPriceRange contains one positive context range and its effective
// token prices. Nil pricing retains a missing-price gap for uniformity checks,
// while minTokens and maxTokens use the same excluded-lower, included-upper
// ranges as interval selection.
type ResolvedTokenPriceRange struct {
	minTokens int // excluded lower context-token endpoint
	// maxTokens is the included upper endpoint, with nil denoting an unbounded
	// range. Range construction can retain a configured interval's endpoint
	// storage.
	maxTokens *int // included upper context-token endpoint
	// pricing is nil for an unavailable range, including a gap lacking default
	// prices. Uniform display checks keep that absence distinct from a free
	// amount.
	pricing *ModelPricing // effective token-price range record
}

// NormalizeBillingServiceTier trims surrounding whitespace and lowercases the
// supplied tier.
func NormalizeBillingServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}

// UsePriorityServiceTierPricing reports whether this request selects catalog
// priority amounts. The normalized tier must be priority or fast, and either
// Fast multiplier pointer disables this path, including a pointer to zero.
//
// At least one priority presence flag or positive legacy priority amount
// enables selection. Presence flags retain explicit free buckets; an individual
// bucket with neither presence nor a positive amount continues to use its
// standard price during calculation.
func UsePriorityServiceTierPricing(serviceTier string, pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	tier := NormalizeBillingServiceTier(serviceTier)
	if tier != "priority" && tier != "fast" {
		return false
	}
	if pricing.FastModeMultiplier != nil || pricing.FastMultiplier != nil {
		return false
	}
	return pricing.PriorityInputPresent || pricing.PriorityOutputPresent || pricing.PriorityCacheReadPresent || pricing.PriorityCacheWritePresent || pricing.InputPricePerTokenPriority > 0 || pricing.OutputPricePerTokenPriority > 0 ||
		pricing.CacheCreationPricePerTokenPriority > 0 || pricing.CacheReadPricePerTokenPriority > 0
}

// NormalizedFastModeMultiplier returns a Fast factor and whether it was
// configured. FastModeMultiplier takes precedence over FastMultiplier. Nil
// pricing or absent pointers yield one with false; a supplied negative value
// yields zero with true. Callers validate finiteness.
func NormalizedFastModeMultiplier(pricing *ModelPricing) (float64, bool) {
	if pricing == nil {
		return 1, false
	}
	configured := pricing.FastModeMultiplier
	if configured == nil {
		configured = pricing.FastMultiplier
	}
	if configured == nil {
		return 1, false
	}
	// Keep the configured factor present after clamping so negative cached values
	// cannot reactivate catalog priority amounts through an absent-factor path.
	if *configured < 0 {
		return 0, true
	}
	return *configured, true
}

// ConfiguredServiceTierMultiplier returns the configured Fast or Flex factor
// for serviceTier. Priority and fast use NormalizedFastModeMultiplier; flex
// reads FlexMultiplier directly. Absent settings and other tiers return one,
// and callers validate supplied factors.
func ConfiguredServiceTierMultiplier(serviceTier string, pricing *ModelPricing) float64 {
	if pricing != nil {
		switch NormalizeBillingServiceTier(serviceTier) {
		case "priority", "fast":
			if multiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
				return multiplier
			}
		case "flex":
			if pricing.FlexMultiplier != nil {
				return *pricing.FlexMultiplier
			}
		}
	}
	return 1
}

// ApplyConfigFastModeMultiplier copies the selected card Fast factor into
// pricing. FastMultiplier takes precedence over the legacy FastModeMultiplier,
// negative values become zero, and both resolved pointer fields share the newly
// allocated factor. Nil inputs leave pricing unchanged.
func ApplyConfigFastModeMultiplier(pricing *ModelPricing, configPricing *ModelPricingEntry) {
	if pricing == nil || configPricing == nil {
		return
	}
	multiplierPtr := configPricing.FastMultiplier
	if multiplierPtr == nil {
		multiplierPtr = configPricing.FastModeMultiplier
	}
	if multiplierPtr == nil {
		return
	}
	multiplier := *multiplierPtr
	// Store a zero factor for a negative card amount while keeping its presence,
	// so later tier selection continues to use the configured Fast path.
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FastModeMultiplier = &multiplier
	pricing.FastMultiplier = &multiplier
}

// ApplyConfigFlexMultiplier copies a supplied card Flex factor into pricing.
// Nil inputs or an omitted factor leave pricing unchanged; a negative factor
// becomes zero. The resolved field receives separate value storage.
func ApplyConfigFlexMultiplier(pricing *ModelPricing, configPricing *ModelPricingEntry) {
	if pricing == nil || configPricing == nil || configPricing.FlexMultiplier == nil {
		return
	}
	multiplier := *configPricing.FlexMultiplier
	// Keep the supplied Flex factor present while clamping so later flex
	// calculation remains a configured free path.
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FlexMultiplier = &multiplier
}

// ApplyCostBreakdownMultiplier scales all six component costs and both totals
// in place. Nil breakdowns and a factor of one are unchanged. Callers validate
// the factor; BillingMode and LongContextBillingApplied retain their existing
// values.
func ApplyCostBreakdownMultiplier(cost *CostBreakdown, multiplier float64) {
	if cost == nil || multiplier == 1 {
		return
	}
	cost.InputCost *= multiplier
	cost.ImageInputCost *= multiplier
	cost.OutputCost *= multiplier
	cost.ImageOutputCost *= multiplier
	cost.CacheCreationCost *= multiplier
	cost.CacheReadCost *= multiplier
	cost.TotalCost *= multiplier
	cost.ActualCost *= multiplier
}

// MaxReasoningEffortBillingMultiplier returns a positive configured factor for
// normalized max effort. Other efforts, absent pricing, absent factors, and
// nonpositive factors return one. The model argument does not participate in
// selection, and callers validate finiteness.
func MaxReasoningEffortBillingMultiplier(model, effort string, pricing *ModelPricing) float64 {
	if protocol.NormalizeClaudeOutputEffort(effort) == nil || !strings.EqualFold(strings.TrimSpace(effort), "max") {
		return 1
	}
	if pricing != nil && pricing.MaxReasoningEffortMultiplier != nil && *pricing.MaxReasoningEffortMultiplier > 0 {
		return *pricing.MaxReasoningEffortMultiplier
	}
	return 1
}

// ConfigTierOverridePrice derives a priority amount from a replaced standard
// amount. Both catalog amounts must be positive to supply a tier-to-standard
// ratio. Otherwise the result is zero, including when the standard amount is
// free or absent.
//
// The configured standard amount is multiplied by that ratio, so an explicit
// free override also produces a free priority amount. Callers retain presence
// information separately and supply finite validated amounts.
func ConfigTierOverridePrice(baseStandard, baseTier, configStandard float64) float64 {
	if baseStandard > 0 && baseTier > 0 {
		return configStandard * (baseTier / baseStandard)
	}
	return 0
}

// ApplyConfigTokenPriceOverrides replaces supplied text and cache defaults
// throughout pricing. It allocates new context-tier records before recursion,
// so scalar overrides do not mutate the supplied tier records. Callers supply
// non-nil tier prices and validated card amounts; nil top-level inputs are
// ignored.
//
// Omitted text and cache buckets retain their selected base values, while
// replaced standard buckets derive priority amounts from the previous ratio. A
// cache-write override covers both TTLs unless a separate hourly amount is
// supplied. Card image rules apply to every resulting tier: absent or zero
// image input uses text input, and absent image output becomes free.
func ApplyConfigTokenPriceOverrides(pricing *ModelPricing, configPricing *ModelPricingEntry) {
	if pricing == nil || configPricing == nil {
		return
	}
	// Context tiers hold independent absolute amounts; copy each tier record
	// before applying card defaults so omitted buckets retain that tier's own
	// catalog values.
	if len(pricing.ContextPrices) > 0 {
		tiers := make([]ContextModelPrice, len(pricing.ContextPrices))
		for i, tier := range pricing.ContextPrices {
			value := *tier.Pricing
			ApplyConfigTokenPriceOverrides(&value, configPricing)
			tiers[i] = ContextModelPrice{Threshold: tier.Threshold, Pricing: &value}
		}
		pricing.ContextPrices = tiers
	}
	if configPricing.InputPrice != nil {
		priority := ConfigTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *configPricing.InputPrice)
		pricing.InputPricePerToken = *configPricing.InputPrice
		pricing.InputPricePerTokenPriority = priority
	}
	if configPricing.OutputPrice != nil {
		priority := ConfigTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *configPricing.OutputPrice)
		pricing.OutputPricePerToken = *configPricing.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	}
	if configPricing.CacheWritePrice != nil {
		basePriority := pricing.CacheCreationPricePerTokenPriority
		priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, basePriority, *configPricing.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *configPricing.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *configPricing.CacheWritePrice
		if configPricing.CacheWrite1hPrice == nil {
			// A short cache override replaces both TTL amounts when hourly is absent,
			// keeping old single-price cards from inheriting an unrelated hourly catalog
			// price.
			pricing.CacheCreation1hPrice = *configPricing.CacheWritePrice
		}
	}
	if configPricing.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *configPricing.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if configPricing.CacheReadPrice != nil {
		priority := ConfigTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *configPricing.CacheReadPrice)
		pricing.CacheReadPricePerToken = *configPricing.CacheReadPrice
		// A manual aggregate cache price also owns the image subset; retaining the
		// catalog image rate would partially bypass this administrator override.
		pricing.ImageCacheReadPriceExplicit = false
		pricing.CacheReadPricePerTokenPriority = priority
	}
	applyConfigImagePriceOverrides(pricing, configPricing)
}

// CalculateTokenCost prices reported usage with the matching configured context
// interval. Context counts input, cache creation, and cache reads; callers
// supply a non-nil resolved record, valid counts whose sum fits int, and finite
// amounts. Missing effective pricing returns ErrModelPricingUnavailable.
//
// Configured intervals suppress built-in long-context charging.
// ComputeTokenBreakdown selects the service tier and applies the user rate;
// catalog time rules then apply only for catalog-source resolution. Card time
// rules use their separate instant and location, and final max effort scales
// the resulting components and totals.
func CalculateTokenCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens

	pricing := GetIntervalPricing(resolved, totalContext)
	if pricing == nil {
		return nil, fmt.Errorf("no pricing available for model: %s: %w", input.Model, ErrModelPricingUnavailable)
	}

	// A nonempty configured interval list owns context pricing even for unmatched
	// ranges, so built-in thresholds are suppressed while defaults handle those
	// gaps.
	applyLongCtx := len(resolved.Intervals) == 0 && resolved.LongContextPricingEnabled

	breakdown := ComputeTokenBreakdown(pricing, input.Tokens, input.RateMultiplier, input.ServiceTier, applyLongCtx)
	if resolved.Source == PricingSourceCatalog && pricing.TimePricing != nil {
		ApplyCostBreakdownMultiplier(breakdown, pricing.TimePricing.MultiplierAt(input.ModelPricingAt, input.ModelTimeLocation))
	}
	ApplyCostBreakdownMultiplier(breakdown, ResolvedTimeMultiplier(resolved, input.PricingAt, input.TimePricingLocation))
	ApplyCostBreakdownMultiplier(breakdown, MaxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, pricing))
	return breakdown, nil
}

// ComputeTokenBreakdown returns token component costs and a rate-scaled total.
// Callers supply non-nil prices, nonnegative counts whose context sums fit int,
// and finite amounts. Context tiers are ordered by ascending threshold with
// non-nil prices; the last exceeded tier prices the entire usage.
//
// A configured Fast factor scales standard buckets; otherwise available
// priority buckets replace their standard counterparts. Built-in long-context
// multipliers apply when enabled and eligible, with input-side scaling also
// passed to cache creation. Absolute context tiers inherit the base
// service-tier modifiers before recursive calculation.
//
// Image input is split from InputTokens and capped there; image output is
// charged separately while text output is reduced to at least zero. TTL cache
// details are normalized against their aggregate when supported. Negative user
// rates become zero, and LongContextBillingApplied records an increase over the
// corresponding base-rate charge.
func ComputeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	if applyLongCtx && len(pricing.ContextPrices) > 0 {
		input := tokens.InputTokens + tokens.CacheReadTokens + tokens.CacheCreationTokens
		selected := pricing
		for _, tier := range pricing.ContextPrices {
			if input > tier.Threshold {
				selected = tier.Pricing
			}
		}
		// The absolute tier supplies bucket amounts, while the base keeps current
		// Fast, Flex and effort choices. Copy that selected tier before recursive
		// calculation and comparison with the base charge.
		if selected != pricing {
			value := *selected
			value.FastModeMultiplier = pricing.FastModeMultiplier
			value.FastMultiplier = pricing.FastMultiplier
			value.FlexMultiplier = pricing.FlexMultiplier
			value.MaxReasoningEffortMultiplier = pricing.MaxReasoningEffortMultiplier
			cost := ComputeTokenBreakdown(&value, tokens, rateMultiplier, serviceTier, false)
			cost.LongContextBillingApplied = cost.ActualCost > ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, false).ActualCost
			return cost
		}
	}
	// A negative user rate would reverse a positive token charge; normalize that
	// rate to zero while retaining the calculated base component costs.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	inputPrice := pricing.InputPricePerToken
	outputPrice := pricing.OutputPricePerToken
	cacheReadPrice := pricing.CacheReadPricePerToken
	cacheCreationPrice := pricing.CacheCreationPricePerToken
	cacheCreationMultiplier := 1.0
	imageCacheMultiplier := 1.0
	tierMultiplier := 1.0

	tier := NormalizeBillingServiceTier(serviceTier)
	if tier == "priority" || tier == "fast" {
		if fastMultiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
			// A configured Fast factor scales standard amounts as one selected path;
			// applying catalog priority amounts too would charge two service-tier
			// adjustments.
			tierMultiplier = fastMultiplier
		} else if UsePriorityServiceTierPricing(serviceTier, pricing) {
			if pricing.InputPricePerTokenPriority > 0 || pricing.PriorityInputPresent {
				inputPrice = pricing.InputPricePerTokenPriority
			}
			if pricing.OutputPricePerTokenPriority > 0 || pricing.PriorityOutputPresent {
				outputPrice = pricing.OutputPricePerTokenPriority
			}
			if pricing.CacheReadPricePerTokenPriority > 0 || pricing.PriorityCacheReadPresent {
				cacheReadPrice = pricing.CacheReadPricePerTokenPriority
			}
			if pricing.CacheCreationPricePerTokenPriority > 0 || pricing.PriorityCacheWritePresent {
				cacheCreationPrice = pricing.CacheCreationPricePerTokenPriority
			}
		} else {
			tierMultiplier = ConfiguredServiceTierMultiplier(serviceTier, pricing)
		}
	} else {
		tierMultiplier = ConfiguredServiceTierMultiplier(serviceTier, pricing)
	}

	longContextPricingEligible := applyLongCtx && ShouldApplySessionLongContextPricing(tokens, pricing)
	var baselineCost *CostBreakdown
	if longContextPricingEligible {
		baselineCost = ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, false)
		// A partial long-context rule can supply one side alone; using a unit factor
		// for the other side retains its selected standard or priority amount.
		longContextInputMultiplier := LongContextMultiplierOrOne(pricing.LongContextInputMultiplier)
		inputPrice *= longContextInputMultiplier
		outputPrice *= LongContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
		// Cache-read usage participates in the input-context threshold and receives
		// the selected input factor, so cached input follows that same long-context
		// amount rule.
		cacheReadPrice *= longContextInputMultiplier
		imageCacheMultiplier = longContextInputMultiplier
		// ComputeCacheCreationCost selects aggregate or TTL rates from pricing
		// directly; pass the input factor separately so whichever cache rate is
		// chosen receives the long-context adjustment.
		cacheCreationMultiplier = longContextInputMultiplier
	}

	bd := &CostBreakdown{}
	// Split reported image input from the aggregate input bucket before charging.
	// Separate text and image components keep reconciliation able to identify
	// their rates without adding the image subset twice.
	if tokens.ImageInputTokens > 0 {
		imageInputTokens := max(tokens.ImageInputTokens-tokens.ImageCacheReadTokens, 0)
		textInputTokens := tokens.InputTokens - imageInputTokens
		// Reported image input can exceed its aggregate; cap the image subset at that
		// aggregate so splitting cannot create a negative text-input component.
		if textInputTokens < 0 {
			textInputTokens = 0
			imageInputTokens = tokens.InputTokens
		}
		imageInputPrice := pricing.ImageInputPricePerToken
		if imageInputPrice == 0 {
			imageInputPrice = inputPrice
		}
		bd.InputCost = float64(textInputTokens) * inputPrice
		bd.ImageInputCost = float64(imageInputTokens) * imageInputPrice
	} else {
		bd.InputCost = float64(tokens.InputTokens) * inputPrice
	}

	// Keep text and image output in distinct recorded buckets; remove the image
	// count from aggregate output and cap the remaining text count at zero.
	textOutputTokens := max(tokens.OutputTokens-tokens.ImageOutputTokens, 0)
	bd.OutputCost = float64(textOutputTokens) * outputPrice

	// Image output has its own amount and presence marker; an omitted zero amount
	// can inherit text pricing, while an explicitly present zero remains free.
	if tokens.ImageOutputTokens > 0 {
		imgPrice := pricing.ImageOutputPricePerToken
		if imgPrice == 0 && !pricing.ImageOutputPriceExplicit {
			imgPrice = outputPrice
		}
		bd.ImageOutputCost = float64(tokens.ImageOutputTokens) * imgPrice
	}

	// When priority cache creation is present and standard pricing is positive,
	// its ratio scales both TTL amounts so one selected priority rule covers both
	// durations.
	if UsePriorityServiceTierPricing(serviceTier, pricing) && pricing.SupportsCacheBreakdown && pricing.CacheCreationPricePerToken > 0 && pricing.PriorityCacheWritePresent {
		cacheCreationMultiplier *= cacheCreationPrice / pricing.CacheCreationPricePerToken
	}
	// The cache selector now receives the selected aggregate amount and
	// accumulated context or priority ratio, allowing its TTL choice to apply
	// those adjustments once.
	bd.CacheCreationCost = ComputeCacheCreationCost(pricing, tokens, cacheCreationPrice, cacheCreationMultiplier)

	imageCached := min(max(tokens.ImageCacheReadTokens, 0), max(tokens.CacheReadTokens, 0))
	imageCachePrice := cacheReadPrice
	if pricing.ImageCacheReadPriceExplicit {
		imageCachePrice = pricing.ImageCacheReadPricePerToken * imageCacheMultiplier
	}
	bd.CacheReadCost = float64(tokens.CacheReadTokens-imageCached)*cacheReadPrice + float64(imageCached)*imageCachePrice

	if tierMultiplier != 1.0 {
		bd.InputCost *= tierMultiplier
		bd.ImageInputCost *= tierMultiplier
		bd.OutputCost *= tierMultiplier
		bd.ImageOutputCost *= tierMultiplier
		bd.CacheCreationCost *= tierMultiplier
		bd.CacheReadCost *= tierMultiplier
	}

	bd.TotalCost = bd.InputCost + bd.ImageInputCost + bd.OutputCost + bd.ImageOutputCost +
		bd.CacheCreationCost + bd.CacheReadCost
	bd.ActualCost = bd.TotalCost * rateMultiplier
	bd.LongContextBillingApplied = baselineCost != nil && bd.ActualCost > baselineCost.ActualCost

	return bd
}

// ComputeCacheCreationCost returns aggregate or TTL-specific cache creation
// costs with multiplier applied to the selected rates. Callers supply non-nil
// pricing and finite amounts and factors. TTL rates are eligible only when
// breakdown support is enabled and at least one TTL rate is positive.
//
// Eligible TTL pricing uses normalized details, or charges positive aggregate
// usage at the 5m rate when both details are absent. Otherwise the supplied
// aggregate price parameter determines the charge. The same multiplier scales
// either path, carrying the caller's selected context and priority adjustments.
func ComputeCacheCreationCost(pricing *ModelPricing, tokens UsageTokens, price, multiplier float64) float64 {
	if pricing.SupportsCacheBreakdown && (pricing.CacheCreation5mPrice > 0 || pricing.CacheCreation1hPrice > 0) {
		cacheCreation5mTokens, cacheCreation1hTokens := NormalizeCacheCreationBreakdown(tokens)
		if cacheCreation5mTokens == 0 && cacheCreation1hTokens == 0 && tokens.CacheCreationTokens > 0 {
			// Aggregate usage can arrive without TTL details; the short-TTL amount
			// retains reported creation charges without inventing a count of hourly
			// tokens.
			return float64(tokens.CacheCreationTokens) * pricing.CacheCreation5mPrice * multiplier
		}
		return float64(cacheCreation5mTokens)*pricing.CacheCreation5mPrice*multiplier +
			float64(cacheCreation1hTokens)*pricing.CacheCreation1hPrice*multiplier
	}
	// TTL pricing is unavailable under the support or rate condition; retain the
	// caller's selected aggregate amount, including a free or priority price.
	return float64(tokens.CacheCreationTokens) * price * multiplier
}

// NormalizeCacheCreationBreakdown returns nonnegative 5m and 1h token details.
// Negative detail counts become zero. A positive aggregate caps only an
// overreported detail sum; a nonpositive aggregate leaves the remaining details
// available because it supplies no positive cap.
//
// For overreported details, the 5m share is proportionally rounded and the 1h
// count receives the aggregate remainder. Comparing the rounded value with the
// aggregate before integer conversion keeps the returned counts within that cap
// despite float64 rounding near large integer values.
func NormalizeCacheCreationBreakdown(tokens UsageTokens) (int, int) {
	cacheCreation5mTokens := tokens.CacheCreation5mTokens
	cacheCreation1hTokens := tokens.CacheCreation1hTokens
	aggregate := tokens.CacheCreationTokens
	// Discard negative details before comparing their sum with the aggregate cap.
	if cacheCreation5mTokens < 0 {
		cacheCreation5mTokens = 0
	}
	if cacheCreation1hTokens < 0 {
		cacheCreation1hTokens = 0
	}
	if aggregate <= 0 || (cacheCreation5mTokens <= aggregate && cacheCreation1hTokens <= aggregate-cacheCreation5mTokens) {
		return cacheCreation5mTokens, cacheCreation1hTokens
	}

	// Use floating detail totals to avoid integer addition overflow while
	// estimating proportional counts that will be returned as integers.
	detailTotal := float64(cacheCreation5mTokens) + float64(cacheCreation1hTokens)
	// Round the short-TTL share and give hourly usage the remainder.
	normalized5mTokens := math.Round(float64(aggregate) * float64(cacheCreation5mTokens) / detailTotal)
	// Near int limits, a rounded float may reach the aggregate's rounded
	// representation; return the original integer cap before any potentially
	// oversized conversion.
	if normalized5mTokens >= float64(aggregate) {
		cacheCreation5mTokens = aggregate
	} else {
		cacheCreation5mTokens = int(normalized5mTokens)
	}
	return cacheCreation5mTokens, aggregate - cacheCreation5mTokens
}

// CalculatePerRequestCost returns a unit-priced total for requests or media
// usage. Positive UsageUnits takes precedence over RequestCount; otherwise a
// nonpositive count becomes one. Callers provide non-nil resolution, finite
// rates, and token counts whose context sum fits int.
//
// Label selection precedes unlabeled context intervals and the explicit
// default. Context is supplied here, so request tiers can vary with actual
// input and cache usage. An unavailable price returns
// ErrModelPricingUnavailable; an explicit zero remains available. This helper
// multiplies the rate as supplied, while CalculateCost performs negative-rate
// normalization.
func CalculatePerRequestCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	units := input.UsageUnits
	if units <= 0 {
		count := input.RequestCount
		if count <= 0 {
			count = 1
		}
		units = float64(count)
	}

	totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens
	unitPrice, found := ResolveRequestUnitPrice(resolved, input.SizeTier, &totalContext)
	if !found {
		return nil, fmt.Errorf("%w for request price: %s", ErrModelPricingUnavailable, input.Model)
	}

	totalCost := unitPrice * units
	actualCost := totalCost * input.RateMultiplier

	return &CostBreakdown{
		TotalCost:  totalCost,
		ActualCost: actualCost,
	}, nil
}

// LongContextMultiplierOrOne returns a positive multiplier or the unit factor.
// Nonpositive values select one so an omitted long-context input or output
// factor leaves its corresponding base prices chargeable. Callers validate
// finiteness.
func LongContextMultiplierOrOne(multiplier float64) float64 {
	if multiplier <= 0 {
		return 1
	}
	return multiplier
}

// ShouldApplySessionLongContextPricing reports eligibility for a built-in
// multiplier tier. Pricing needs a positive threshold and at least one input or
// output factor above one. Callers supply nonnegative usage whose
// input-plus-cache sum fits int.
//
// Eligibility counts InputTokens, CacheCreationTokens, and CacheReadTokens,
// excluding output and duplicate image subsets. Inclusive thresholds admit the
// threshold itself; other thresholds require a larger total. Absolute catalog
// context tiers are selected separately by ComputeTokenBreakdown.
func ShouldApplySessionLongContextPricing(tokens UsageTokens, pricing *ModelPricing) bool {
	if pricing == nil || pricing.LongContextInputThreshold <= 0 {
		return false
	}
	if pricing.LongContextInputMultiplier <= 1 && pricing.LongContextOutputMultiplier <= 1 {
		return false
	}
	totalInputTokens := tokens.InputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
	if pricing.LongContextThresholdInclusive {
		return totalInputTokens >= pricing.LongContextInputThreshold
	}
	return totalInputTokens > pricing.LongContextInputThreshold
}

// DisplayPricingFromResolved returns a priced display snapshot and whether it
// is usable. Token mode tries one complete uniform price first, then separate
// priced intervals; disabled built-in context rules are removed from the
// displayed base copy. Unpriced gaps are omitted, and configured effort and
// valid time rules are attached separately.
//
// Image and per-request displays require configuration-source prices.
// Per-request mode also needs explicitly labeled image dimensions, then quotes
// only fixed available 1K, 2K, and 4K amounts. Video mode exposes configured
// per-second labels and its optional default; unsupported modes return false. Callers supply a finite rate already normalized for display.
func DisplayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	if resolved == nil {
		return ModelDisplayPricing{}, false
	}

	switch resolved.Mode {
	case BillingModeVideo:
		return BuildVideoDisplayPricing(resolved, nil, rateMultiplier)
	case BillingModeToken:
		pricing := ResolvedDisplayTokenPricing(resolved)
		if pricing != nil && !resolved.LongContextPricingEnabled {
			pricing = WithoutLongContextDisplayPricing(pricing)
		}
		if pricing != nil && (HasAnyDisplayTokenPricing(pricing) || resolved.HasEffectiveOverridePricing()) {
			return withTokenDisplayModifiers(BuildTokenDisplayPricing(pricing, rateMultiplier), resolved), true
		}
		intervals := ResolvedDisplayPricingIntervals(resolved, rateMultiplier)
		if len(intervals) > 0 {
			return withTokenDisplayModifiers(BuildTokenIntervalDisplayPricing(intervals), resolved), true
		}
		return ModelDisplayPricing{}, false
	case BillingModeImage, BillingModePerRequest:
		if resolved.Source != PricingSourceConfig {
			return ModelDisplayPricing{}, false
		}
		if resolved.Mode == BillingModePerRequest {
			imageDimensions := false
			for _, tier := range resolved.RequestTiers {
				if slices.ContainsFunc([]string{"512", "1K", "2K", "4K"}, func(size string) bool { return strings.EqualFold(strings.TrimSpace(tier.TierLabel), size) }) {
					imageDimensions = true
				}
			}
			if !imageDimensions {
				return ModelDisplayPricing{}, false
			}
		}
		result := ModelDisplayPricing{PricingMode: "image", PriceStatus: "priced"}
		prices := []*float64{&result.ImagePrice1K, &result.ImagePrice2K, &result.ImagePrice4K}
		for i, size := range []string{"1K", "2K", "4K"} {
			value, found := ConfiguredImageUnitPrice(resolved, size)
			if !found {
				continue
			}
			*prices[i] = value * rateMultiplier
			result.ImagePriceSizes = append(result.ImagePriceSizes, size)
		}
		return result, len(result.ImagePriceSizes) > 0
	default:
		return ModelDisplayPricing{}, false
	}
}

// withTokenDisplayModifiers adds the base effort factor and card schedules with
// valid periods. It requires non-nil resolution, copies the positive non-unit
// effort amount, and clones nonempty card time rules after period validation.
// The billing facade loads the timezone; existing display collections keep
// their references.
func withTokenDisplayModifiers(display ModelDisplayPricing, resolved *ResolvedPricing) ModelDisplayPricing {
	if resolved.BasePricing != nil {
		multiplier := resolved.BasePricing.MaxReasoningEffortMultiplier
		if multiplier != nil && *multiplier > 0 && *multiplier != 1 {
			value := *multiplier
			display.MaxReasoningEffortMultiplier = &value
		}
	}
	if resolved.ConfigPricing != nil {
		config := resolved.ConfigPricing.TimePricing
		// Omit an invalid or empty schedule from display metadata while keeping the
		// available token amounts. Timezone loading remains separate from this
		// period-validity check.
		if config != nil && len(config.Periods) > 0 && ValidateTimePricingConfig(config) == nil {
			display.TimePricing = resolved.ConfigPricing.Clone().TimePricing
		}
	}
	return display
}

// WithoutLongContextDisplayPricing returns a shallow price copy with built-in
// context rules cleared. Nil input returns nil; amount fields retain their
// values. Remaining multiplier and time pointers stay shared and require stable
// source data.
func WithoutLongContextDisplayPricing(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	cloned := *pricing
	cloned.LongContextInputThreshold = 0
	cloned.ContextPrices = nil
	cloned.LongContextInputMultiplier = 0
	cloned.LongContextOutputMultiplier = 0
	return &cloned
}

// ResolvedTokenPriceRanges builds positive context ranges around configured
// token intervals. Callers supply validated, nonoverlapping ranges. Nil
// resolution or absent intervals returns nil. It sorts a copied interval
// slice and fills gaps with available defaults, retaining nil
// pricing where a default is unavailable.
//
// The final unbounded interval ends construction; otherwise an unbounded
// default range follows the last endpoint. Each configured range uses the same
// inherited price conversion as settlement. Endpoint pointers and default-price
// references can remain shared with the resolved inputs, which callers keep
// stable during display construction.
func ResolvedTokenPriceRanges(resolved *ResolvedPricing) []ResolvedTokenPriceRange {
	if resolved == nil || len(resolved.Intervals) == 0 {
		return nil
	}
	intervals := append([]PricingInterval(nil), resolved.Intervals...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].MinTokens < intervals[j].MinTokens })
	ranges := make([]ResolvedTokenPriceRange, 0, len(intervals)*2+1)
	cursor := 0
	for i := range intervals {
		interval := &intervals[i]
		// A gap uses the available default with built-in tiers cleared; an absent
		// default remains nil so the display can retain unpriced context coverage.
		if interval.MinTokens > cursor {
			maxTokens := interval.MinTokens
			ranges = append(ranges, ResolvedTokenPriceRange{cursor, &maxTokens, WithoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
		}
		ranges = append(ranges, ResolvedTokenPriceRange{interval.MinTokens, interval.MaxTokens, resolved.tokenPricingForInterval(interval)})
		if interval.MaxTokens == nil {
			return ranges
		}
		cursor = *interval.MaxTokens
	}
	// The remaining unbounded context uses the same available default, retaining
	// nil pricing when no default can cover usage beyond the last configured
	// interval.
	return append(ranges, ResolvedTokenPriceRange{cursor, nil, WithoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
}

// ResolvedDisplayTokenPricing returns one token price when every positive range
// is equally priced. Absent intervals use available base pricing directly. With
// intervals, any unpriced gap or unequal standard or Fast display bucket
// prevents a uniform result.
//
// Comparison uses unit-rate display intervals, so separate TTL cache prices
// participate together with text and image buckets. The result is the first
// range's price reference and may remain shared with resolution; callers treat
// it as read-only or copy it before mutation.
func ResolvedDisplayTokenPricing(resolved *ResolvedPricing) *ModelPricing {
	if resolved == nil {
		return nil
	}
	if len(resolved.Intervals) == 0 {
		return resolved.tokenPricingForInterval(nil)
	}
	ranges := ResolvedTokenPriceRanges(resolved)
	pricing := ranges[0].pricing
	// A flat display promises one amount across every positive context; an
	// unpriced gap or unequal standard/Fast bucket prevents that promise from
	// covering all ranges.
	for _, priceRange := range ranges {
		if priceRange.pricing == nil || !SameDisplayTokenPricing(pricing, priceRange.pricing) {
			return nil
		}
	}
	return pricing
}

// ResolvedDisplayPricingIntervals converts available positive context ranges
// into display records. Unpriced ranges are omitted, retaining their absence
// rather than inventing zero prices. Amounts include rateMultiplier, and
// upper-endpoint pointers can remain shared with resolution.
func ResolvedDisplayPricingIntervals(resolved *ResolvedPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	var intervals []ModelDisplayPricingInterval
	for _, priceRange := range ResolvedTokenPriceRanges(resolved) {
		if priceRange.pricing != nil {
			intervals = append(intervals, ModelPricingDisplayInterval(priceRange.minTokens, priceRange.maxTokens, priceRange.pricing, rateMultiplier))
		}
	}
	return intervals
}

// PricingIntervalHasEffectiveTokenPricing reports whether any token amount
// pointer is supplied.
func PricingIntervalHasEffectiveTokenPricing(interval PricingInterval) bool {
	return interval.InputPrice != nil ||
		interval.OutputPrice != nil ||
		interval.CacheWritePrice != nil ||
		interval.CacheWrite1hPrice != nil ||
		interval.CacheReadPrice != nil
}

// SameDisplayTokenPricing compares all standard and Fast display amount fields.
// Two nil inputs are equal and one nil input differs. It compares unit-rate
// intervals, including displayed TTL cache rates, rather than their underlying
// metadata or context rules.
func SameDisplayTokenPricing(a *ModelPricing, b *ModelPricing) bool {
	if a == nil || b == nil {
		return a == b
	}
	// Uniform ranges must agree in both standard and Fast amounts, so equality
	// includes their displayed TTL buckets and cannot hide a tier-specific
	// difference.
	return ModelPricingDisplayInterval(0, nil, a, 1) == ModelPricingDisplayInterval(0, nil, b, 1)
}

// BuildTokenDisplayPricing returns standard and Fast token amounts scaled by
// rateMultiplier. Built-in absolute tiers or eligible long-context multipliers
// produce interval display instead of flat amounts. Callers supply non-nil
// prices, valid ordered tier records, and a finite normalized rate.
func BuildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	if intervals := LongContextDisplayPricingIntervals(pricing, rateMultiplier); len(intervals) > 0 {
		return BuildTokenIntervalDisplayPricing(intervals)
	}

	cacheWritePrice, cacheWrite1hPrice := CacheCreationDisplayPrices(pricing)
	displayPricing := ModelDisplayPricing{
		PricingMode:               "token",
		PriceStatus:               "priced",
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := FastModeDisplayPricing(pricing); ok {
		displayPricing.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		displayPricing.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		displayPricing.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := CacheCreationDisplayPrices(fastPricing)
		displayPricing.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		displayPricing.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		displayPricing.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		displayPricing.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return displayPricing
}

// CacheCreationDisplayPrices returns the short and hourly cache amounts shown
// in token price views. Nil pricing returns two zeros. The aggregate creation
// amount is the initial short price; breakdown support replaces it only when
// the stored 5m amount is positive.
//
// Breakdown support also controls whether the stored hourly amount is exposed;
// otherwise hourly display is zero. A zero 5m field therefore keeps the
// aggregate short amount, even when the hourly field is available. This helper
// selects display units while cost calculation separately normalizes reported
// usage details.
func CacheCreationDisplayPrices(pricing *ModelPricing) (float64, float64) {
	if pricing == nil {
		return 0, 0
	}
	short := pricing.CacheCreationPricePerToken
	// Keep aggregate short pricing when TTL support or a positive 5m rate is
	// absent; the hourly field alone cannot select a different short amount.
	if pricing.SupportsCacheBreakdown && pricing.CacheCreation5mPrice > 0 {
		short = pricing.CacheCreation5mPrice
	}
	if !pricing.SupportsCacheBreakdown {
		return short, 0
	}
	return short, pricing.CacheCreation1hPrice
}

// LongContextDisplayPricingIntervals builds displayed ranges for built-in
// catalog context pricing. Absolute tiers use their ordered thresholds and
// complete price records, with the final range unbounded. Base service-tier
// modifier pointers are applied to each selected tier's shallow copy.
//
// If no absolute tiers exist, an eligible positive threshold and increased
// multiplier produce base and long-context ranges. This multiplier path uses
// the stored threshold as the range endpoint; it does not shift inclusive
// thresholds here. Callers provide non-nil tier records and a finite normalized
// rate.
func LongContextDisplayPricingIntervals(pricing *ModelPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	if pricing != nil && len(pricing.ContextPrices) > 0 {
		var intervals []ModelDisplayPricingInterval
		start := 0
		current := pricing
		for _, tier := range pricing.ContextPrices {
			end := tier.Threshold
			intervals = append(intervals, ModelPricingDisplayInterval(start, &end, current, rateMultiplier))
			next := *tier.Pricing
			next.FastModeMultiplier = pricing.FastModeMultiplier
			next.FastMultiplier = pricing.FastMultiplier
			next.FlexMultiplier = pricing.FlexMultiplier
			next.MaxReasoningEffortMultiplier = pricing.MaxReasoningEffortMultiplier
			start, current = end, &next
		}
		return append(intervals, ModelPricingDisplayInterval(start, nil, current, rateMultiplier))
	}
	if !HasLongContextDisplayPricing(pricing) {
		return nil
	}

	// Use the stored multiplier threshold as this display endpoint.
	maxTokens := pricing.LongContextInputThreshold
	baseInterval := ModelPricingDisplayInterval(0, &maxTokens, pricing, rateMultiplier)

	longContextPricing := ApplyLongContextDisplayMultipliers(pricing)
	longContextInterval := ModelPricingDisplayInterval(pricing.LongContextInputThreshold, nil, longContextPricing, rateMultiplier)

	return []ModelDisplayPricingInterval{baseInterval, longContextInterval}
}

// ApplyLongContextDisplayMultipliers returns a shallow copy with text and cache
// amounts scaled. Nonpositive input or output factors become one. Standard,
// priority, and both TTL cache-write amounts use the input factor; image amount
// fields and shared metadata references retain their supplied values.
func ApplyLongContextDisplayMultipliers(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	adjusted := *pricing
	// Use the calculation's unit default for a missing input or output factor,
	// keeping the corresponding displayed text and cache amounts at their selected
	// base rate.
	inputMultiplier := LongContextMultiplierOrOne(pricing.LongContextInputMultiplier)
	outputMultiplier := LongContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
	adjusted.InputPricePerToken *= inputMultiplier
	adjusted.InputPricePerTokenPriority *= inputMultiplier
	adjusted.OutputPricePerToken *= outputMultiplier
	adjusted.OutputPricePerTokenPriority *= outputMultiplier
	adjusted.CacheCreationPricePerToken *= inputMultiplier
	adjusted.CacheCreationPricePerTokenPriority *= inputMultiplier
	adjusted.CacheCreation5mPrice *= inputMultiplier
	adjusted.CacheCreation1hPrice *= inputMultiplier
	adjusted.CacheReadPricePerToken *= inputMultiplier
	adjusted.CacheReadPricePerTokenPriority *= inputMultiplier
	return &adjusted
}

// HasLongContextDisplayPricing reports whether multiplier-based context tiers
// can be displayed. It requires displayable prices, a positive context
// threshold, and at least one input or output multiplier above one. Absolute
// tiers are handled separately.
func HasLongContextDisplayPricing(pricing *ModelPricing) bool {
	return HasAnyDisplayTokenPricing(pricing) &&
		pricing.LongContextInputThreshold > 0 &&
		(pricing.LongContextInputMultiplier > 1 || pricing.LongContextOutputMultiplier > 1)
}

// ModelPricingDisplayInterval returns one displayed range with rate-scaled
// standard and Fast prices. Pricing must be non-nil. The supplied maxTokens
// pointer is retained, and nil marks an unbounded range; callers keep endpoint
// storage stable.
func ModelPricingDisplayInterval(minTokens int, maxTokens *int, pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricingInterval {
	cacheWritePrice, cacheWrite1hPrice := CacheCreationDisplayPrices(pricing)
	interval := ModelDisplayPricingInterval{
		MinTokens:                 minTokens,
		MaxTokens:                 maxTokens,
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := FastModeDisplayPricing(pricing); ok {
		interval.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		interval.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		interval.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := CacheCreationDisplayPrices(fastPricing)
		interval.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		interval.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		interval.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		interval.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return interval
}

// FastModeDisplayPricing returns the Fast amount view and whether Fast prices
// are displayable. Configured Fast factors scale a recursive price copy first,
// including an explicit zero factor. Otherwise a shallow copy substitutes
// present or positive legacy priority amounts into corresponding standard
// buckets.
//
// A priority cache-write amount also scales both TTL prices by the ratio of
// priority amount to standard amount when the standard price is positive.
// Unavailable buckets retain their selected values, and copied metadata
// pointers remain shared. The original scalar prices are left available for
// standard display.
func FastModeDisplayPricing(pricing *ModelPricing) (*ModelPricing, bool) {
	if !HasFastModeDisplayPricing(pricing) {
		return nil, false
	}
	if multiplier, configured := NormalizedFastModeMultiplier(pricing); configured {
		fastPricing := MultiplyModelPricing(pricing, multiplier)
		fastPricing.FastModeMultiplier = nil
		return fastPricing, true
	}

	fastPricing := *pricing
	if UsePriorityServiceTierPricing(OpenAIFastTierPriority, pricing) {
		if pricing.InputPricePerTokenPriority > 0 || pricing.PriorityInputPresent {
			fastPricing.InputPricePerToken = pricing.InputPricePerTokenPriority
		}
		if pricing.OutputPricePerTokenPriority > 0 || pricing.PriorityOutputPresent {
			fastPricing.OutputPricePerToken = pricing.OutputPricePerTokenPriority
		}
		if pricing.CacheCreationPricePerTokenPriority > 0 || pricing.PriorityCacheWritePresent {
			fastPricing.CacheCreationPricePerToken = pricing.CacheCreationPricePerTokenPriority
			if pricing.SupportsCacheBreakdown && pricing.CacheCreationPricePerToken > 0 {
				// Use the priority amount divided by the standard amount to scale both TTL
				// display rates, retaining explicit free priority cache pricing.
				ratio := pricing.CacheCreationPricePerTokenPriority / pricing.CacheCreationPricePerToken
				fastPricing.CacheCreation5mPrice *= ratio
				fastPricing.CacheCreation1hPrice *= ratio
			}
		}
		if pricing.CacheReadPricePerTokenPriority > 0 || pricing.PriorityCacheReadPresent {
			fastPricing.CacheReadPricePerToken = pricing.CacheReadPricePerTokenPriority
		}
		return &fastPricing, true
	}

	multiplier := ConfiguredServiceTierMultiplier(OpenAIFastTierPriority, pricing)
	fastPricing.InputPricePerToken *= multiplier
	fastPricing.ImageInputPricePerToken *= multiplier
	fastPricing.ImageCacheReadPricePerToken *= multiplier
	fastPricing.OutputPricePerToken *= multiplier
	fastPricing.CacheCreationPricePerToken *= multiplier
	fastPricing.CacheReadPricePerToken *= multiplier
	fastPricing.ImageOutputPricePerToken *= multiplier
	return &fastPricing, true
}

// ResolvedHasFastModeDisplayPricing reports Fast display availability across
// effective token prices. It checks the available default and then each
// configured interval, including interval-only cards. Nil resolution and other
// billing modes return false.
func ResolvedHasFastModeDisplayPricing(resolved *ResolvedPricing) bool {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return false
	}
	if HasFastModeDisplayPricing(resolved.tokenPricingForInterval(nil)) {
		return true
	}
	for i := range resolved.Intervals {
		pricing := resolved.tokenPricingForInterval(&resolved.Intervals[i])
		if HasFastModeDisplayPricing(pricing) {
			return true
		}
	}
	return false
}

// HasFastModeDisplayPricing reports displayable token prices with any Fast
// pricing indicator. A configured factor, presence flag, or positive legacy
// priority amount qualifies. An explicit zero priority amount needs its
// presence flag to remain visible.
func HasFastModeDisplayPricing(pricing *ModelPricing) bool {
	return HasAnyDisplayTokenPricing(pricing) &&
		(pricing.FastModeMultiplier != nil ||
			pricing.FastMultiplier != nil || pricing.PriorityInputPresent || pricing.PriorityOutputPresent || pricing.PriorityCacheReadPresent || pricing.PriorityCacheWritePresent ||
			pricing.InputPricePerTokenPriority > 0 ||
			pricing.OutputPricePerTokenPriority > 0 ||
			pricing.CacheCreationPricePerTokenPriority > 0 ||
			pricing.CacheReadPricePerTokenPriority > 0)
}

// BuildTokenIntervalDisplayPricing returns a token display result containing
// the supplied interval slice. The slice and its endpoint pointers remain
// shared, so callers keep them stable while the display result is consumed.
func BuildTokenIntervalDisplayPricing(intervals []ModelDisplayPricingInterval) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:      "token",
		PriceStatus:      "priced",
		ContextIntervals: intervals,
	}
}

// BuildImageDisplayPricing returns a priced image display containing all three
// size amounts. Every size is declared available, including a supplied zero.
// Callers use ImagePriceSizes to restrict availability when constructing a
// partial quote.
func BuildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:     "image",
		PriceStatus:     "priced",
		ImagePriceSizes: []string{"1K", "2K", "4K"},
		ImagePrice1K:    price1K,
		ImagePrice2K:    price2K,
		ImagePrice4K:    price4K,
	}
}

// UnknownDisplayPricing returns the display marker for an unresolved model
// price.
func UnknownDisplayPricing() ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode: "unknown",
		PriceStatus: "unpriced",
	}
}

// CalculateWebSearchCost charges a supplied USD-per-call amount for positive
// callCount. Missing prices or nonpositive counts return an empty breakdown.
// Zero is a known free price, negative rates become zero, and callers validate
// nonnegative finite prices and finite rates.
func CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	if callCount <= 0 {
		return &CostBreakdown{}
	}
	if groupPrice == nil {
		return &CostBreakdown{}
	}
	unitPrice := *groupPrice
	if groupPrice != nil && *groupPrice >= 0 {
		unitPrice = *groupPrice
	}
	totalCost := unitPrice * float64(callCount)

	// A negative user factor would reverse a positive search amount; use zero for
	// the charge factor while retaining the base call cost.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  totalCost * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateSearchCost charges numCalls using a supplied USD-per-thousand-call
// amount. Absent, negative, or zero prices and nonpositive counts return an
// empty breakdown. Positive amounts divide by one thousand; negative rates
// become zero, and callers validate finiteness.
func CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	if numCalls <= 0 {
		return &CostBreakdown{}
	}
	if groupPricePer1k == nil {
		return &CostBreakdown{}
	}
	pricePer1k := *groupPricePer1k
	if groupPricePer1k != nil {
		if *groupPricePer1k < 0 {
			return &CostBreakdown{}
		}
		pricePer1k = *groupPricePer1k
	}
	if pricePer1k == 0 {
		return &CostBreakdown{}
	}
	// Normalize a negative user factor before scaling positive search amounts,
	// retaining the reported base cost with a zero user charge.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	// Convert a thousand-call quote into one-call charging units.
	unit := pricePer1k / 1000.0
	total := unit * float64(numCalls)
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateAudioCost charges positive durationOrUnits using the selected audio
// rate. Realtime units are minutes, TTS units are millions of characters, and
// STT units are hours. Mode is case-insensitive but untrimmed; unknown modes or
// nonpositive selected prices return an empty breakdown.
//
// Callers resolve and validate finite rates and usage units before calling.
// Missing optional prices therefore produce zero results here, with
// missing-price rejection owned by the billing facade. Negative user rates
// become zero, and successful positive-priced results use per_request mode.
func CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *AudioPriceConfig, rateMultiplier float64) *CostBreakdown {
	if durationOrUnits <= 0 {
		return &CostBreakdown{}
	}
	var unitPrice float64
	switch strings.ToLower(mode) {
	case "realtime":
		if groupConfig != nil && groupConfig.RealtimePerMin != nil {
			unitPrice = *groupConfig.RealtimePerMin
		}
	case "tts":
		if groupConfig != nil && groupConfig.TTSPerMChars != nil {
			unitPrice = *groupConfig.TTSPerMChars
		}
	case "stt":
		if groupConfig != nil && groupConfig.STTPerHour != nil {
			unitPrice = *groupConfig.STTPerHour
		}
	default:
		return &CostBreakdown{}
	}
	if unitPrice <= 0 {
		return &CostBreakdown{}
	}
	// Normalize a negative user factor before applying the selected audio units,
	// retaining the base operation cost with a zero charge.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	total := unitPrice * durationOrUnits
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// HasExplicitImageGenerationPricing reports a known flat image amount with
// image_generation mode. Mode comparison ignores case and surrounding
// whitespace. A chat entry's per-image metadata alone does not select
// image-generation classification.
func HasExplicitImageGenerationPricing(pricing *CatalogModelPricing) bool {
	return HasImageUnitPrice(pricing) &&
		strings.EqualFold(strings.TrimSpace(pricing.Mode), "image_generation")
}

// HasAnyDisplayTokenPricing reports whether a price record can supply token
// display amounts. Nil pricing returns false. A nonempty CatalogSource other
// than unpriced establishes availability even when every checked amount is
// zero, retaining free catalog models in display lookup.
//
// When that source condition is absent, at least one checked standard text,
// image, cache, or hourly-cache amount must be positive. Priority amounts and
// absolute context records are not inspected by this check. A positive checked
// amount can qualify even with the unpriced label; callers validate amounts
// and selected-source availability separately before using a display result.
func HasAnyDisplayTokenPricing(pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	return pricing.CatalogSource != "" && pricing.CatalogSource != "unpriced" || pricing.InputPricePerToken > 0 ||
		pricing.ImageInputPricePerToken > 0 ||
		pricing.OutputPricePerToken > 0 ||
		pricing.CacheCreationPricePerToken > 0 ||
		pricing.CacheCreation1hPrice > 0 ||
		pricing.CacheReadPricePerToken > 0 ||
		pricing.ImageOutputPricePerToken > 0
}

// CalculateCost dispatches usage according to the resolved billing mode.
// Callers supply non-nil resolution and validated finite inputs. Negative user
// rates become zero; successful results record the selected mode, with empty or
// other dispatch values using token calculation.
func CalculateCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	// Direct calculation also accepts cached records; normalize a negative user
	// factor before dispatch so every selected billing mode receives the same zero
	// charge factor.
	if input.RateMultiplier < 0 {
		input.RateMultiplier = 0
	}

	var breakdown *CostBreakdown
	var err error
	switch resolved.Mode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		breakdown, err = CalculatePerRequestCost(resolved, input)
	default: // BillingModeToken
		breakdown, err = CalculateTokenCost(resolved, input)
	}
	if err == nil && breakdown != nil {
		breakdown.BillingMode = string(resolved.Mode)
		if breakdown.BillingMode == "" {
			breakdown.BillingMode = string(BillingModeToken)
		}
	}
	return breakdown, err
}
