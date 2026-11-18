package pricing

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// card.go defines, validates, clones and matches configured model price cards.
// Model cards select billing units and hold optional amounts, so a nil price
// can inherit another source while an explicit zero remains free. Context
// ranges use (min,max] endpoints; request and media tiers can also select
// amounts by label without requiring disjoint context ranges.
//
// Card validation copies and sorts ranges for token overlap checks while
// leaving the supplied order intact for runtime selection. Matching prefers an
// effective exact card over the first trailing-star wildcard, and returns a
// cloned selected card so later resolution can replace amounts. Provider cost
// rules carry their own ordered selectors and cards; their calculation uses
// separate rules from user-facing token inheritance.

// These mode labels determine which supplied price fields and usage units
// resolution and calculation select for a model card.
const (
	BillingModeToken      BillingMode = "token"       // token-count and context pricing
	BillingModePerRequest BillingMode = "per_request" // request-count unit pricing
	BillingModeImage      BillingMode = "image"       // generated-image count pricing
	BillingModeVideo      BillingMode = "video"       // output-video second pricing
)

// BillingMode selects the token, request, image, or video units used for
// charging.
type BillingMode string

// ProviderStatsPricingRule contains provider-cost selectors and their
// independent model cards. GroupIDs and ProviderIDs are alternative selectors:
// either matching list admits a rule, while both empty admit nothing. Callers
// supply rules in order, and selection uses the first rule that can calculate
// this model's cost.
type ProviderStatsPricingRule struct {
	ID              int64   // stored statistics-rule identifier
	PricingConfigID int64   // owning shared pricing configuration
	Name            string  // administrator statistics-rule display name
	GroupIDs        []int64 // matching group identifier list
	ProviderIDs     []int64 // matching provider identifier list
	// SortOrder is the stored ordering key. PostgreSQL loads rules by this value
	// then ID; pure rule selection uses the supplied slice order.
	SortOrder int // stored statistics-rule ordering key
	// Pricing holds this rule's model cards. Selector matches continue to later
	// rules when the model lacks a calculable card cost.
	Pricing   []ModelPricingEntry // independent provider-cost model cards
	CreatedAt time.Time           // stored rule creation time
	UpdatedAt time.Time           // stored rule update time
}

// ModelPricingEntry contains optional model-card prices and billing modifiers.
// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, CacheReadPrice,
// ImageInputPrice, ImageOutputPrice, and PerRequestPrice retain amount
// presence: nil is unspecified and zero is supplied free pricing where that
// bucket's calculation permits it.
type ModelPricingEntry struct {
	// ReferenceModel selects another model in the same price configuration.
	// Reference entries contain no amounts or modifiers; resolution uses the
	// target card and its catalog identity together.
	ReferenceModel string `json:"reference_model,omitempty"`

	ID int64 `json:"id,omitempty"` // stored model-card identifier
	// owning shared pricing configuration
	PricingConfigID int64 `json:"pricing_config_id,omitempty"`
	// Models holds complete model names or trailing-star prefix patterns. User
	// card matching normalizes case and surrounding whitespace; statistics
	// matching expects trimmed input.
	Models []string `json:"models"` // configured model identity patterns
	// BillingMode selects token, request, image, or video units. Empty means token
	// in resolution; changing mode changes which amount fields establish effective
	// pricing.
	// configured billing-unit selection
	BillingMode BillingMode `json:"billing_mode"`
	// PriceMultiplier scales supplied amounts after overrides. Nil leaves amounts
	// unchanged; configuration validation requires an amount, and direct negative
	// stored factors normalize to zero.
	// configured amount scaling factor
	PriceMultiplier *float64 `json:"price_multiplier"`
	// FastModeMultiplier is the legacy Fast factor. FastMultiplier takes
	// precedence when applying a card, while a legacy factor alone does not make a
	// card effective.
	// legacy configured Fast scaling factor
	FastModeMultiplier *float64 `json:"fast_mode_multiplier"`
	// FastMultiplier overrides Fast or priority scaling when supplied and takes
	// precedence over FastModeMultiplier during card application. Runtime copies
	// its value into resolved metadata.
	// preferred configured Fast scaling factor
	FastMultiplier *float64 `json:"fast_multiplier,omitempty"`
	// FlexMultiplier overrides flex scaling when supplied. Nil inherits base
	// metadata; configuration validation requires a finite positive factor for
	// token-mode cards.
	// configured Flex scaling factor
	FlexMultiplier *float64 `json:"flex_multiplier,omitempty"`
	// MaxReasoningEffortMultiplier overrides the final max-effort factor when
	// supplied. Nil retains base metadata; configuration validation requires a
	// finite positive token-mode factor.
	// configured max-effort scaling factor
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier,omitempty"`
	InputPrice                   *float64 `json:"input_price"`  // USD per text-input token
	OutputPrice                  *float64 `json:"output_price"` // USD per text-output token
	// USD per short-TTL cache-write token
	CacheWritePrice *float64 `json:"cache_write_price"`
	// CacheWrite1hPrice supplies an independent hourly cache amount, including
	// zero. When it is absent, a CacheWritePrice override also replaces the hourly
	// amount.
	// USD per hourly cache-write token
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price"`
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price"` // USD per twelve-hour write token
	// USD per cache-read token
	CacheReadPrice *float64 `json:"cache_read_price"`
	// ImageInputPrice supplies the image-input subset rate. During token-card
	// application, omitted or zero uses the selected text input amount for that
	// tier.
	// USD per image-input token
	ImageInputPrice *float64 `json:"image_input_price"`
	// ImageOutputPrice supplies a separately recorded image-output rate. During
	// token-card application, omitted or zero becomes free instead of inheriting
	// text output.
	// USD per image-output token
	ImageOutputPrice *float64 `json:"image_output_price"`
	// USD per request or media unit
	PerRequestPrice *float64 `json:"per_request_price"`
	// Intervals holds context ranges or labeled unit tiers according to
	// BillingMode. User resolution inherits missing token buckets, while custom
	// statistics intervals replace their flat buckets.
	// configured context or label tiers
	Intervals []PricingInterval `json:"intervals"`
	// TimePricing supplies this card's token schedule. Modifier merging replaces
	// an existing effective schedule only for nonempty periods; MultiplierAt
	// returns one for an empty schedule.
	// configured daily token-price periods
	TimePricing *TimePricingConfig `json:"time_pricing,omitempty"`
	// stored card creation time
	CreatedAt time.Time `json:"created_at,omitempty"`
	// stored card update time
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// TimePricingConfig defines daily token-price periods and the intended zone.
// Timezone, WeekdaysOnly, and Periods describe one schedule; consumers load the
// named zone and use its local date and clock when applying the supplied
// periods.
type TimePricingConfig struct {
	// Timezone names the schedule's intended zone for validation and facade
	// loading. MultiplierAt uses its supplied location and does not load this name
	// itself.
	Timezone string `json:"timezone"` // configured schedule timezone name
	// WeekdaysOnly limits this schedule to Monday through Friday in the supplied
	// location. Weekend instants receive one regardless of configured period
	// matches.
	// weekday-only schedule selection flag
	WeekdaysOnly bool `json:"weekdays_only,omitempty"`
	// Periods holds daily multiplier ranges. Nil or empty periods disable lookup;
	// parsed periods must be nonoverlapping and are sorted independently of this
	// slice.
	// configured daily multiplier ranges
	Periods []TimePricingPeriod `json:"periods"`
}

// TimePricingPeriod defines one daily wall-clock range and its price
// multiplier. StartTime and EndTime are [start,end) clocks at minute or second
// precision. The parsed end may represent midnight at the end of the day, while
// the start remains within the day.
type TimePricingPeriod struct {
	StartTime string `json:"start_time"` // inclusive HH:mm or HH:mm:ss start
	// EndTime is exclusive and accepts midnight text as the end of the day. The
	// parser requires advancing ranges and rejects identical start/end spellings.
	EndTime string `json:"end_time"` // exclusive HH:mm or HH:mm:ss end
	// Multiplier scales token amounts within the period. Parsing requires finite
	// values at least 0.01 with at most two decimals within its binary rounding
	// tolerance.
	Multiplier float64 `json:"multiplier"` // daily period token-price factor
}

// PricingInterval contains context endpoints and optional bucket or unit
// prices. InputMultiplier, OutputMultiplier, CacheWriteMultiplier, and
// CacheReadMultiplier scale inherited user-card amounts when their
// corresponding amount is omitted; custom statistics calculation ignores those
// factors.
//
// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, and
// CacheReadPrice retain pointer presence, including zero. User-card interval
// conversion inherits omitted buckets from defaults, while custom statistics
// intervals replace those buckets with zero when omitted. PerRequestPrice
// supplies a labeled or context-selected unit amount.
type PricingInterval struct {
	ID int64 `json:"id,omitempty"` // stored interval row identifier
	// owning model-card row identifier
	PricingID int64 `json:"pricing_id,omitempty"`
	MinTokens int   `json:"min_tokens"` // excluded lower context-token endpoint
	// MaxTokens is the included upper context endpoint; nil makes the interval
	// unbounded. Validation requires supplied endpoints to be positive and greater
	// than MinTokens.
	MaxTokens *int `json:"max_tokens"` // included upper context-token endpoint
	// TierLabel selects labeled request or media prices. User request-context
	// lookup skips nonblank labels; token interval matching reads context
	// endpoints independently of this label.
	TierLabel   string   `json:"tier_label"`   // request or media tier label
	InputPrice  *float64 `json:"input_price"`  // USD per interval input token
	OutputPrice *float64 `json:"output_price"` // USD per interval output token
	// USD per short-TTL cache-write token
	CacheWritePrice *float64 `json:"cache_write_price"`
	// CacheWrite1hPrice sets a separate hourly cache amount and enables TTL
	// details. An interval CacheWritePrice override also covers hourly amounts
	// when this is nil.
	// USD per hourly cache-write token
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price"`
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price"` // USD per twelve-hour write token
	// USD per interval cache-read token
	CacheReadPrice *float64 `json:"cache_read_price"`
	// interval input-price scaling factor
	InputMultiplier *float64 `json:"input_multiplier,omitempty"`
	// interval output-price scaling factor
	OutputMultiplier *float64 `json:"output_multiplier,omitempty"`
	// interval cache-write scaling factor
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier,omitempty"`
	// interval cache-read scaling factor
	CacheReadMultiplier *float64 `json:"cache_read_multiplier,omitempty"`
	PerRequestPrice     *float64 `json:"per_request_price"` // USD per matched unit
	SortOrder           int      `json:"sort_order"`        // stored interval ordering key
	// stored interval creation time
	CreatedAt time.Time `json:"created_at,omitempty"`
	// stored interval update time
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// IsValid reports whether m is a supported mode or the empty default. Token,
// per-request, image, and video are accepted. This check validates the mode
// name; required amounts are validated separately.
func (m BillingMode) IsValid() bool {
	switch m {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo, "":
		return true
	}
	return false
}

// IsValidUsageFilter reports whether m can be supplied as a usage mode filter.
// The empty value and all four supported billing modes are accepted. Other
// strings return false without normalization.
func (m BillingMode) IsValidUsageFilter() bool {
	switch m {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo, "":
		return true
	}
	return false
}

// FindMatchingInterval returns the first supplied range containing totalTokens.
// Ranges are (min,max], and nil MaxTokens is unbounded. Zero misses a range
// starting at zero; the returned pointer references the input slice and remains
// shared.
func FindMatchingInterval(intervals []PricingInterval, totalTokens int) *PricingInterval {
	for i := range intervals {
		iv := &intervals[i]
		if totalTokens > iv.MinTokens && (iv.MaxTokens == nil || totalTokens <= *iv.MaxTokens) {
			return iv
		}
	}
	return nil
}

// GetTierByLabel returns the first interval with a case-insensitive label
// match. The receiver must be non-nil; labels are compared without trimming. A
// miss returns nil, and a matched pointer refers to the receiver's shared
// interval slice.
func (p *ModelPricingEntry) GetTierByLabel(label string) *PricingInterval {
	labelLower := strings.ToLower(label)
	for i := range p.Intervals {
		if strings.EqualFold(p.Intervals[i].TierLabel, labelLower) {
			return &p.Intervals[i]
		}
	}
	return nil
}

// HasEffectivePricing reports amounts or supported modifiers for the selected
// mode. Nil cards return false and empty mode uses token rules. Request and
// media modes require flat or interval amount pointers, including zero.
//
// Token mode accepts text, cache, or image amounts, supported tier or effort
// factors, nonempty time periods, and interval token amounts or factors.
// PriceMultiplier and the legacy FastModeMultiplier alone do not make a card
// effective; resolution needs other price or rule data to match such cards.
func (p *ModelPricingEntry) HasEffectivePricing() bool {
	if p == nil {
		return false
	}
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	switch mode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		if p.PerRequestPrice != nil {
			return true
		}
		for i := range p.Intervals {
			if p.Intervals[i].PerRequestPrice != nil {
				return true
			}
		}
		return false
	default:
		if p.InputPrice != nil ||
			p.OutputPrice != nil ||
			p.CacheWritePrice != nil ||
			p.CacheWrite1hPrice != nil || p.CacheWrite30mPrice != nil || p.CacheWrite12hPrice != nil ||
			p.CacheReadPrice != nil ||
			p.ImageInputPrice != nil ||
			p.ImageOutputPrice != nil ||
			p.FastMultiplier != nil ||
			p.FlexMultiplier != nil ||
			p.MaxReasoningEffortMultiplier != nil ||
			(p.TimePricing != nil && len(p.TimePricing.Periods) > 0) {
			return true
		}
		for i := range p.Intervals {
			iv := p.Intervals[i]
			if iv.InputPrice != nil ||
				iv.OutputPrice != nil ||
				iv.CacheWritePrice != nil ||
				iv.CacheWrite1hPrice != nil || iv.CacheWrite30mPrice != nil || iv.CacheWrite12hPrice != nil ||
				iv.CacheReadPrice != nil ||
				iv.InputMultiplier != nil ||
				iv.OutputMultiplier != nil ||
				iv.CacheWriteMultiplier != nil ||
				iv.CacheReadMultiplier != nil {
				return true
			}
		}
	}
	return false
}

// Clone returns a card with separate amount storage, model and interval slices,
// and time settings. Interval endpoints and all supported multiplier pointers
// are copied, preserving nil and explicit zero distinctions. Callers can mutate
// the resulting nested values independently.
func (p ModelPricingEntry) Clone() ModelPricingEntry {
	cp := p
	// Independent amount and multiplier pointers let callers alter the cloned card
	// while the cached source remains readable.
	ClonePricingAmounts(&cp.PriceMultiplier, &cp.FastModeMultiplier, &cp.FastMultiplier, &cp.FlexMultiplier,
		&cp.MaxReasoningEffortMultiplier, &cp.InputPrice, &cp.OutputPrice, &cp.CacheWritePrice,
		&cp.CacheWrite1hPrice, &cp.CacheWrite30mPrice, &cp.CacheWrite12hPrice, &cp.CacheReadPrice, &cp.ImageInputPrice, &cp.ImageOutputPrice, &cp.PerRequestPrice)
	if p.Models != nil {
		cp.Models = make([]string, len(p.Models))
		copy(cp.Models, p.Models)
	}
	if p.Intervals != nil {
		cp.Intervals = make([]PricingInterval, len(p.Intervals))
		copy(cp.Intervals, p.Intervals)
		for i := range cp.Intervals {
			iv := &cp.Intervals[i]
			ClonePricingAmounts(&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheWrite1hPrice, &iv.CacheWrite30mPrice, &iv.CacheWrite12hPrice,
				&iv.CacheReadPrice, &iv.InputMultiplier, &iv.OutputMultiplier, &iv.CacheWriteMultiplier,
				&iv.CacheReadMultiplier, &iv.PerRequestPrice)
			if iv.MaxTokens != nil {
				maxTokens := *iv.MaxTokens
				iv.MaxTokens = &maxTokens
			}
		}
	}
	if p.TimePricing != nil {
		cp.TimePricing = &TimePricingConfig{
			Timezone:     p.TimePricing.Timezone,
			WeekdaysOnly: p.TimePricing.WeekdaysOnly,
		}
		if p.TimePricing.Periods != nil {
			cp.TimePricing.Periods = append([]TimePricingPeriod(nil), p.TimePricing.Periods...)
		}
	}
	return cp
}

// ClonePricingAmounts replaces each non-nil amount with newly allocated value
// storage. Every outer field pointer must be non-nil. Nil inner amounts remain
// nil and explicit zero amounts retain their presence.
func ClonePricingAmounts(fields ...**float64) {
	for _, field := range fields {
		if *field != nil {
			value := **field
			*field = &value
		}
	}
}

// ValidateIntervals checks individual fields and mode-specific context overlap.
// A copied slice is sorted by minimum for validation, leaving runtime input
// order unchanged. Empty lists succeed; token and other non-media modes require
// nonoverlap and an unbounded range last.
//
// Request, image, and video modes permit overlapping ranges because runtime
// label or configuration-order selection determines their price. All modes
// still check nonnegative minima, positive maxima above minima, finite
// nonnegative amounts, and finite positive interval factors. Error indices
// refer to the copied minimum-sorted order. Video labels must fit stored
// resolution columns and remain unique after alias and case normalization.
func ValidateIntervals(intervals []PricingInterval, mode BillingMode) error {
	if len(intervals) == 0 {
		return nil
	}
	sorted := make([]PricingInterval, len(intervals))
	copy(sorted, intervals)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].MinTokens < sorted[j].MinTokens
	})

	videoLabels := make(map[string]struct{})
	for i := range sorted {
		if mode == BillingModeVideo && strings.TrimSpace(sorted[i].TierLabel) != "" {
			label, valid := LookupVideoBillingResolution(sorted[i].TierLabel)
			if !valid {
				return fmt.Errorf("invalid video resolution %q", sorted[i].TierLabel)
			}
			if _, duplicate := videoLabels[label]; duplicate {
				return fmt.Errorf("duplicate video resolution %q", label)
			}
			videoLabels[label] = struct{}{}
		}
		if err := ValidateSingleInterval(&sorted[i], i); err != nil {
			return err
		}
	}

	// Request and media tiers can share context ranges because runtime label or
	// supplied-order selection chooses their unit price.
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		return nil
	}
	return ValidateIntervalOverlap(sorted)
}

// ValidateSingleInterval checks one interval's endpoints before validating
// amounts and factors. The interval must be non-nil; idx is a zero-based
// diagnostic position. Nil maximum is unbounded, while a supplied maximum must
// be positive and exceed the minimum.
func ValidateSingleInterval(iv *PricingInterval, idx int) error {
	if iv.MinTokens < 0 {
		return fmt.Errorf("interval #%d: min_tokens (%d) must be >= 0", idx+1, iv.MinTokens)
	}
	if iv.MaxTokens != nil {
		if *iv.MaxTokens <= 0 {
			return fmt.Errorf("interval #%d: max_tokens (%d) must be > 0", idx+1, *iv.MaxTokens)
		}
		if *iv.MaxTokens <= iv.MinTokens {
			return fmt.Errorf("interval #%d: max_tokens (%d) must be > min_tokens (%d)",
				idx+1, *iv.MaxTokens, iv.MinTokens)
		}
	}
	return ValidateIntervalPrices(iv, idx)
}

// ValidateIntervalPrices checks optional amounts first, then optional interval
// factors. The interval must be non-nil and idx supplies the zero-based error
// position. Nil values are skipped; amounts must be finite and nonnegative, and
// factors finite and positive.
func ValidateIntervalPrices(iv *PricingInterval, idx int) error {
	// name identifies the amount for an error, and val retains price presence;
	// omitted values skip checks while supplied zero remains a valid free amount.
	prices := []struct {
		name string   // configuration field diagnostic name
		val  *float64 // optional value being validated
	}{
		{"input_price", iv.InputPrice},
		{"output_price", iv.OutputPrice},
		{"cache_write_price", iv.CacheWritePrice},
		{"cache_write_1h_price", iv.CacheWrite1hPrice},
		{"cache_write_30m_price", iv.CacheWrite30mPrice},
		{"cache_write_12h_price", iv.CacheWrite12hPrice},
		{"cache_read_price", iv.CacheReadPrice},
		{"per_request_price", iv.PerRequestPrice},
	}
	for _, p := range prices {
		if p.val != nil && (math.IsNaN(*p.val) || math.IsInf(*p.val, 0) || *p.val < 0) {
			return fmt.Errorf("interval #%d: %s must be >= 0", idx+1, p.name)
		}
	}
	// name identifies the factor for an error, and val retains its optional
	// presence; omitted factors skip validation while supplied factors require a
	// positive finite value.
	multipliers := []struct {
		name string   // configuration field diagnostic name
		val  *float64 // optional value being validated
	}{
		{"input_multiplier", iv.InputMultiplier},
		{"output_multiplier", iv.OutputMultiplier},
		{"cache_write_multiplier", iv.CacheWriteMultiplier},
		{"cache_read_multiplier", iv.CacheReadMultiplier},
	}
	for _, multiplier := range multipliers {
		if multiplier.val != nil && (math.IsNaN(*multiplier.val) || math.IsInf(*multiplier.val, 0) || *multiplier.val <= 0) {
			return fmt.Errorf("interval #%d: %s must be > 0", idx+1, multiplier.name)
		}
	}
	return nil
}

// ValidateIntervalOverlap checks nonoverlap in a supplied minimum-sorted
// interval slice. Callers validate endpoints beforehand. An unbounded interval
// must be last; equal adjoining endpoints are valid because the later lower
// endpoint is excluded.
func ValidateIntervalOverlap(sorted []PricingInterval) error {
	for i, iv := range sorted {
		// An earlier unbounded range contains every later positive context, so token
		// pricing requires that range to end the list.
		if iv.MaxTokens == nil && i < len(sorted)-1 {
			return fmt.Errorf("interval #%d: unbounded interval (max_tokens=null) must be the last one",
				i+1)
		}
		if i == 0 {
			continue
		}
		prev := sorted[i-1]
		// Equal endpoints give adjacent (min,max] ranges one owner per token count; a
		// larger preceding maximum makes the ranges overlap.
		if prev.MaxTokens == nil || *prev.MaxTokens > iv.MinTokens {
			return fmt.Errorf("interval #%d and #%d overlap: prev max=%s > cur min=%d",
				i, i+1, FormatMaxTokensLabel(prev.MaxTokens), iv.MinTokens)
		}
	}
	return nil
}

// FormatMaxTokensLabel returns the decimal endpoint or infinity for an
// unbounded maximum.
func FormatMaxTokensLabel(max *int) string {
	if max == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *max)
}

// MatchPriceCard returns a cloned effective card matching the normalized
// complete model name. It ignores ineffective rows and normalizes case and
// surrounding whitespace for candidates and patterns. A trailing star matches a
// prefix; other wildcard syntax has no special meaning.
//
// An exact match anywhere in the list takes precedence over wildcard matches.
// Among wildcard-only matches, the first card and pattern encountered wins.
// Cloning separates amount pointers, model labels, intervals, and time settings
// so callers can resolve or edit the selected card independently of the
// supplied slice.
func MatchPriceCard(cards []ModelPricingEntry, candidate string) *ModelPricingEntry {
	candidate = NormalizePriceModelName(candidate)
	var wildcard *ModelPricingEntry
	for i := range cards {
		entry := &cards[i]
		if !entry.HasEffectivePricing() {
			continue
		}
		for _, pattern := range entry.Models {
			normalized := NormalizePriceModelName(pattern)
			if normalized == candidate {
				cp := entry.Clone()
				return &cp
			}
			if strings.HasSuffix(normalized, "*") && strings.HasPrefix(candidate, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
				cp := entry.Clone()
				wildcard = &cp
			}
		}
	}
	return wildcard
}
