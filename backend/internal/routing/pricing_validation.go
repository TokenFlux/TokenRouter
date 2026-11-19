package routing

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// pricing_validation.go validates shared user-price cards, provider-cost rules,
// model pattern overlap, and daily time-pricing windows before persistence.
// Card validation normalizes reference targets, clears empty time-pricing
// containers, and canonicalizes video resolution labels on working entries so
// saved rows match runtime lookup identities without mutating caller slices in
// place when later checks fail.
//
// Pattern conflict detection classifies trailing wildcards before applying
// price-name normalization, keeping overlap rejection aligned with
// expandPricingToCache. Group model-mapping overlap checks use the same
// exact-versus-prefix intersection logic while preserving source punctuation
// and whitespace for exact gateway routing matches.

// PricingConfigValidation checks user and provider-cost cards using an optional
// timezone loader.
type PricingConfigValidation struct {
	// LoadLocation validates populated daily pricing configuration after its
	// timezone name passes validation. Nil is accepted for empty periods but
	// returns an error for populated ones.
	LoadLocation func(string) (*time.Location, error) // time-pricing timezone validation loader
}

// modelEntry stores normalized pattern identities used during range overlap
// detection.
type modelEntry struct {
	// pattern, prefix, and wildcard separate error display from match behavior.
	// pattern retains supplied spelling, prefix follows its caller's
	// normalization, and wildcard selects prefix overlap rather than exact
	// equality.
	pattern  string // original diagnostic pattern spelling
	prefix   string // normalized match comparison prefix
	wildcard bool   // trailing-star pattern selection flag
}

// PricingEntries validates model ranges, references, intervals, amounts, and time pricing.
// It trims reference targets, clears empty time objects, and canonicalizes video labels in the
// supplied working entries. Callers publish or persist these entries only after
// validation succeeds, keeping stored labels within the validated character limit.
func (v PricingConfigValidation) PricingEntries(entries []ModelPricingEntry) error {
	if err := validatePriceReferences(entries); err != nil {
		return err
	}
	if err := validateNoConflictingModels(entries); err != nil {
		return err
	}
	if err := validatePricingIntervals(entries); err != nil {
		return err
	}
	if err := validatePricingBillingMode(entries); err != nil {
		return err
	}
	if err := v.PricingTime(entries); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].BillingMode != BillingModeVideo {
			continue
		}
		for j := range entries[i].Intervals {
			// Persist the validated canonical spelling so surrounding whitespace
			// cannot exceed the database capacity after a normalized-length check.
			label := strings.TrimSpace(entries[i].Intervals[j].TierLabel)
			if label != "" {
				label = pricing.NormalizeVideoBillingResolutionOrDefault(label)
			}
			entries[i].Intervals[j].TierLabel = label
		}
	}
	return nil
}

// PricingTime validates time multipliers for token cards and removes time
// objects with no periods. Other explicit billing modes reject populated time
// pricing; missing mode behaves as token, and accepted periods require the
// configured timezone loader.
func (v PricingConfigValidation) PricingTime(pricing []ModelPricingEntry) error {
	for i := range pricing {
		config := pricing[i].TimePricing
		if config == nil {
			continue
		}
		// An empty time object carries no multiplier periods. Clear it before
		// persistence so omission and an unused editor object resolve to the same
		// inherited pricing behavior.
		if len(config.Periods) == 0 {
			pricing[i].TimePricing = nil
			continue
		}
		mode := pricing[i].BillingMode
		if mode != "" && mode != BillingModeToken {
			return infraerrors.BadRequest("TIME_PRICING_UNSUPPORTED_MODE", "time pricing only supports token billing mode")
		}
		if err := v.TimePricing(config); err != nil {
			return infraerrors.BadRequest("INVALID_TIME_PRICING", fmt.Sprintf(
				"invalid time pricing for models %v: %v", pricing[i].Models, err))
		}
	}
	return nil
}

// validatePricingBillingMode checks each card's mode requirements, amount
// bounds, and interval content. It stops at the first failure; empty card lists
// succeed, and interval range ordering is validated separately by
// validatePricingIntervals.
func validatePricingBillingMode(pricing []ModelPricingEntry) error {
	for _, p := range pricing {
		if err := CheckBillingModeRequirements(p); err != nil {
			return err
		}
		if err := checkPricesNotNegative(p); err != nil {
			return err
		}
		if err := checkIntervalsHavePrices(p); err != nil {
			return err
		}
	}
	return nil
}

// CheckBillingModeRequirements checks the fields required by a card's charging
// mode and modifiers. Per-request, image, and video cards require a request
// amount or intervals; legacy amount multipliers require supplied prices, while
// token-only service-tier and reasoning multipliers must be finite and
// positive.
func CheckBillingModeRequirements(p ModelPricingEntry) error {
	if p.BillingMode == BillingModePerRequest || p.BillingMode == BillingModeImage || p.BillingMode == BillingModeVideo {
		if p.PerRequestPrice == nil && len(p.Intervals) == 0 {
			return infraerrors.BadRequest(
				"BILLING_MODE_MISSING_PRICE",
				"per-request price or intervals required for per_request/image billing mode",
			)
		}
	}
	if p.PriceMultiplier != nil && !hasExplicitPricingPrice(p) {
		return infraerrors.BadRequest(
			"PRICE_MULTIPLIER_MISSING_PRICE",
			"price_multiplier requires at least one explicit price",
		)
	}
	if p.FastModeMultiplier != nil {
		mode := p.BillingMode
		if mode == "" {
			mode = BillingModeToken
		}
		if mode != BillingModeToken {
			return infraerrors.BadRequest(
				"FAST_MODE_MULTIPLIER_UNSUPPORTED_BILLING_MODE",
				"fast_mode_multiplier is only supported for token billing mode",
			)
		}
		if !hasExplicitPricingPrice(p) {
			return infraerrors.BadRequest(
				"FAST_MODE_MULTIPLIER_MISSING_PRICE",
				"fast_mode_multiplier requires at least one explicit price",
			)
		}
	}
	if p.MaxReasoningEffortMultiplier != nil && (math.IsNaN(*p.MaxReasoningEffortMultiplier) || math.IsInf(*p.MaxReasoningEffortMultiplier, 0)) {
		return infraerrors.BadRequest("INVALID_MULTIPLIER", "max_reasoning_effort_multiplier must be finite and > 0")
	}
	for _, c := range []struct {
		field string   // rejected multiplier field label
		val   *float64 // supplied optional multiplier amount
	}{
		{"fast_multiplier", p.FastMultiplier},
		{"flex_multiplier", p.FlexMultiplier},
		{"max_reasoning_effort_multiplier", p.MaxReasoningEffortMultiplier},
	} {
		if c.val != nil && (math.IsNaN(*c.val) || math.IsInf(*c.val, 0) || *c.val <= 0) {
			return infraerrors.BadRequest("INVALID_MULTIPLIER", fmt.Sprintf("%s must be finite and > 0", c.field))
		}
	}
	if p.FastMultiplier != nil || p.FlexMultiplier != nil || p.MaxReasoningEffortMultiplier != nil {
		mode := p.BillingMode
		if mode == "" {
			mode = BillingModeToken
		}
		if mode != BillingModeToken {
			return infraerrors.BadRequest(
				"TIER_MULTIPLIER_UNSUPPORTED_BILLING_MODE",
				"fast_multiplier, flex_multiplier and max_reasoning_effort_multiplier are only supported for token billing mode",
			)
		}
	}
	return nil
}

// hasExplicitPricingPrice delegates optional amount presence detection to the
// shared price-card rules.
func hasExplicitPricingPrice(p ModelPricingEntry) bool { return pricing.HasExplicitPricingPrice(p) }

// checkPricesNotNegative rejects supplied prices and legacy multipliers that
// are negative or nonfinite. Nil values remain unspecified and zero remains
// supplied; strictly positive tier and reasoning factors are checked by
// CheckBillingModeRequirements.
func checkPricesNotNegative(p ModelPricingEntry) error {
	checks := []struct {
		field string   // rejected price field label
		val   *float64 // supplied optional price amount
	}{
		{"price_multiplier", p.PriceMultiplier},
		{"fast_mode_multiplier", p.FastModeMultiplier},
		{"input_price", p.InputPrice},
		{"output_price", p.OutputPrice},
		{"cache_write_price", p.CacheWritePrice},
		{"cache_write_5m_price", p.CacheWrite5mPrice},
		{"cache_write_1h_price", p.CacheWrite1hPrice},
		{"cache_write_30m_price", p.CacheWrite30mPrice},
		{"cache_write_12h_price", p.CacheWrite12hPrice},
		{"cache_read_price", p.CacheReadPrice},
		{"image_input_price", p.ImageInputPrice},
		{"image_output_price", p.ImageOutputPrice},
		{"per_request_price", p.PerRequestPrice},
	}
	for _, c := range checks {
		if c.val != nil && (math.IsNaN(*c.val) || math.IsInf(*c.val, 0) || *c.val < 0) {
			return infraerrors.BadRequest("NEGATIVE_PRICE", fmt.Sprintf("%s must be >= 0", c.field))
		}
	}
	return nil
}

// ProviderStatsPricing rejects references, user-only tier, reasoning, and populated time
// modifiers before validating provider-cost cards. Empty time objects can be
// normalized away by PricingEntries; callers supply separate working cards for
// their provider statistics rules.
func (v PricingConfigValidation) ProviderStatsPricing(pricing []ModelPricingEntry) error {
	for _, p := range pricing {
		if p.ReferenceModel != "" {
			return infraerrors.BadRequest("INVALID_PRICE_REFERENCE", "provider cost rules do not support model references")
		}
		if p.FastModeMultiplier != nil {
			return infraerrors.BadRequest(
				"PROVIDER_STATS_FAST_MODE_MULTIPLIER_UNSUPPORTED",
				"fast_mode_multiplier is not supported for provider stats pricing",
			)
		}
		if p.FastMultiplier != nil || p.FlexMultiplier != nil {
			return infraerrors.BadRequest(
				"PROVIDER_STATS_TIER_MULTIPLIER_UNSUPPORTED",
				"service tier multipliers are not supported for provider stats pricing",
			)
		}
		if p.MaxReasoningEffortMultiplier != nil {
			return infraerrors.BadRequest(
				"PROVIDER_STATS_REASONING_MULTIPLIER_UNSUPPORTED",
				"max_reasoning_effort_multiplier is not supported for provider stats pricing",
			)
		}
		if p.TimePricing != nil && len(p.TimePricing.Periods) > 0 {
			return infraerrors.BadRequest(
				"PROVIDER_STATS_TIME_PRICING_UNSUPPORTED",
				"provider stats pricing does not support time pricing",
			)
		}
	}
	return v.PricingEntries(pricing)
}

// checkIntervalsHavePrices rejects intervals containing none of the recognized
// amount or scaling fields. A default card amount does not make a blank
// interval valid, and supplied zero pointers still count as present values.
func checkIntervalsHavePrices(p ModelPricingEntry) error {
	for _, iv := range p.Intervals {
		if iv.InputPrice == nil && iv.OutputPrice == nil &&
			iv.CacheWritePrice == nil && iv.CacheWrite5mPrice == nil && iv.CacheWrite1hPrice == nil && iv.CacheWrite30mPrice == nil && iv.CacheWrite12hPrice == nil && iv.CacheReadPrice == nil &&
			iv.PerRequestPrice == nil && iv.InputMultiplier == nil &&
			iv.OutputMultiplier == nil && iv.CacheWriteMultiplier == nil &&
			iv.CacheReadMultiplier == nil {
			return infraerrors.BadRequest(
				"INTERVAL_MISSING_PRICE",
				fmt.Sprintf("interval [%d, %s] has no price fields set for model %v",
					iv.MinTokens, formatMaxTokens(iv.MaxTokens), p.Models),
			)
		}
	}
	return nil
}

// formatMaxTokens formats a nullable interval ceiling for validation
// diagnostics. Nil denotes the unbounded infinity label, while a supplied zero
// or another integer is rendered literally for the caller's error message.
func formatMaxTokens(max *int) string {
	if max == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *max)
}

// conflictsBetween reports whether two normalized exact or prefix patterns
// accept any common model. Both entries use the same normalization chosen by
// their caller before this comparison.
//
// Two exact values overlap when their prefixes equal. An exact value overlaps a
// wildcard when the exact name starts with its prefix, and two wildcards
// overlap when either prefix contains the other. An empty wildcard prefix
// therefore intersects every pattern, preventing a catch-all from silently
// competing with a more specific saved card.
func conflictsBetween(a, b modelEntry) bool {
	switch {
	case !a.wildcard && !b.wildcard:
		return a.prefix == b.prefix
	case a.wildcard && !b.wildcard:
		return strings.HasPrefix(b.prefix, a.prefix)
	case !a.wildcard && b.wildcard:
		return strings.HasPrefix(a.prefix, b.prefix)
	default:
		return strings.HasPrefix(a.prefix, b.prefix) ||
			strings.HasPrefix(b.prefix, a.prefix)
	}
}

// toModelEntry retains a mapping source's spelling and lowercases its
// comparison prefix. A trailing asterisk marks wildcard behavior and is removed
// from the prefix; surrounding whitespace remains part of mapping identity.
func toModelEntry(pattern string) modelEntry {
	lower := strings.ToLower(pattern)
	isWild := strings.HasSuffix(lower, "*")
	prefix := lower
	if isWild {
		prefix = strings.TrimSuffix(lower, "*")
	}
	return modelEntry{pattern: pattern, prefix: prefix, wildcard: isWild}
}

// toPricingModelEntry separates a price pattern's diagnostic spelling from its
// normalized comparison identity. It retains the original pattern for conflict
// messages and classifies a trailing asterisk before applying price-name
// normalization.
//
// The order matches expandPricingToCache: "model* " becomes an exact identity
// with comparison prefix "model*", while "model*" becomes the wildcard prefix
// "model". Trimming before asterisk detection would make validation and cached
// lookup disagree on those supplied forms.
func toPricingModelEntry(pattern string) modelEntry {
	// Detect and remove the trailing star before price-name normalization,
	// matching cache construction so a supplied spelling cannot classify
	// differently during overlap checks and runtime lookup.
	isWild := strings.HasSuffix(pattern, "*")
	prefix := pattern
	if isWild {
		prefix = strings.TrimSuffix(pattern, "*")
	}
	return modelEntry{
		pattern:  pattern,
		prefix:   normalizePriceModelName(prefix),
		wildcard: isWild,
	}
}

// validateNoConflictingModels rejects overlapping patterns anywhere in a
// supplied price-card list. It normalizes with cache identity rules, so exact
// duplicates, case differences, trimmed-name collisions, and intersecting
// wildcard ranges cannot compete during valid saved lookup.
func validateNoConflictingModels(pricingList []ModelPricingEntry) error {
	var entries []modelEntry
	for _, price := range pricingList {
		for _, model := range price.Models {
			entries = append(entries, toPricingModelEntry(model))
		}
	}
	return detectConflicts(entries, "MODEL_PATTERN_CONFLICT", "model patterns")
}

// validateNoConflictingMappings rejects overlapping exact or trailing-wildcard
// mapping sources. Targets are irrelevant to overlap detection, and source
// normalization retains punctuation and whitespace while treating letter case
// as equivalent.
func validateNoConflictingMappings(mapping map[string]string) error {
	entries := make([]modelEntry, 0, len(mapping))
	for source := range mapping {
		entries = append(entries, toModelEntry(source))
	}
	return detectConflicts(entries, "MAPPING_PATTERN_CONFLICT", "mapping source patterns")
}

// validatePricingIntervals delegates each card's ranges to the shared interval
// validator and labels failures with its models. Validation stops at the first
// card error and leaves the supplied interval ordering unchanged.
func validatePricingIntervals(pricingList []ModelPricingEntry) error {
	for _, pricing := range pricingList {
		if err := ValidateIntervals(pricing.Intervals, pricing.BillingMode); err != nil {
			return infraerrors.BadRequest(
				"INVALID_PRICING_INTERVALS",
				fmt.Sprintf("invalid pricing intervals for models %v: %v", pricing.Models, err),
			)
		}
	}
	return nil
}

// detectConflicts returns a labeled bad-request error for the first overlapping
// pair in supplied order. Empty or disjoint entries succeed, and diagnostic
// text retains each original pattern rather than its normalized comparison
// prefix.
func detectConflicts(entries []modelEntry, errCode, label string) error {
	for i := range entries {
		for j := i + 1; j < len(entries); j++ {
			if conflictsBetween(entries[i], entries[j]) {
				return infraerrors.BadRequest(errCode,
					fmt.Sprintf("%s '%s' and '%s' conflict: overlapping match range "+
						"(model names are matched case-insensitively, so an existing entry already covers all case variants)",
						label, entries[i].pattern, entries[j].pattern))
			}
		}
	}
	return nil
}

// TimePricing validates populated daily pricing periods and their timezone. Nil
// or period-empty objects succeed without loading a timezone; populated objects
// require LoadLocation after timezone-name validation, then use shared period
// and multiplier checks.
func (v PricingConfigValidation) TimePricing(config *TimePricingConfig) error {
	if config == nil || len(config.Periods) == 0 {
		return nil
	}
	if err := pricing.ValidateTimezoneName(config.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	if v.LoadLocation == nil {
		return fmt.Errorf("timezone: pricing timezone loader is not configured")
	}
	if _, err := v.LoadLocation(config.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	return pricing.ValidateTimePricingConfig(config)
}
