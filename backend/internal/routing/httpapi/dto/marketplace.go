package dto

import (
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// marketplace.go converts routing marketplace results into JSON response
// structures while retaining optional price presence.
// A quoted image size can be free or have no available price, and both cases
// may carry a numeric zero before HTTP conversion. ImagePriceSizes retains
// which sizes were quoted; the mapper emits a pointer for a known free size
// and leaves an unavailable size absent from JSON.
//
// Marketplace groups also carry optional observations and route display
// attributes supplied by routing. Conversion allocates the group, model,
// interval, bucket, and period records used for JSON, while nested
// attributes, protocol and modality slices, and optional scalar pointers can
// retain source storage. Those retained values stay read-only while the
// response is encoded.

// ModelMarketplaceStats holds token and user totals for the public homepage.
type ModelMarketplaceStats struct {
	TodayTokens int64 `json:"today_tokens"` // today observed token count
	TotalTokens int64 `json:"total_tokens"` // reported total token count
	TotalUsers  int64 `json:"total_users"`  // public total user count
}

// ModelMarketplacePricing exposes token and media unit amounts with optional
// pricing modifiers for a public group quote.
type ModelMarketplacePricing struct {
	// VideoPrices carries the available per-resolution amounts, including zero.
	// The billing quote already applies the group multiplier to each value.
	VideoPrices map[string]float64 `json:"video_prices,omitempty"`
	// VideoPricePerSecond supplies the explicit default for unlisted resolutions.
	// A nil pointer is absent; zero remains an encoded free price.
	VideoPricePerSecond *float64 `json:"video_price_per_second,omitempty"`
	PricingMode         string   `json:"pricing_mode"` // quoted billing mode
	// PriceStatus identifies whether a quote is priced; zero-valued token fields
	// may be omitted from JSON even when the quote is free.
	PriceStatus string `json:"price_status"` // priced or unpriced status
	// InputPricePerToken, ImageInputPricePerToken, OutputPricePerToken,
	// CacheWritePricePerToken, CacheWrite1hPricePerToken,
	// CacheReadPricePerToken, and ImageOutputPricePerToken expose Standard
	// amounts. FastInputPricePerToken, FastImageInputPricePerToken,
	// FastOutputPricePerToken, FastCacheWritePricePerToken,
	// FastCacheWrite1hPricePerToken, FastCacheReadPricePerToken, and
	// FastImageOutputPricePerToken expose Fast amounts. Both sets use the
	// resolved quote's rate multiplier, and JSON omits zero numeric fields.
	InputPricePerToken            float64                           `json:"input_price_per_token,omitempty"`               // USD per text-input token
	ImageInputPricePerToken       float64                           `json:"image_input_price_per_token,omitempty"`         // USD per image-input token
	OutputPricePerToken           float64                           `json:"output_price_per_token,omitempty"`              // USD per text-output token
	CacheWritePricePerToken       float64                           `json:"cache_write_price_per_token,omitempty"`         // USD per short-TTL cache-write token
	CacheWrite1hPricePerToken     float64                           `json:"cache_write_1h_price_per_token,omitempty"`      // USD per hourly cache-write token
	CacheReadPricePerToken        float64                           `json:"cache_read_price_per_token,omitempty"`          // USD per cache-read token
	ImageOutputPricePerToken      float64                           `json:"image_output_price_per_token,omitempty"`        // USD per image-output token
	FastInputPricePerToken        float64                           `json:"fast_input_price_per_token,omitempty"`          // Fast USD per text-input token
	FastImageInputPricePerToken   float64                           `json:"fast_image_input_price_per_token,omitempty"`    // Fast USD per image-input token
	FastOutputPricePerToken       float64                           `json:"fast_output_price_per_token,omitempty"`         // Fast USD per text-output token
	FastCacheWritePricePerToken   float64                           `json:"fast_cache_write_price_per_token,omitempty"`    // Fast USD per short-TTL cache-write token
	FastCacheWrite1hPricePerToken float64                           `json:"fast_cache_write_1h_price_per_token,omitempty"` // Fast USD per hourly cache-write token
	FastCacheReadPricePerToken    float64                           `json:"fast_cache_read_price_per_token,omitempty"`     // Fast USD per cache-read token
	FastImageOutputPricePerToken  float64                           `json:"fast_image_output_price_per_token,omitempty"`   // Fast USD per image-output token
	ContextIntervals              []ModelMarketplacePricingInterval `json:"context_intervals,omitempty"`                   // quoted context-token price ranges
	// MaxReasoningEffortMultiplier is an optional positive non-unit factor
	// carried separately from the quoted token amounts for the marketplace's
	// effort selector.
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier,omitempty"` // optional max-effort token factor
	// TimePricing carries configured card periods separately from token amounts
	// so the marketplace can show the applicable time-dependent multiplier.
	TimePricing *ModelMarketplaceTimePricing `json:"time_pricing,omitempty"` // optional daily token schedule
	// ImagePrice1K, ImagePrice2K, and ImagePrice4K retain the quote's size
	// availability. A nil pointer omits an unavailable size; a nonnil zero
	// encodes a known free image.
	ImagePrice1K *float64 `json:"image_price_1k,omitempty"` // USD per 1K output image
	ImagePrice2K *float64 `json:"image_price_2k,omitempty"` // USD per 2K output image
	ImagePrice4K *float64 `json:"image_price_4k,omitempty"` // USD per 4K output image
}

// ModelMarketplacePricingInterval exposes quoted token amounts within one
// context-length range.
type ModelMarketplacePricingInterval struct {
	// MinTokens and MaxTokens describe the quoted (MinTokens, MaxTokens] range.
	// Token amounts apply to that context interval and already include the group
	// quote's rate multiplier.
	MinTokens int `json:"min_tokens"` // excluded lower context-token endpoint
	// MaxTokens is nil for an unbounded range; conversion retains an existing
	// endpoint pointer, which remains stable while encoding the response.
	MaxTokens                     *int    `json:"max_tokens,omitempty"`                          // included upper context-token endpoint
	InputPricePerToken            float64 `json:"input_price_per_token,omitempty"`               // USD per text-input token
	ImageInputPricePerToken       float64 `json:"image_input_price_per_token,omitempty"`         // USD per image-input token
	OutputPricePerToken           float64 `json:"output_price_per_token,omitempty"`              // USD per text-output token
	CacheWritePricePerToken       float64 `json:"cache_write_price_per_token,omitempty"`         // USD per short-TTL cache-write token
	CacheWrite1hPricePerToken     float64 `json:"cache_write_1h_price_per_token,omitempty"`      // USD per hourly cache-write token
	CacheReadPricePerToken        float64 `json:"cache_read_price_per_token,omitempty"`          // USD per cache-read token
	ImageOutputPricePerToken      float64 `json:"image_output_price_per_token,omitempty"`        // USD per image-output token
	FastInputPricePerToken        float64 `json:"fast_input_price_per_token,omitempty"`          // Fast USD per text-input token
	FastImageInputPricePerToken   float64 `json:"fast_image_input_price_per_token,omitempty"`    // Fast USD per image-input token
	FastOutputPricePerToken       float64 `json:"fast_output_price_per_token,omitempty"`         // Fast USD per text-output token
	FastCacheWritePricePerToken   float64 `json:"fast_cache_write_price_per_token,omitempty"`    // Fast USD per short-TTL cache-write token
	FastCacheWrite1hPricePerToken float64 `json:"fast_cache_write_1h_price_per_token,omitempty"` // Fast USD per hourly cache-write token
	FastCacheReadPricePerToken    float64 `json:"fast_cache_read_price_per_token,omitempty"`     // Fast USD per cache-read token
	FastImageOutputPricePerToken  float64 `json:"fast_image_output_price_per_token,omitempty"`   // Fast USD per image-output token
}

// ModelMarketplaceTimePricing describes the daily token schedule attached to
// a quote.
type ModelMarketplaceTimePricing struct {
	// Timezone, WeekdaysOnly, and Periods describe one daily schedule. The
	// marketplace uses the named local clock and weekday restriction to select
	// displayed factors; uncovered times use factor one.
	Timezone     string                              `json:"timezone"`      // daily schedule timezone name
	WeekdaysOnly bool                                `json:"weekdays_only"` // Monday-through-Friday schedule restriction
	Periods      []ModelMarketplaceTimePricingPeriod `json:"periods"`       // daily token-price multiplier periods
}

// ModelMarketplaceTimePricingPeriod describes one daily clock range and its
// token-price factor.
type ModelMarketplaceTimePricingPeriod struct {
	// StartTime and EndTime define a daily [start, end) clock range at minute or
	// second precision. EndTime spelled 00:00 or 00:00:00 closes the current
	// day.
	StartTime  string  `json:"start_time"` // inclusive local clock start
	EndTime    string  `json:"end_time"`   // exclusive local clock end
	Multiplier float64 `json:"multiplier"` // period token-price scaling factor
}

// ModelMarketplaceModel combines a requestable ID with display attributes and
// a public quote.
type ModelMarketplaceModel struct {
	// ProtocolAttributes exposes display ranges keyed by requestable client protocol.
	// Missing data leaves protocol labels available while their attributes remain unknown.
	ProtocolAttributes map[protocol.ProtocolID]modelcatalog.Presentation `json:"protocol_attributes,omitempty"`
	// Attributes contains route-aware display information when the attribute
	// query succeeds; nil leaves capability details unknown while the model and
	// quote remain available.
	Attributes       *routing.EffectiveModelAttributes `json:"attributes,omitempty"`        // optional effective display attributes
	ID               string                            `json:"id"`                          // client request model ID
	DisplayName      string                            `json:"display_name"`                // resolved model display name
	Pricing          ModelMarketplacePricing           `json:"pricing"`                     // public group model quote
	InputModalities  []string                          `json:"input_modalities,omitempty"`  // legacy input modality labels
	OutputModalities []string                          `json:"output_modalities,omitempty"` // legacy output modality labels
	// Protocols and NativeProtocols come from the same requestable model
	// resolution. For each client entry, NativeProtocols requires every provider
	// contributing that entry to route it directly; Protocols also contains
	// entries served through conversion.
	Protocols       []protocol.ProtocolID `json:"protocols,omitempty"`        // requestable client protocol choices
	NativeProtocols []protocol.ProtocolID `json:"native_protocols,omitempty"` // protocols native across routes
}

// ModelMarketplaceCapacity holds current aggregate provider capacity
// observations for an authorized group.
type ModelMarketplaceCapacity struct {
	ConcurrencyUsed int `json:"concurrency_used"` // occupied provider concurrency slots
	ConcurrencyMax  int `json:"concurrency_max"`  // aggregate provider concurrency capacity
	SessionsUsed    int `json:"sessions_used"`    // occupied provider session slots
	SessionsMax     int `json:"sessions_max"`     // aggregate provider session capacity
	RPMUsed         int `json:"rpm_used"`         // observed requests per minute
	RPMMax          int `json:"rpm_max"`          // aggregate provider RPM capacity
}

// ModelMarketplaceAvailabilityDay contains probe counts for one displayed
// time bucket.
type ModelMarketplaceAvailabilityDay struct {
	Date string `json:"date"` // local time-bucket label
	// SuccessCount, TotalCount, and AvailabilityRate summarize final probe
	// rounds within this bucket. AvailabilityRate is success divided by total,
	// with nil indicating no recorded rounds.
	SuccessCount     int64    `json:"success_count"`               // successful probe round count
	TotalCount       int64    `json:"total_count"`                 // recorded probe round count
	AvailabilityRate *float64 `json:"availability_rate,omitempty"` // successful round proportion
}

// ModelMarketplaceAvailability combines windowed probe totals, latest status,
// and time buckets.
type ModelMarketplaceAvailability struct {
	WindowDays    int `json:"window_days"`    // configured observation window days
	BucketMinutes int `json:"bucket_minutes"` // configured time-bucket size minutes
	// SuccessCount, TotalCount, and AvailabilityRate summarize the observation
	// window. AvailabilityRate is success divided by total, and nil indicates
	// that the window contains no recorded rounds.
	SuccessCount     int64    `json:"success_count"`               // successful window round count
	TotalCount       int64    `json:"total_count"`                 // recorded window round count
	AvailabilityRate *float64 `json:"availability_rate,omitempty"` // window successful round proportion
	// LastStatus and LastCheckedAt describe the latest stored probe round even
	// when it predates the observation window. LastCheckedAt is that round's
	// finish time; absent history leaves these values unset.
	LastStatus    string                            `json:"last_status,omitempty"`     // latest probe round status
	LastCheckedAt *time.Time                        `json:"last_checked_at,omitempty"` // latest round completion time
	Days          []ModelMarketplaceAvailabilityDay `json:"days"`                      // ordered local observation buckets
}

// ModelMarketplaceGroup contains localized product text, models, prices, and
// optional observations.
type ModelMarketplaceGroup struct {
	SearchTerms []string          `json:"search_terms,omitempty"`  // localized group search texts
	Resolution  locale.Resolution `json:"localization_resolution"` // resolved group display language
	ID          int64             `json:"id"`                      // stored visible group identifier
	IsExclusive bool              `json:"is_exclusive"`            // requires explicit user authorization
	Name        string            `json:"name"`                    // resolved group display name
	Description string            `json:"description"`             // resolved group display description

	DisplayBrand   string  `json:"display_brand"`   // resolved group display brand
	SortOrder      int     `json:"sort_order"`      // manual group display order
	RateMultiplier float64 `json:"rate_multiplier"` // group user quote multiplier
	// HideMaxDiscount and HideRateMultiplier independently suppress the group's
	// marketplace header tags. False keeps each tag eligible for display while
	// pricing and currency metadata remain available.
	HideMaxDiscount    bool `json:"hide_max_discount"`    // hide discount header tag
	HideRateMultiplier bool `json:"hide_rate_multiplier"` // hide multiplier header tag
	// OfficialPriceRatio and OfficialPriceRMBEquivalent require valid unit-price
	// and USD-exchange settings. They carry rate-scaled conversion values for
	// price display; unavailable, nonpositive, or nonfinite conversion results
	// remain absent.
	OfficialPriceRatio         *float64 `json:"official_price_ratio,omitempty"`          // price-to-official conversion ratio
	OfficialPriceRMBEquivalent *float64 `json:"official_price_rmb_equivalent,omitempty"` // rate-scaled RMB unit equivalent
	// Capacity is present when the marketplace includes capacity and obtains a
	// summary; disabled or unavailable capacity queries leave this member
	// absent.
	Capacity *ModelMarketplaceCapacity `json:"capacity,omitempty"` // optional current capacity summary
	// Availability contains observed probe history when supplied by routing; a
	// missing summary remains absent and does not invent an availability rate.
	Availability *ModelMarketplaceAvailability `json:"availability,omitempty"` // optional historical probe summary
	ModelCount   int                           `json:"model_count"`            // resolved requestable model count
	Models       []ModelMarketplaceModel       `json:"models"`                 // requestable visible model entries
}

// ModelMarketplaceGroupsFromRouting returns authorized group DTOs in the supplied
// order, including an empty nonnil slice for empty input. It allocates group
// and model records, while attribute pointers, model modality/protocol
// slices, protocol attribute maps, search texts, and optional scalar pointers
// can retain routing
// storage; treat them as read-only during encoding.
func ModelMarketplaceGroupsFromRouting(groups []routing.ModelMarketplaceGroup) []ModelMarketplaceGroup {
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for _, group := range groups {
		models := make([]ModelMarketplaceModel, 0, len(group.Models))
		for _, model := range group.Models {
			models = append(models, ModelMarketplaceModel{
				ID:                 model.ID,
				DisplayName:        model.DisplayName,
				Pricing:            modelMarketplacePricingFromRouting(model.Pricing),
				InputModalities:    model.InputModalities,
				Attributes:         model.Attributes,
				ProtocolAttributes: model.ProtocolAttributes,
				OutputModalities:   model.OutputModalities,
				Protocols:          model.Protocols,
				NativeProtocols:    model.NativeProtocols,
			})
		}

		out = append(out, ModelMarketplaceGroup{
			SearchTerms: group.SearchTerms, Resolution: group.Resolution,
			ID:          group.ID,
			IsExclusive: group.IsExclusive,
			Name:        group.Name,
			Description: group.Description,

			DisplayBrand:               group.DisplayBrand,
			SortOrder:                  group.SortOrder,
			RateMultiplier:             group.RateMultiplier,
			HideMaxDiscount:            group.HideMaxDiscount,
			HideRateMultiplier:         group.HideRateMultiplier,
			OfficialPriceRatio:         group.OfficialPriceRatio,
			OfficialPriceRMBEquivalent: group.OfficialPriceRMBEquivalent,
			Capacity:                   modelMarketplaceCapacityFromRouting(group.Capacity),
			Availability:               modelMarketplaceAvailabilityFromRouting(group.Availability),
			ModelCount:                 group.ModelCount,
			Models:                     models,
		})
	}

	return out
}

// modelMarketplaceAvailabilityFromRouting maps a probe summary or returns nil
// when it is unavailable. Bucket records are allocated for the DTO, while
// availability-rate and latest-check pointers retain the summary's values and
// storage.
func modelMarketplaceAvailabilityFromRouting(availability *routing.GroupAvailabilitySummary) *ModelMarketplaceAvailability {
	if availability == nil {
		return nil
	}
	days := make([]ModelMarketplaceAvailabilityDay, 0, len(availability.Days))
	for _, day := range availability.Days {
		days = append(days, ModelMarketplaceAvailabilityDay{
			Date:             day.Date,
			SuccessCount:     day.SuccessCount,
			TotalCount:       day.TotalCount,
			AvailabilityRate: day.AvailabilityRate,
		})
	}
	return &ModelMarketplaceAvailability{
		WindowDays:       availability.WindowDays,
		BucketMinutes:    availability.BucketMinutes,
		SuccessCount:     availability.SuccessCount,
		TotalCount:       availability.TotalCount,
		AvailabilityRate: availability.AvailabilityRate,
		LastStatus:       availability.LastStatus,
		LastCheckedAt:    availability.LastCheckedAt,
		Days:             days,
	}
}

// modelMarketplaceCapacityFromRouting copies current capacity observations
// into a new DTO. A nil summary remains nil so unavailable capacity can be
// omitted, while a present summary retains zero observations.
func modelMarketplaceCapacityFromRouting(capacity *routing.GroupCapacitySummary) *ModelMarketplaceCapacity {
	if capacity == nil {
		return nil
	}
	return &ModelMarketplaceCapacity{
		ConcurrencyUsed: capacity.ConcurrencyUsed,
		ConcurrencyMax:  capacity.ConcurrencyMax,
		SessionsUsed:    capacity.SessionsUsed,
		SessionsMax:     capacity.SessionsMax,
		RPMUsed:         capacity.RPMUsed,
		RPMMax:          capacity.RPMMax,
	}
}

// modelMarketplacePricingFromRouting maps an already resolved display quote
// without recalculating its amounts. It allocates interval records and daily
// schedule records, retains video maps and optional scalar pointers as read-only
// quote data, and derives optional image prices from size-presence metadata so
// declared free sizes remain encoded. A free video default retains its nonnil
// pointer, while an unavailable default remains absent. Video dimension keys
// pass through using the canonical labels supplied by the billing quote.
func modelMarketplacePricingFromRouting(pricing pricing.ModelDisplayPricing) ModelMarketplacePricing {
	intervals := make([]ModelMarketplacePricingInterval, 0, len(pricing.ContextIntervals))
	for _, interval := range pricing.ContextIntervals {
		intervals = append(intervals, ModelMarketplacePricingInterval{
			MinTokens:                     interval.MinTokens,
			MaxTokens:                     interval.MaxTokens,
			InputPricePerToken:            interval.InputPricePerToken,
			ImageInputPricePerToken:       interval.ImageInputPricePerToken,
			OutputPricePerToken:           interval.OutputPricePerToken,
			CacheWritePricePerToken:       interval.CacheWritePricePerToken,
			CacheWrite1hPricePerToken:     interval.CacheWrite1hPricePerToken,
			CacheReadPricePerToken:        interval.CacheReadPricePerToken,
			ImageOutputPricePerToken:      interval.ImageOutputPricePerToken,
			FastInputPricePerToken:        interval.FastInputPricePerToken,
			FastImageInputPricePerToken:   interval.FastImageInputPricePerToken,
			FastOutputPricePerToken:       interval.FastOutputPricePerToken,
			FastCacheWritePricePerToken:   interval.FastCacheWritePricePerToken,
			FastCacheWrite1hPricePerToken: interval.FastCacheWrite1hPricePerToken,
			FastCacheReadPricePerToken:    interval.FastCacheReadPricePerToken,
			FastImageOutputPricePerToken:  interval.FastImageOutputPricePerToken,
		})
	}

	return ModelMarketplacePricing{
		VideoPrices:                   pricing.VideoPrices,
		VideoPricePerSecond:           pricing.VideoPricePerSecond,
		PricingMode:                   pricing.PricingMode,
		PriceStatus:                   pricing.PriceStatus,
		InputPricePerToken:            pricing.InputPricePerToken,
		ImageInputPricePerToken:       pricing.ImageInputPricePerToken,
		OutputPricePerToken:           pricing.OutputPricePerToken,
		CacheWritePricePerToken:       pricing.CacheWritePricePerToken,
		CacheWrite1hPricePerToken:     pricing.CacheWrite1hPricePerToken,
		CacheReadPricePerToken:        pricing.CacheReadPricePerToken,
		ImageOutputPricePerToken:      pricing.ImageOutputPricePerToken,
		FastInputPricePerToken:        pricing.FastInputPricePerToken,
		FastImageInputPricePerToken:   pricing.FastImageInputPricePerToken,
		FastOutputPricePerToken:       pricing.FastOutputPricePerToken,
		FastCacheWritePricePerToken:   pricing.FastCacheWritePricePerToken,
		FastCacheWrite1hPricePerToken: pricing.FastCacheWrite1hPricePerToken,
		FastCacheReadPricePerToken:    pricing.FastCacheReadPricePerToken,
		FastImageOutputPricePerToken:  pricing.FastImageOutputPricePerToken,
		ContextIntervals:              intervals,
		MaxReasoningEffortMultiplier:  pricing.MaxReasoningEffortMultiplier,
		TimePricing:                   modelMarketplaceTimePricingFromRouting(pricing.TimePricing),
		ImagePrice1K:                  imagePriceValue(pricing, "1K", pricing.ImagePrice1K),
		ImagePrice2K:                  imagePriceValue(pricing, "2K", pricing.ImagePrice2K),
		ImagePrice4K:                  imagePriceValue(pricing, "4K", pricing.ImagePrice4K),
	}
}

// modelMarketplaceTimePricingFromRouting copies the daily schedule into new
// DTO and period storage. A nil configuration remains nil; clock spellings,
// weekday restriction, timezone, and supplied period order pass through for
// display.
func modelMarketplaceTimePricingFromRouting(config *pricing.TimePricingConfig) *ModelMarketplaceTimePricing {
	if config == nil {
		return nil
	}
	periods := make([]ModelMarketplaceTimePricingPeriod, 0, len(config.Periods))
	for _, period := range config.Periods {
		periods = append(periods, ModelMarketplaceTimePricingPeriod{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &ModelMarketplaceTimePricing{
		Timezone:     config.Timezone,
		WeekdaysOnly: config.WeekdaysOnly,
		Periods:      periods,
	}
}

// imagePriceValue returns a quoted image amount or nil when that size is
// unavailable. A nonempty ImagePriceSizes list is authoritative, so a listed
// size returns a pointer even when its amount is zero; an unlisted size stays
// absent regardless of its numeric amount. Legacy records with no size list
// expose nonzero amounts and omit zero amounts. The returned pointer stores
// the passed amount, preserving the JSON distinction between a declared free
// size and an unavailable size.
func imagePriceValue(price pricing.ModelDisplayPricing, size string, value float64) *float64 {
	// Quoted size metadata distinguishes free images from missing quotes; an
	// explicit list takes precedence over legacy numeric amount inference.
	if len(price.ImagePriceSizes) > 0 {
		if !slices.Contains(price.ImagePriceSizes, size) {
			return nil
		}
	} else if value == 0 {
		return nil
	}
	return &value
}
