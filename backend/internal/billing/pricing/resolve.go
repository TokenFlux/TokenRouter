package pricing

import (
	"slices"
	"strings"
)

// resolve.go applies card overrides and resolves token intervals and
// request-unit prices.
// A token card can supply default amounts, priced intervals, or modifiers that
// need an existing price. Resolution retains availability separately from
// zero-valued amount storage, so an interval-only model stays unpriced outside
// its configured ranges and a multiplier-only placeholder cannot create a free
// default.
//
// Card defaults are applied to copied base prices before interval conversion,
// allowing omitted interval buckets and interval factors to use the same
// defaults as requests outside ranges. Explicit request and media amounts
// retain presence through scaling, with labels separated from context
// selection. Amount-copying helpers preserve shared endpoint and modifier
// references where they are not replaced; callers keep source metadata stable
// while consuming resolved pricing.

// These labels classify the selected pricing path and govern whether
// calculation evaluates catalog-specific time rules or marks the model
// unavailable.
const (
	PricingSourceConfig   = "pricing_config" // effective configured price path
	PricingSourceCatalog  = "catalog"        // effective catalog price path
	PricingSourceUnpriced = "unpriced"       // unavailable selected model price
)

// ResolvedPricing contains the selected units, amount availability, intervals,
// and card metadata for one pricing resolution.
type ResolvedPricing struct {
	// LookupModel carries the resolved price identity into catalog media reads.
	// Nil retains the caller's model for resolutions constructed outside PriceResolver.
	LookupModel *string `json:"-"`
	// ConfiguredReference records a target selected by an explicit profile reference.
	// A referenced target can still be unpriced; callers check availability separately.
	ConfiguredReference bool `json:"-"`

	// Mode controls whether costs use tokens or unit prices. ResolvePriceCards
	// defaults an empty card mode to token; direct callers supply the intended
	// supported mode.
	Mode BillingMode // effective billing-unit selection

	// BasePricing holds available defaults or a modifier placeholder. Resolution
	// can retain its pointer when unchanged or replace it with scalar and context
	// copies before overrides.
	BasePricing *ModelPricing // resolved default token-price record

	// Intervals retains token field-bearing context ranges in supplied order.
	// Matching uses (min,max] endpoints and inherits default buckets; nested
	// pointers can remain shared with the card.
	Intervals []PricingInterval // configured token-context price ranges

	// RequestTiers retains unit tiers whose amount pointer is supplied, including
	// zero. User lookup gives labels precedence and uses unlabeled ranges for
	// known context.
	RequestTiers []PricingInterval // configured request or media unit tiers

	DefaultPerRequestPrice float64 // USD per default request or media unit
	// DefaultPerRequestPricePresent records supplied default presence after
	// resolution and scaling. A zero amount with this marker remains free rather
	// than becoming an unavailable default.
	DefaultPerRequestPricePresent bool `json:"-"` // default unit amount supplied

	// Source classifies the selected configuration, catalog, or unpriced path.
	// CalculateTokenCost evaluates catalog time rules only for the catalog label;
	// individual buckets may still inherit base amounts.
	Source string // effective price-source classification

	// SupportsCacheBreakdown carries available TTL support or a supplied hourly
	// override. Interval conversion applies it to the resulting token price record
	// for calculation.
	SupportsCacheBreakdown bool // resolved TTL detail pricing support

	// SupportsServiceTier carries base service-tier metadata into converted
	// interval records. Actual Fast calculation still checks configured factors
	// and priority amount presence.
	SupportsServiceTier bool // resolved service-tier support metadata

	// ConfigPricing is the effective card reused for interval image amounts and
	// modifiers. Some paths retain source pointer data, so callers keep card
	// inputs stable during resolution use.
	ConfigPricing *ModelPricingEntry `json:"-"` // effective model card and modifiers

	// LongContextPricingEnabled permits built-in catalog context selection when
	// the token interval list is empty. Configured intervals keep their own prices
	// regardless of this policy.
	LongContextPricingEnabled bool `json:"-"` // built-in context-pricing policy flag
	// BasePricingUnavailable identifies a default placeholder containing no
	// established base amount. Explicit interval amounts can still price their
	// ranges while unmatched or modifier-only ranges remain unavailable.
	BasePricingUnavailable bool `json:"-"` // unavailable default token-price marker
}

// ApplyPricingModifiers merges supplied card modifiers into resolved token
// pricing. Nil resolution, other modes, absent cards, and unavailable base
// pointers leave the record unchanged. A shallow base copy retains price and
// catalog metadata while selected modifier fields are replaced.
//
// Interval conversion rereads ConfigPricing, so a copied effective card
// receives the same Fast, Flex, and effort overrides. Nonempty supplied time
// periods replace the card schedule after cloning. Some modifier pointers still
// reference config; callers keep source amounts stable during use of the
// resolved record.
func ApplyPricingModifiers(resolved *ResolvedPricing, config *ModelPricingEntry) {
	if resolved == nil || resolved.Mode != BillingModeToken || config == nil || resolved.BasePricing == nil {
		return
	}
	if config.FastMultiplier == nil && config.FastModeMultiplier == nil && config.FlexMultiplier == nil &&
		config.MaxReasoningEffortMultiplier == nil && config.TimePricing == nil {
		return
	}
	pricing := *resolved.BasePricing
	resolved.BasePricing = &pricing
	merged := ModelPricingEntry{BillingMode: BillingModeToken}
	if resolved.ConfigPricing != nil {
		merged = resolved.ConfigPricing.Clone()
	}
	// Interval conversion reads card metadata again; updating the copied card
	// alongside base modifiers keeps those two calculation paths on the same
	// selected factors.
	if config.FastMultiplier != nil || config.FastModeMultiplier != nil {
		merged.FastMultiplier = config.FastMultiplier
		merged.FastModeMultiplier = config.FastModeMultiplier
	}
	if config.FlexMultiplier != nil {
		merged.FlexMultiplier = config.FlexMultiplier
	}
	if config.MaxReasoningEffortMultiplier != nil {
		merged.MaxReasoningEffortMultiplier = config.MaxReasoningEffortMultiplier
		pricing.MaxReasoningEffortMultiplier = config.MaxReasoningEffortMultiplier
	}
	if config.TimePricing != nil && len(config.TimePricing.Periods) > 0 {
		merged.TimePricing = config.Clone().TimePricing
	}
	resolved.ConfigPricing = &merged
	ApplyResolvedFastModeMultiplier(resolved, &merged)
}

// IsUnpriced reports an unavailable selected source or a token card lacking
// available defaults and amount-bearing intervals. Nil resolution returns
// false, while the unpriced source label wins before mode and card checks.
// Other modes or an absent card leave independent price availability to their
// later lookup.
//
// For a configured token card, an established base counts even at zero; a
// modifier placeholder cannot supply that base. Any amount-bearing interval
// makes the card potentially priced, including a free interval, but requests
// outside priced ranges can still fail their context-specific lookup.
func (r *ResolvedPricing) IsUnpriced() bool {
	if r == nil {
		return false
	}
	if r.Source == PricingSourceUnpriced {
		return true
	}
	// A configured token card needs a base or an amount-bearing interval. Media
	// paths can resolve their independent unit amounts after this
	// token-availability check.
	if r.Mode != BillingModeToken || r.ConfigPricing == nil || r.hasBaseTokenPricing() {
		return false
	}
	return !slices.ContainsFunc(r.Intervals, PricingIntervalHasEffectiveTokenPricing)
}

// hasBaseTokenPricing reports an available base pointer, including explicit
// free amounts. Nil records, absent pointers, and BasePricingUnavailable return
// false, so a placeholder carrying only modifiers cannot supply a default
// price.
func (r *ResolvedPricing) hasBaseTokenPricing() bool {
	return r != nil && r.BasePricing != nil && !r.BasePricingUnavailable
}

// tokenPricingForInterval returns the effective token record for an optional
// interval. A nil interval returns the available base reference. An interval
// with price pointers can stand alone; modifier-only intervals need an
// available base or return nil.
//
// Conversion copies scalar base fields, applies selected amount or multiplier
// overrides, and restores resolved service-tier support. Omitted interval
// buckets use the base when present and otherwise remain zero. Shared metadata
// pointers stay stable while settlement or display consumes the returned
// record.
func (r *ResolvedPricing) tokenPricingForInterval(interval *PricingInterval) *ModelPricing {
	if r == nil {
		return nil
	}
	if interval == nil {
		if r.hasBaseTokenPricing() {
			return r.BasePricing
		}
		return nil
	}
	if !r.hasBaseTokenPricing() && !PricingIntervalHasEffectiveTokenPricing(*interval) {
		return nil
	}
	pricing := IntervalToModelPricingWithBase(interval, r.SupportsCacheBreakdown, r.ConfigPricing, r.BasePricing)
	pricing.SupportsServiceTier = r.SupportsServiceTier
	return pricing
}

// HasEffectivePricing reports usable configuration-source resolution with an
// effective card. It excludes unpriced results and catalog-source modifier
// cards, even when those cards are present. Nil resolution returns false for
// this narrower configuration check.
func (r *ResolvedPricing) HasEffectivePricing() bool {
	return r != nil && !r.IsUnpriced() && r.Source == PricingSourceConfig && r.ConfigPricing != nil && r.ConfigPricing.HasEffectivePricing()
}

// HasConfiguredPricing reports whether resolution retained a configuration
// card. Modifier-only cards can keep catalog as Source, so media callers use
// this separate presence check. Nil resolution returns false.
func (r *ResolvedPricing) HasConfiguredPricing() bool {
	return r != nil && r.ConfigPricing != nil
}

// HasEffectiveOverridePricing reports a usable configuration-source card,
// including free amounts. Catalog-source modifier cards do not qualify for this
// override check. Nil resolution, unpriced results, and absent or ineffective
// cards return false.
func (r *ResolvedPricing) HasEffectiveOverridePricing() bool {
	return r != nil && !r.IsUnpriced() && r.Source == PricingSourceConfig &&
		r.ConfigPricing != nil && r.ConfigPricing.HasEffectivePricing()
}

// ApplyTokenOverrides builds the base and interval inputs for token-card
// resolution. Callers supply non-nil card and resolution records. Intervals are
// retained by token field presence, with separate hourly amounts enabling TTL
// support at the base and interval levels.
//
// Only default card amounts or a previously available base establish default
// pricing. An interval-only card retains an unavailable placeholder outside
// priced ranges, preventing a modifier from making missing prices free. Base
// scalars and nested context price records are copied before overrides;
// retained card and interval pointers require stable caller-owned data.
func ApplyTokenOverrides(chPricing *ModelPricingEntry, resolved *ResolvedPricing) {
	// Field presence retains token amounts and factors while discarding empty or
	// unit-only rows, so switching card modes cannot invent token ranges from
	// stale unit fields.
	validIntervals := FilterValidTokenIntervals(chPricing.Intervals)
	// A supplied hourly amount establishes separate TTL intent even at zero,
	// allowing downstream calculation to distinguish free hourly usage from an
	// omitted hourly price.
	if chPricing.CacheWrite1hPrice != nil {
		resolved.SupportsCacheBreakdown = true
	}
	for _, iv := range validIntervals {
		if iv.CacheWrite1hPrice != nil {
			resolved.SupportsCacheBreakdown = true
			break
		}
	}

	// Apply default amounts before interval conversion so missing interval
	// buckets, interval factors, and unmatched contexts share one available
	// default. Interval-only amounts leave the default placeholder unavailable.
	baseConfig := *chPricing
	baseConfig.Intervals = nil
	resolved.BasePricingUnavailable = !resolved.hasBaseTokenPricing() && !HasExplicitPricingPrice(baseConfig)
	resolved.Intervals = validIntervals
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// Copy base scalar fields before overrides so modifying this resolution does
		// not replace amounts on the shared catalog price record.
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	ApplyConfigTokenPriceOverrides(resolved.BasePricing, chPricing)
	if resolved.SupportsCacheBreakdown {
		resolved.BasePricing.SupportsCacheBreakdown = true
	}
	if chPricing.MaxReasoningEffortMultiplier != nil {
		resolved.BasePricing.MaxReasoningEffortMultiplier = chPricing.MaxReasoningEffortMultiplier
	}
}

// applyConfigImagePriceOverrides writes card image defaults into a non-nil
// token record. Omitted output becomes an explicit zero, disabling catalog
// text-output inheritance. Omitted or zero image input uses the selected text
// rate during calculation; callers validate supplied amounts.
func applyConfigImagePriceOverrides(pricing *ModelPricing, chPricing *ModelPricingEntry) {
	pricing.ImageOutputPricePerToken = 0
	if chPricing.ImageOutputPrice != nil {
		pricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
	}
	pricing.ImageOutputPriceExplicit = true
	if chPricing.ImageInputPrice != nil {
		pricing.ImageInputPricePerToken = *chPricing.ImageInputPrice
	} else {
		pricing.ImageInputPricePerToken = 0
	}
}

// ApplyRequestTierOverrides retains request tiers with a supplied unit amount.
// Callers supply non-nil records; tier pointers remain shared. A supplied
// default sets its amount and presence flag, while an omitted default leaves
// existing default fields unchanged.
func ApplyRequestTierOverrides(chPricing *ModelPricingEntry, resolved *ResolvedPricing) {
	resolved.RequestTiers = FilterValidRequestIntervals(chPricing.Intervals)
	if chPricing.PerRequestPrice != nil {
		resolved.DefaultPerRequestPrice = *chPricing.PerRequestPrice
		resolved.DefaultPerRequestPricePresent = true
	}
}

// ApplyResolvedPriceMultiplier scales resolved amounts after card overrides are
// applied. Absent multipliers or nil resolution leave the record unchanged.
// Callers validate finite factors and require an explicit price; a negative
// stored factor becomes zero.
//
// Base context prices and interval amount pointers receive scaled copies, while
// default price presence survives scaling to zero. Interval conversion also
// reads card image defaults, so a cloned card receives scaled amounts with its
// PriceMultiplier cleared. This keeps later interval conversion from rereading
// unscaled card prices or applying the factor again.
func ApplyResolvedPriceMultiplier(resolved *ResolvedPricing, chPricing *ModelPricingEntry) {
	multiplier, configured := NormalizedPriceMultiplier(chPricing)
	if resolved == nil || !configured {
		return
	}

	resolved.BasePricing = MultiplyModelPricing(resolved.BasePricing, multiplier)
	resolved.Intervals = MultiplyPricingIntervals(resolved.Intervals, multiplier)
	resolved.RequestTiers = MultiplyPricingIntervals(resolved.RequestTiers, multiplier)
	resolved.DefaultPerRequestPrice *= multiplier

	// Scale a cloned card's image amounts together with resolved amounts because
	// later interval conversion rereads those defaults from ConfigPricing.
	scaledConfigPricing := chPricing.Clone()
	scaledConfigPricing.PriceMultiplier = nil
	MultiplyPricingFields(&scaledConfigPricing, multiplier)
	resolved.ConfigPricing = &scaledConfigPricing
}

// ApplyResolvedFastModeMultiplier applies card Fast and Flex factors to token
// base pricing. Nil resolution and other modes are ignored. Standard amounts
// have already been resolved and scaled; the called helpers copy selected
// factor values.
func ApplyResolvedFastModeMultiplier(resolved *ResolvedPricing, chPricing *ModelPricingEntry) {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return
	}
	ApplyConfigFastModeMultiplier(resolved.BasePricing, chPricing)
	ApplyConfigFlexMultiplier(resolved.BasePricing, chPricing)
}

// NormalizedPriceMultiplier returns a card factor and whether its pointer is
// present. Nil cards and omitted factors return one with false. A supplied
// negative factor returns zero with true, and callers validate finiteness.
func NormalizedPriceMultiplier(pricing *ModelPricingEntry) (float64, bool) {
	if pricing == nil || pricing.PriceMultiplier == nil {
		return 1, false
	}
	// Keep factor presence after clamping so scaled explicit prices remain
	// resolved and free, allowing callers to distinguish them from absent prices.
	if *pricing.PriceMultiplier < 0 {
		return 0, true
	}
	return *pricing.PriceMultiplier, true
}

// MultiplyModelPricing returns recursively copied token amount records scaled
// by multiplier. Nil input returns nil, and context thresholds retain their
// values. Time configurations and multiplier pointers remain shared, so callers
// keep that metadata stable while using the copied prices.
func MultiplyModelPricing(pricing *ModelPricing, multiplier float64) *ModelPricing {
	if pricing == nil {
		return nil
	}
	scaled := *pricing
	scaled.ContextPrices = make([]ContextModelPrice, len(pricing.ContextPrices))
	for i, tier := range pricing.ContextPrices {
		scaled.ContextPrices[i] = ContextModelPrice{Threshold: tier.Threshold, Pricing: MultiplyModelPricing(tier.Pricing, multiplier)}
	}
	scaled.InputPricePerToken *= multiplier
	scaled.InputPricePerTokenPriority *= multiplier
	scaled.ImageInputPricePerToken *= multiplier
	scaled.ImageCacheReadPricePerToken *= multiplier
	scaled.OutputPricePerToken *= multiplier
	scaled.OutputPricePerTokenPriority *= multiplier
	scaled.CacheCreationPricePerToken *= multiplier
	scaled.CacheCreationPricePerTokenPriority *= multiplier
	scaled.CacheReadPricePerToken *= multiplier
	scaled.CacheReadPricePerTokenPriority *= multiplier
	scaled.CacheCreation5mPrice *= multiplier
	scaled.CacheCreation1hPrice *= multiplier
	scaled.ImageOutputPricePerToken *= multiplier
	return &scaled
}

// MultiplyPricingIntervals returns copied interval records with independently
// scaled amount pointers. Nil input remains nil. Endpoint and multiplier
// pointers retain their references, requiring stable source values while the
// copied slice is consumed.
func MultiplyPricingIntervals(intervals []PricingInterval, multiplier float64) []PricingInterval {
	if intervals == nil {
		return nil
	}
	scaled := make([]PricingInterval, len(intervals))
	for i := range intervals {
		scaled[i] = intervals[i]
		scaled[i].InputPrice = MultiplyPricePointer(intervals[i].InputPrice, multiplier)
		scaled[i].OutputPrice = MultiplyPricePointer(intervals[i].OutputPrice, multiplier)
		scaled[i].CacheWritePrice = MultiplyPricePointer(intervals[i].CacheWritePrice, multiplier)
		scaled[i].CacheWrite1hPrice = MultiplyPricePointer(intervals[i].CacheWrite1hPrice, multiplier)
		scaled[i].CacheReadPrice = MultiplyPricePointer(intervals[i].CacheReadPrice, multiplier)
		scaled[i].PerRequestPrice = MultiplyPricePointer(intervals[i].PerRequestPrice, multiplier)
	}
	return scaled
}

// MultiplyPricingFields replaces a card's amount pointers and interval amounts
// with scaled copies. Nil cards are ignored; absent amounts remain absent and
// zero stays present. Endpoint, modifier, and time metadata keep their existing
// references.
func MultiplyPricingFields(pricing *ModelPricingEntry, multiplier float64) {
	if pricing == nil {
		return
	}
	pricing.InputPrice = MultiplyPricePointer(pricing.InputPrice, multiplier)
	pricing.OutputPrice = MultiplyPricePointer(pricing.OutputPrice, multiplier)
	pricing.CacheWritePrice = MultiplyPricePointer(pricing.CacheWritePrice, multiplier)
	pricing.CacheWrite1hPrice = MultiplyPricePointer(pricing.CacheWrite1hPrice, multiplier)
	pricing.CacheReadPrice = MultiplyPricePointer(pricing.CacheReadPrice, multiplier)
	pricing.ImageInputPrice = MultiplyPricePointer(pricing.ImageInputPrice, multiplier)
	pricing.ImageOutputPrice = MultiplyPricePointer(pricing.ImageOutputPrice, multiplier)
	pricing.PerRequestPrice = MultiplyPricePointer(pricing.PerRequestPrice, multiplier)
	pricing.Intervals = MultiplyPricingIntervals(pricing.Intervals, multiplier)
}

// MultiplyPricePointer returns separate storage for a scaled optional amount.
// Nil input returns nil and explicit zero remains present. The supplied amount
// and factor are multiplied as given, so callers validate them.
func MultiplyPricePointer(price *float64, multiplier float64) *float64 {
	if price == nil {
		return nil
	}
	scaled := *price * multiplier
	return &scaled
}

// FilterValidTokenIntervals keeps intervals containing token amounts or token
// multiplier pointers. It filters by presence, including zero, rather than
// numeric or range validity. Copied records retain nested pointers; callers
// validate fields separately.
func FilterValidTokenIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil ||
			iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil ||
			iv.InputMultiplier != nil || iv.OutputMultiplier != nil ||
			iv.CacheWriteMultiplier != nil || iv.CacheReadMultiplier != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// FilterValidRequestIntervals keeps tiers containing a supplied per-request
// amount pointer. Explicit zero stays present, and range, label, and amount
// validity are caller checks. Copied records retain their nested pointer
// storage.
func FilterValidRequestIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.PerRequestPrice != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// GetIntervalPricing returns the first matching context interval's effective
// prices. It requires non-nil resolution and uses (min,max] ranges in supplied
// order. No match uses available defaults; absent defaults or base-dependent
// intervals can return nil.
func GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	return resolved.tokenPricingForInterval(FindMatchingInterval(resolved.Intervals, totalContextTokens))
}

// IntervalToModelPricingWithBase converts a token interval using optional base
// and card data. A nil interval returns base directly. Otherwise base scalars
// are copied, supplied interval amounts take precedence over multipliers, and
// omitted values retain the base or zero.
//
// Replaced standard buckets derive priority amounts using their prior ratios.
// Cache-write overrides cover both TTLs unless an hourly amount is supplied,
// while cache multipliers scale both TTL rates. Built-in threshold multipliers
// are cleared for this explicit interval; card image and service-tier rules
// then apply. Metadata pointers remain shared and supplied amounts require
// prior validation.
func IntervalToModelPricingWithBase(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ModelPricingEntry, base *ModelPricing) *ModelPricing {
	if iv == nil {
		return base
	}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: supportsCacheBreakdown,
	}
	if base != nil {
		cloned := *base
		pricing = &cloned
		pricing.SupportsCacheBreakdown = supportsCacheBreakdown
		// The explicit interval already selects its context range; clear built-in
		// threshold multipliers so display and calculation do not introduce another
		// threshold rule.
		pricing.LongContextInputThreshold = 0
		pricing.LongContextInputMultiplier = 0
		pricing.LongContextOutputMultiplier = 0
	}
	if iv.InputPrice != nil {
		priority := ConfigTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *iv.InputPrice)
		pricing.InputPricePerToken = *iv.InputPrice
		pricing.InputPricePerTokenPriority = priority
	} else if iv.InputMultiplier != nil {
		pricing.InputPricePerToken *= *iv.InputMultiplier
		pricing.InputPricePerTokenPriority *= *iv.InputMultiplier
	}
	if iv.OutputPrice != nil {
		priority := ConfigTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *iv.OutputPrice)
		pricing.OutputPricePerToken = *iv.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	} else if iv.OutputMultiplier != nil {
		pricing.OutputPricePerToken *= *iv.OutputMultiplier
		pricing.OutputPricePerTokenPriority *= *iv.OutputMultiplier
	}
	if iv.CacheWritePrice != nil {
		priority := ConfigTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *iv.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *iv.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *iv.CacheWritePrice
		if iv.CacheWrite1hPrice == nil {
			// The interval's short cache override also replaces hourly pricing when
			// hourly is absent, so inherited TTL amounts cannot silently keep a
			// different default.
			pricing.CacheCreation1hPrice = *iv.CacheWritePrice
		}
	} else if iv.CacheWriteMultiplier != nil {
		pricing.CacheCreationPricePerToken *= *iv.CacheWriteMultiplier
		pricing.CacheCreationPricePerTokenPriority *= *iv.CacheWriteMultiplier
		pricing.CacheCreation5mPrice *= *iv.CacheWriteMultiplier
		pricing.CacheCreation1hPrice *= *iv.CacheWriteMultiplier
	}
	if iv.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *iv.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if iv.CacheReadPrice != nil {
		priority := ConfigTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *iv.CacheReadPrice)
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
		// A manual aggregate cache price also owns the image subset; retaining the
		// catalog image rate would partially bypass this administrator override.
		pricing.ImageCacheReadPriceExplicit = false
		pricing.CacheReadPricePerTokenPriority = priority
	} else if iv.CacheReadMultiplier != nil {
		pricing.CacheReadPricePerToken *= *iv.CacheReadMultiplier
		pricing.ImageCacheReadPricePerToken *= *iv.CacheReadMultiplier
		pricing.CacheReadPricePerTokenPriority *= *iv.CacheReadMultiplier
	}
	// Intervals carry no independent image fields; apply the card image rules
	// after selecting text buckets so zero image input can inherit that tier's
	// text price.
	if chPricing != nil {
		applyConfigImagePriceOverrides(pricing, chPricing)
		ApplyConfigFastModeMultiplier(pricing, chPricing)
		ApplyConfigFlexMultiplier(pricing, chPricing)
	}
	return pricing
}

// GetRequestTierPrice returns the first matching label amount, or zero on a
// miss. The value-only result combines missing and explicit free prices;
// callers needing availability use GetRequestTierPriceValue.
func GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	price, ok := GetRequestTierPriceValue(resolved, tierLabel)
	if !ok {
		return 0
	}
	return price
}

// GetRequestTierPriceValue returns the first trimmed, case-insensitive label
// price and its presence. Nil resolution, blank labels, and unmatched tiers
// return false. Video labels also share canonical resolution aliases. A supplied
// zero amount returns true and remains a free price.
func GetRequestTierPriceValue(resolved *ResolvedPricing, tierLabel string) (float64, bool) {
	tierLabel = strings.TrimSpace(tierLabel)
	if resolved == nil || tierLabel == "" {
		return 0, false
	}
	for _, tier := range resolved.RequestTiers {
		label := strings.TrimSpace(tier.TierLabel)
		if resolved.Mode == BillingModeVideo {
			// Normalize both saved and requested labels so historical aliases and
			// custom dimensions select the same unit during quotation and settlement.
			label = NormalizeVideoBillingResolutionOrDefault(label)
			tierLabel = NormalizeVideoBillingResolutionOrDefault(tierLabel)
		}
		if strings.TrimSpace(tier.TierLabel) != "" && strings.EqualFold(label, tierLabel) && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice, true
		}
	}
	return 0, false
}

// GetRequestTierPriceByContext returns the first unlabeled context amount, or
// zero on a miss. The value-only result merges absence with explicit free
// prices; callers needing that distinction use
// GetRequestTierPriceByContextValue.
func GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	price, ok := GetRequestTierPriceByContextValue(resolved, totalContextTokens)
	if !ok {
		return 0
	}
	return price
}

// GetRequestTierPriceByContextValue returns the first priced unlabeled range
// containing totalContextTokens. Ranges use (min,max] in supplied order, and
// labeled tiers are skipped. Nil resolution or no match returns false; explicit
// zero remains present.
func GetRequestTierPriceByContextValue(resolved *ResolvedPricing, totalContextTokens int) (float64, bool) {
	if resolved == nil {
		return 0, false
	}
	for _, iv := range resolved.RequestTiers {
		if strings.TrimSpace(iv.TierLabel) != "" {
			continue
		}
		if totalContextTokens > iv.MinTokens && (iv.MaxTokens == nil || totalContextTokens <= *iv.MaxTokens) && iv.PerRequestPrice != nil {
			return *iv.PerRequestPrice, true
		}
	}
	return 0, false
}

// ResolveRequestUnitPrice returns an available label, context, or default
// amount in that order. Nil resolution returns false, and explicit zero prices
// remain available. A non-nil totalContext permits the first matching unlabeled
// context range.
//
// With unknown context in per-request mode, every unlabeled priced tier must
// equal the available default before a fixed default quote is safe. A matched
// label already determines a fixed price. Image and video modes can use their
// default directly; no eligible tier or default returns false.
func ResolveRequestUnitPrice(resolved *ResolvedPricing, label string, totalContext *int) (float64, bool) {
	if resolved == nil {
		return 0, false
	}
	if value, found := GetRequestTierPriceValue(resolved, label); found {
		return value, true
	}
	hasDefault := resolved.DefaultPerRequestPricePresent ||
		resolved.DefaultPerRequestPrice > 0 ||
		resolved.ConfigPricing != nil && resolved.ConfigPricing.PerRequestPrice != nil
	if totalContext != nil {
		if value, found := GetRequestTierPriceByContextValue(resolved, *totalContext); found {
			return value, true
		}
	} else if resolved.Mode == BillingModePerRequest {
		for _, iv := range resolved.RequestTiers {
			if strings.TrimSpace(iv.TierLabel) != "" || iv.PerRequestPrice == nil {
				continue
			}
			// A fixed reservation has no context observation; require every unlabeled
			// amount to equal the available default before presenting one price,
			// including free defaults.
			if !hasDefault || *iv.PerRequestPrice != resolved.DefaultPerRequestPrice {
				return 0, false
			}
		}
	}
	if hasDefault {
		return resolved.DefaultPerRequestPrice, true
	}
	return 0, false
}

// HasExplicitPricingPrice reports supplied amount pointers for the card's
// selected mode. Empty mode uses token fields; request, image, and video modes
// use flat or interval unit prices. Zero qualifies, while multiplier fields
// alone supply no amount.
func HasExplicitPricingPrice(p ModelPricingEntry) bool {
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		if p.PerRequestPrice != nil {
			return true
		}
		for _, iv := range p.Intervals {
			if iv.PerRequestPrice != nil {
				return true
			}
		}
		return false
	}
	if p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil || p.CacheWrite1hPrice != nil ||
		p.CacheReadPrice != nil || p.ImageInputPrice != nil || p.ImageOutputPrice != nil {
		return true
	}
	for _, iv := range p.Intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil || iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil {
			return true
		}
	}
	return false
}

// NormalizePriceModelName trims whitespace and lowercases the complete pricing
// model name.
func NormalizePriceModelName(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// ApplyConfigPrice returns token prices with card defaults and modifiers
// applied. A nil card returns the supplied pricing pointer. With a card,
// pricing must be non-nil; scalar and nested context price copies receive
// validated overrides.
//
// Amount scaling follows overrides, then Fast, Flex, and effort pointers are
// set from the card. Time and remaining modifier metadata can still reference
// source records, so callers keep them stable. Card image defaults apply even
// when image amounts are omitted, setting their calculation-specific free or
// inherited behavior.
func ApplyConfigPrice(pricing *ModelPricing, configPricing *ModelPricingEntry) *ModelPricing {
	if configPricing == nil {
		return pricing
	}
	// Copy scalar price fields before overrides, then recursively copy changed
	// context amounts so catalog readers keep their own published price records.
	cloned := *pricing
	pricing = &cloned
	ApplyConfigTokenPriceOverrides(pricing, configPricing)
	multiplier, configured := NormalizedPriceMultiplier(configPricing)
	if configured {
		pricing = MultiplyModelPricing(pricing, multiplier)
	}
	ApplyConfigFastModeMultiplier(pricing, configPricing)
	ApplyConfigFlexMultiplier(pricing, configPricing)
	if configPricing.MaxReasoningEffortMultiplier != nil {
		pricing.MaxReasoningEffortMultiplier = configPricing.MaxReasoningEffortMultiplier
	}
	return pricing
}
