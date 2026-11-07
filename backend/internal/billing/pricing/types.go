package pricing

import "time"

// types.go defines token usage, calculation inputs, resolved amounts and
// display price records.
// Token usage contains image and cache details that need different treatment
// from independent token buckets. Ordinary calculation splits image input from
// aggregate input and removes image output from text output, while cache TTL
// details subdivide reported cache creation for the selected price rules.
//
// ModelPricing retains absolute amounts, presence markers, ordered context
// tiers, and optional modifiers for those calculations. A numeric zero can
// therefore mean free usage, text-rate inheritance, or an omitted display
// amount depending on its owning field. Display records expose rate-scaled USD
// units and separate card time or effort controls; CostInput supplies the
// distinct schedule instants and loaded locations rather than reading clocks or
// zones during pure calculation.

// ModelPricing contains per-token USD amounts and the rules used to calculate
// usage. PriorityInputPresent, PriorityOutputPresent, PriorityCacheReadPresent,
// and PriorityCacheWritePresent retain supplied free priority amounts so zero
// can select a bucket rather than inherit a standard amount.
type ModelPricing struct {
	// TimePricing carries catalog daily rules. CalculateTokenCost evaluates them
	// only for catalog-source resolution with its supplied model instant and
	// location.
	TimePricing *TimePricingConfig // catalog daily price periods
	// CatalogSource records price provenance. HasAnyDisplayTokenPricing accepts
	// any nonempty value other than unpriced, including catalog amounts that are
	// all zero.
	CatalogSource             string // catalog price provenance label
	PriorityInputPresent      bool   // priority input amount supplied
	PriorityOutputPresent     bool   // priority output amount supplied
	PriorityCacheReadPresent  bool   // priority cache-read amount supplied
	PriorityCacheWritePresent bool   // priority cache-write amount supplied
	// ContextPrices holds ascending absolute tiers with non-nil prices.
	// ComputeTokenBreakdown selects the last exceeded threshold; callers keep
	// retained metadata stable during calculation.
	// absolute catalog context tiers
	ContextPrices              []ContextModelPrice
	InputPricePerToken         float64 // USD per text-input token
	InputPricePerTokenPriority float64 // USD per priority-input token
	// ImageCacheReadPricePerToken and ImageCacheReadPriceExplicit distinguish a
	// supplied zero from a missing image-cache rate. Missing rates use aggregate
	// cache pricing; explicit administrator cache overrides disable the subdivision.
	ImageCacheReadPricePerToken float64 // USD per cached image token
	ImageCacheReadPriceExplicit bool    // cached image amount supplied
	// ImageInputPricePerToken prices uncached image input after removing an
	// explicitly reported cached image subset. Zero uses the text input price.
	ImageInputPricePerToken            float64 // USD per image-input token
	OutputPricePerToken                float64 // USD per text-output token
	OutputPricePerTokenPriority        float64 // USD per priority-output token
	CacheCreationPricePerToken         float64 // USD per aggregate cache-write token
	CacheCreationPricePerTokenPriority float64 // USD per priority cache-write token
	// CacheCreationPriceExplicit records configured cache-write overrides or a
	// present catalog zero with provenance. Calculations use the amount and TTL
	// fields directly.
	CacheCreationPriceExplicit     bool    // explicit cache-write pricing marker
	CacheReadPricePerToken         float64 // USD per cache-read token
	CacheReadPricePerTokenPriority float64 // USD per priority cache-read token
	CacheCreation5mPrice           float64 // USD per short-TTL cache-write token
	CacheCreation1hPrice           float64 // USD per hourly cache-write token
	// SupportsCacheBreakdown permits TTL detail charging when at least one TTL
	// rate is positive. Otherwise ComputeCacheCreationCost uses the supplied
	// aggregate rate.
	SupportsCacheBreakdown bool // TTL detail pricing support
	// SupportsServiceTier records service-tier support for resolved metadata and
	// administrator Flex display. Token calculation selects configured factors and
	// amounts independently of this flag.
	SupportsServiceTier bool // catalog service-tier support flag
	// FastModeMultiplier takes precedence over FastMultiplier during calculation
	// when supplied, including zero. Configured factor application stores a copied
	// value here.
	FastModeMultiplier *float64 // preferred resolved Fast factor
	// FastMultiplier supplies catalog Fast scaling when FastModeMultiplier is
	// absent. A supplied zero remains a configured factor rather than selecting
	// priority amounts.
	FastMultiplier *float64 // catalog or configured Fast factor
	// FlexMultiplier scales token amounts for the normalized flex service tier
	// when supplied. An absent pointer leaves that tier at unit scaling.
	FlexMultiplier *float64 // configured Flex scaling factor
	// MaxReasoningEffortMultiplier scales token costs when the final effort
	// normalizes to max and the stored amount is positive. An absent value leaves
	// costs unchanged.
	MaxReasoningEffortMultiplier *float64 // final max-effort scaling factor
	LongContextInputThreshold    int      // total input-context token threshold
	// LongContextThresholdInclusive includes the stored threshold in
	// multiplier-tier eligibility when true. Absolute context tiers use separately
	// converted strict thresholds.
	LongContextThresholdInclusive bool    // inclusive multiplier threshold flag
	LongContextInputMultiplier    float64 // long-context text and cache factor
	LongContextOutputMultiplier   float64 // long-context text-output price factor
	ImageOutputPricePerToken      float64 // USD per image-output token
	// ImageOutputPriceExplicit keeps a zero image-output amount free. When false
	// and the amount is zero, ComputeTokenBreakdown uses the selected text-output
	// price.
	ImageOutputPriceExplicit bool // explicit image-output pricing marker
}

// ContextModelPrice supplies absolute token amounts strictly above Threshold.
// Catalog conversion shifts inclusive xAI thresholds before constructing this
// record, allowing calculation and display to use the same strict internal
// endpoint.
type ContextModelPrice struct {
	Threshold int // strict lower context-token threshold
	// Pricing supplies the complete tier amount record and must be non-nil for
	// calculation. Retained modifier and time pointers remain stable during reads.
	Pricing *ModelPricing // absolute selected tier amounts
}

// UsageTokens contains reported aggregate counts and image or cache detail
// counts. CacheCreation5mTokens and CacheCreation1hTokens describe
// CacheCreationTokens for TTL pricing; positive overreported details are
// normalized to a positive aggregate while missing details can use aggregate
// charging.
type UsageTokens struct {
	InputTokens int // reported aggregate input-token count
	// ImageInputTokens retains the upstream image input subset, including any
	// ImageCacheReadTokens reported within it. Calculation removes that explicit
	// cached subset before capping image input at the ordinary InputTokens bucket.
	ImageInputTokens      int // image subset of input tokens
	ImageCacheReadTokens  int // explicit cached image token subset
	OutputTokens          int // aggregate reported output-token count
	CacheCreationTokens   int // aggregate cache-creation token count
	CacheReadTokens       int // reported cache-read token count
	CacheCreation5mTokens int // short-TTL cache-creation detail count
	CacheCreation1hTokens int // hourly cache-creation detail count
	// ImageOutputTokens identifies separately priced image output. Ordinary token
	// billing subtracts it from text output; custom statistics adds its amount to
	// reported output.
	ImageOutputTokens int // reported image-output token count
}

// CostBreakdown contains USD component amounts, the base total, and the user
// charge. Token components sum into TotalCost, while request and media
// calculations can populate only totals. Applied price factors may scale
// components and both totals together.
type CostBreakdown struct {
	InputCost         float64 // USD text-input component amount
	ImageInputCost    float64 // USD image-input component amount
	OutputCost        float64 // USD text-output component amount
	ImageOutputCost   float64 // USD image-output component amount
	CacheCreationCost float64 // USD cache-creation component amount
	CacheReadCost     float64 // USD cache-read component amount
	TotalCost         float64 // USD cost before user rate
	// ActualCost is TotalCost scaled by the effective user rate in ordinary
	// calculation. Applied time, tier, and effort factors can scale both totals
	// afterward.
	ActualCost  float64 // USD rate-scaled charge amount
	BillingMode string  // token, per_request, image, or video
	// LongContextBillingApplied reports that built-in context selection increased
	// ActualCost over its computed base charge. Merely exceeding a threshold does
	// not set it.
	LongContextBillingApplied bool // built-in context charge-increase marker
}

// ModelDisplayPricing contains rate-scaled USD amounts and optional price
// controls for a model. InputPricePerToken, ImageInputPricePerToken,
// OutputPricePerToken, CacheWritePricePerToken, CacheWrite1hPricePerToken,
// CacheReadPricePerToken, and ImageOutputPricePerToken describe standard token
// units.
//
// FastInputPricePerToken, FastImageInputPricePerToken, FastOutputPricePerToken,
// FastCacheWritePricePerToken, FastCacheWrite1hPricePerToken,
// FastCacheReadPricePerToken, and FastImageOutputPricePerToken describe the
// respective Fast units. Both sets include the caller's rate multiplier;
// configured effort and time controls are carried separately.
type ModelDisplayPricing struct {
	PricingMode                   string  // display classification: token, image, video, or unknown
	PriceStatus                   string  // priced or unpriced display state
	InputPricePerToken            float64 // USD per text-input token
	ImageInputPricePerToken       float64 // USD per image-input token
	OutputPricePerToken           float64 // USD per text-output token
	CacheWritePricePerToken       float64 // USD per short-TTL cache-write token
	CacheWrite1hPricePerToken     float64 // USD per hourly cache-write token
	CacheReadPricePerToken        float64 // USD per cache-read token
	ImageOutputPricePerToken      float64 // USD per image-output token
	FastInputPricePerToken        float64 // Fast USD per text-input token
	FastImageInputPricePerToken   float64 // Fast USD per image-input token
	FastOutputPricePerToken       float64 // Fast USD per text-output token
	FastCacheWritePricePerToken   float64 // Fast USD per short-TTL cache-write token
	FastCacheWrite1hPricePerToken float64 // Fast USD per hourly cache-write token
	FastCacheReadPricePerToken    float64 // Fast USD per cache-read token
	FastImageOutputPricePerToken  float64 // Fast USD per image-output token
	// ContextIntervals supplies separately displayed priced ranges when one flat
	// price cannot represent all contexts. Amounts and upper-endpoint pointers
	// reflect the chosen resolution.
	// available token-context price ranges
	ContextIntervals []ModelDisplayPricingInterval
	// MaxReasoningEffortMultiplier carries a positive non-unit effort factor
	// separately from token display amounts. withTokenDisplayModifiers copies its
	// value from the resolved base.
	MaxReasoningEffortMultiplier *float64 // displayed max-effort scaling factor
	// TimePricing carries a cloned card schedule whose periods validate. Display
	// construction does not load its zone here; catalog time factors may already
	// be included by the billing caller.
	TimePricing *TimePricingConfig // displayed configured daily schedule
	// VideoPrices contains only quoted resolutions, including free amounts. The
	// quote owns its map, and every value already includes the group multiplier.
	VideoPrices map[string]float64 // USD per video second
	// VideoPricePerSecond carries the explicit default for unlisted resolutions;
	// nil means unavailable, while a pointer to zero declares a free default.
	VideoPricePerSecond *float64 // default USD per second
	// ImagePriceSizes identifies quoted dimensions, including explicit free
	// values. Marketplace DTO conversion omits other sizes; with an empty list it
	// uses nonzero legacy amounts.
	ImagePriceSizes []string // known quoted image-size labels
	ImagePrice1K    float64  // USD per 1K output image
	ImagePrice2K    float64  // USD per 2K output image
	ImagePrice4K    float64  // USD per 4K output image
}

// ModelDisplayPricingInterval contains one (MinTokens,MaxTokens] displayed
// token-price range. InputPricePerToken, ImageInputPricePerToken,
// OutputPricePerToken, CacheWritePricePerToken, CacheWrite1hPricePerToken,
// CacheReadPricePerToken, and ImageOutputPricePerToken supply standard USD
// amounts. FastInputPricePerToken, FastImageInputPricePerToken,
// FastOutputPricePerToken, FastCacheWritePricePerToken,
// FastCacheWrite1hPricePerToken, FastCacheReadPricePerToken, and
// FastImageOutputPricePerToken supply their Fast amounts; all include the
// supplied display rate.
type ModelDisplayPricingInterval struct {
	MinTokens int // excluded lower context-token endpoint
	// MaxTokens is the included upper context endpoint; nil leaves the range
	// unbounded. Display construction may retain the caller's endpoint pointer.
	MaxTokens                     *int    // included upper context-token endpoint
	InputPricePerToken            float64 // USD per text-input token
	ImageInputPricePerToken       float64 // USD per image-input token
	OutputPricePerToken           float64 // USD per text-output token
	CacheWritePricePerToken       float64 // USD per short-TTL cache-write token
	CacheWrite1hPricePerToken     float64 // USD per hourly cache-write token
	CacheReadPricePerToken        float64 // USD per cache-read token
	ImageOutputPricePerToken      float64 // USD per image-output token
	FastInputPricePerToken        float64 // Fast USD per text-input token
	FastImageInputPricePerToken   float64 // Fast USD per image-input token
	FastOutputPricePerToken       float64 // Fast USD per text-output token
	FastCacheWritePricePerToken   float64 // Fast USD per short-TTL cache-write token
	FastCacheWrite1hPricePerToken float64 // Fast USD per hourly cache-write token
	FastCacheReadPricePerToken    float64 // Fast USD per cache-read token
	FastImageOutputPricePerToken  float64 // Fast USD per image-output token
}

// AudioPriceConfig contains optional USD rates for three audio usage units.
// RealtimePerMin, TTSPerMChars, and STTPerHour are absent when unresolved and
// may explicitly be zero; CalculateAudioCost returns an empty breakdown for
// absent or nonpositive selected amounts.
type AudioPriceConfig struct {
	RealtimePerMin *float64 // USD per Realtime minute
	TTSPerMChars   *float64 // USD per million TTS characters
	STTPerHour     *float64 // USD per STT hour
}

// CostInput supplies a complete model, reported usage, final tier and effort,
// and pricing context. ModelPricingAt with ModelTimeLocation evaluates catalog
// schedules; PricingAt with TimePricingLocation independently evaluates
// configured card schedules. Zero instants or nil locations disable their
// respective time factor here.
type CostInput struct {
	// ModelPricingAt is the instant for catalog daily rules. A zero instant
	// disables those rules here; Calculator.ProjectCostInput may supply its clock
	// before calling.
	ModelPricingAt time.Time // catalog schedule evaluation instant
	// ModelTimeLocation supplies the catalog rule's zone conversion. A nil
	// location disables the catalog time factor instead of loading a zone during
	// calculation.
	ModelTimeLocation *time.Location // catalog schedule clock location
	Model             string         // complete billing model identity
	Tokens            UsageTokens    // reported calculation token buckets
	// RequestCount supplies unit-count billing when UsageUnits is nonpositive. A
	// nonpositive count then becomes one in CalculatePerRequestCost.
	RequestCount int // request or media unit count
	// UsageUnits takes precedence over RequestCount when positive, supplying the
	// selected mode's continuous units. Callers validate finite units before
	// calculation.
	UsageUnits float64 // continuous mode-specific usage units
	SizeTier   string  // request or media tier label
	// RateMultiplier scales TotalCost into ActualCost. CalculateCost and token
	// breakdown clamp negative values to zero; callers validate finiteness
	// beforehand.
	RateMultiplier float64 // effective user charge factor
	// PricingAt is the instant for configured card periods. Zero disables card
	// time pricing even when ModelPricingAt separately supplies a catalog instant.
	PricingAt       time.Time // card schedule evaluation instant
	ServiceTier     string    // final forwarded service-tier label
	ReasoningEffort string    // final forwarded reasoning-effort label
	// TimePricingLocation supplies zone conversion for card periods. Missing
	// locations select the unit time factor; the billing facade loads configured
	// zone names.
	TimePricingLocation *time.Location // card schedule clock location
}
