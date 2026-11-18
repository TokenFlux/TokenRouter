package httpapi

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// pricing_handler.go translates administrative pricing requests and responses
// for shared configuration operations.
// Shared price cards keep optional amount presence through request and
// response conversion. Nil amounts stay unspecified, supplied zero values
// remain present, and the current Fast multiplier takes precedence over its
// legacy alias by pointer presence. The billing mode determines how price
// consumers interpret token buckets, request/media amounts, and context or
// labeled intervals.
//
// Administrator writes use strict JSON binding before price-service
// validation and persistence. Collection pointers distinguish retaining
// stored cards or associations from replacing them with an empty list, while
// statistics rules retain request order for their ordered selection. Default
// form autofill reads catalog amounts independently of shared cards and
// reports an unsuccessful lookup through found=false.

// PricingHandler serves shared price configuration administration and default
// catalog queries.
type PricingHandler struct {
	pricingConfigs *routing.PricingConfigService // shared price configuration service
	catalog        *routing.PricingCatalog       // published default price catalog
}

// createPricingConfigRequest carries new shared cards, billing settings, and
// group associations.
type createPricingConfigRequest struct {
	billingSettingsRequest                       // supplied shared billing policy
	Name                   string                `json:"name" binding:"required,max=100"` // shared configuration business name
	Description            string                `json:"description"`                     // configuration display description
	GroupIDs               []int64               `json:"group_ids"`                       // associated group identifier list
	ModelPricing           []modelPricingRequest `json:"model_pricing"`                   // user-facing model price cards

	// BillingModelSource chooses the model identity used for charging; an empty
	// creation input uses the service's group_mapped default.
	BillingModelSource string `json:"billing_model_source" binding:"omitempty,oneof=requested upstream group_mapped"` // requested upstream or group_mapped

	ProviderStatsPricingRules []providerStatsPricingRuleRequest `json:"provider_stats_pricing_rules"` // ordered provider-cost price rules
}

// updatePricingConfigRequest retains omission information for stored prices
// and group associations.
type updatePricingConfigRequest struct {
	billingSettingsRequest // supplied shared billing policy
	// Name changes the stored name when nonempty; an empty or omitted string
	// leaves it unchanged during price-service update.
	Name string `json:"name" binding:"omitempty,max=100"` // shared configuration business name
	// Description retains the stored text when nil; a supplied empty string
	// clears the description during price-service update.
	Description *string `json:"description"` // configuration display description
	// Status retains the stored state when empty; a supplied active or disabled
	// value selects the configuration's activation state.
	Status string `json:"status" binding:"omitempty,oneof=active disabled"` // active or disabled state patch
	// GroupIDs and ModelPricing retain stored collections when nil. Supplied
	// lists replace those collections, including empty arrays that remove group
	// associations or user-facing cards.
	GroupIDs     *[]int64               `json:"group_ids"`     // associated group identifier list
	ModelPricing *[]modelPricingRequest `json:"model_pricing"` // user-facing model price cards

	// BillingModelSource retains the current model-source choice for an empty
	// string; a supplied accepted value replaces that stored charging identity
	// source.
	BillingModelSource string `json:"billing_model_source" binding:"omitempty,oneof=requested upstream group_mapped"` // requested upstream or group_mapped

	// ProviderStatsPricingRules retains stored statistics rules when nil; a
	// supplied empty array removes them and other lists replace them in request
	// order.
	ProviderStatsPricingRules *[]providerStatsPricingRuleRequest `json:"provider_stats_pricing_rules"` // ordered provider-cost price rules
}

// modelPricingRequest carries optional model-card amounts, billing modifiers,
// and interval inputs.
type modelPricingRequest struct {
	// ReferenceModel selects a concrete target in the same profile. Empty values
	// retain custom prices; populated references reject supplied price overrides.
	ReferenceModel string `json:"reference_model,omitempty"`

	// Models supplies complete IDs or trailing-star patterns for card matching;
	// configuration validation checks the submitted patterns and overlapping
	// model rules.
	Models []string `json:"models" binding:"required,min=1,max=100"` // complete model identity patterns
	// BillingMode chooses the card's charging units; an empty input or stored
	// value is converted to token mode for service and HTTP use.
	BillingMode string `json:"billing_mode" binding:"omitempty,oneof=token per_request image video"` // token request image or video
	// PriceMultiplier scales user amounts after card overrides, with nil leaving
	// those amounts unchanged. Service validation requires an explicit price for
	// a supplied factor; provider statistics uses its configured amounts
	// directly.
	PriceMultiplier *float64 `json:"price_multiplier" binding:"omitempty,min=0"` // configured amount scaling factor
	// FastModeMultiplier and FastMultiplier retain legacy and current inputs.
	// Conversion prefers a present FastMultiplier, including zero. Service
	// validation requires explicit prices for the legacy factor and rejects
	// nonpositive or nonfinite effective Fast factors. Provider-statistics
	// writes reject either factor.
	FastModeMultiplier *float64 `json:"fast_mode_multiplier" binding:"omitempty,min=0"` // legacy Fast scaling factor
	FastMultiplier     *float64 `json:"fast_multiplier" binding:"omitempty,gt=0"`       // preferred Fast scaling factor
	// FlexMultiplier and MaxReasoningEffortMultiplier override user token-card
	// factors; nil retains inherited metadata. Supplied factors must be finite
	// and positive, and the Max factor applies when the final forwarded effort
	// is max. Provider-statistics writes reject these factors.
	FlexMultiplier               *float64 `json:"flex_multiplier" binding:"omitempty,gt=0"`                 // configured Flex scaling factor
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier" binding:"omitempty,gt=0"` // final max-effort scaling factor
	// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, and
	// CacheReadPrice retain optional amount presence through conversion. Nil
	// leaves a bucket unspecified; a supplied zero remains available for its
	// billing consumer to interpret.
	InputPrice      *float64 `json:"input_price" binding:"omitempty,min=0"`       // USD per text-input token
	OutputPrice     *float64 `json:"output_price" binding:"omitempty,min=0"`      // USD per text-output token
	CacheWritePrice *float64 `json:"cache_write_price" binding:"omitempty,min=0"` // USD per short-TTL cache-write token
	// CacheWrite1hPrice can independently supply an hourly amount, including
	// zero; when absent, a CacheWritePrice override also covers that hourly
	// bucket.
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price" binding:"omitempty,min=0"`  // USD per hourly cache-write token
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price" binding:"omitempty,min=0"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price" binding:"omitempty,min=0"` // USD per twelve-hour write token
	CacheReadPrice     *float64 `json:"cache_read_price" binding:"omitempty,min=0"`      // USD per cache-read token
	// ImageInputPrice is the image-input subset rate; user token-card
	// application uses the selected text-input amount when this field is omitted
	// or zero.
	ImageInputPrice *float64 `json:"image_input_price" binding:"omitempty,min=0"` // USD per image-input token
	// ImageOutputPrice is a separately recorded image-output rate. User token
	// pricing treats omitted or zero overrides as free image-output tokens.
	ImageOutputPrice *float64 `json:"image_output_price" binding:"omitempty,min=0"` // USD per image-output token
	// PerRequestPrice supplies the default charge for a request, image, or video
	// unit. Nil leaves that default unspecified; a supplied zero remains an
	// explicit free unit amount.
	PerRequestPrice *float64 `json:"per_request_price" binding:"omitempty,min=0"` // USD per request or media unit
	// Intervals supplies context ranges or labeled unit tiers according to
	// BillingMode; user token resolution can inherit missing buckets while
	// provider statistics uses its independent card rules.
	Intervals []pricingIntervalRequest `json:"intervals"` // context or labeled unit tiers
	// TimePricing supplies a user token-card schedule. Nil leaves it
	// unspecified, and service writes remove schedules with no periods;
	// populated schedules are rejected for provider-statistics cards.
	TimePricing *timePricingRequest `json:"time_pricing"` // configured daily token-price schedule
}

// timePricingRequest carries a daily token-price schedule for administrator
// card input.
type timePricingRequest struct {
	// Timezone, WeekdaysOnly, and Periods describe one daily token schedule.
	// Billing callers load the timezone and evaluate local dates and clocks;
	// times outside applicable periods use factor one.
	Timezone     string                     `json:"timezone"`      // daily schedule timezone name
	WeekdaysOnly bool                       `json:"weekdays_only"` // Monday-through-Friday schedule restriction
	Periods      []timePricingPeriodRequest `json:"periods"`       // daily token-price multiplier periods
}

// timePricingPeriodRequest carries one local-clock range and its token-price
// multiplier.
type timePricingPeriodRequest struct {
	// StartTime and EndTime define a daily [start, end) range in HH:mm or
	// HH:mm:ss form. An end of 00:00 or 00:00:00 represents the current day's
	// end.
	StartTime  string  `json:"start_time"` // inclusive local clock start
	EndTime    string  `json:"end_time"`   // exclusive local clock end
	Multiplier float64 `json:"multiplier"` // daily token-price scaling factor
}

// pricingIntervalRequest carries context endpoints, optional amounts, and
// labeled unit pricing.
type pricingIntervalRequest struct {
	// MinTokens and MaxTokens describe a (MinTokens, MaxTokens] context range. A
	// nil upper endpoint leaves that range unbounded; billing mode also permits
	// labeled request or media unit tiers.
	MinTokens int    `json:"min_tokens"` // excluded lower context-token endpoint
	MaxTokens *int   `json:"max_tokens"` // included upper context-token endpoint
	TierLabel string `json:"tier_label"` // request or media tier label
	// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, and
	// CacheReadPrice retain optional interval amounts. User token intervals
	// inherit missing buckets; provider statistics treats missing buckets under
	// its independent price calculation.
	InputPrice      *float64 `json:"input_price"`       // USD per interval input token
	OutputPrice     *float64 `json:"output_price"`      // USD per interval output token
	CacheWritePrice *float64 `json:"cache_write_price"` // USD per short-TTL interval cache-write token
	// CacheWrite1hPrice supplies a separate hourly interval amount, including
	// zero; absent hourly input lets an interval CacheWritePrice override cover
	// that bucket.
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price"`  // USD per hourly interval cache-write token
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price"` // USD per twelve-hour write token
	CacheReadPrice     *float64 `json:"cache_read_price"`      // USD per interval cache-read token
	// InputMultiplier, OutputMultiplier, CacheWriteMultiplier, and
	// CacheReadMultiplier scale inherited user token amounts when the
	// corresponding explicit amount is absent. Provider statistics calculations
	// ignore these interval factors.
	InputMultiplier      *float64 `json:"input_multiplier" binding:"omitempty,gt=0"`       // interval input-price scaling factor
	OutputMultiplier     *float64 `json:"output_multiplier" binding:"omitempty,gt=0"`      // interval output-price scaling factor
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier" binding:"omitempty,gt=0"` // interval cache-write scaling factor
	CacheReadMultiplier  *float64 `json:"cache_read_multiplier" binding:"omitempty,gt=0"`  // interval cache-read scaling factor
	// PerRequestPrice supplies this unit tier, including free pricing at zero.
	// FilterValidRequestIntervals excludes rows with a nil amount from request
	// or media unit matching.
	PerRequestPrice *float64 `json:"per_request_price"` // USD per matched unit
	SortOrder       int      `json:"sort_order"`        // stored interval ordering key
}

// providerStatsPricingRuleRequest carries ordered provider-cost selectors and
// their independent model cards.
type providerStatsPricingRuleRequest struct {
	Name string `json:"name"` // administrator statistics-rule display name
	// GroupIDs and ProviderIDs are alternative selectors for this statistics
	// rule. Either matching list admits the rule; administrator writes require
	// at least one selector and a nonempty Pricing list.
	GroupIDs    []int64               `json:"group_ids"`    // matching group identifier list
	ProviderIDs []int64               `json:"provider_ids"` // matching provider identifier list
	Pricing     []modelPricingRequest `json:"pricing"`      // independent provider-cost model cards
}

// pricingConfigResponse exposes stored shared cards, billing settings, and
// group associations.
type pricingConfigResponse struct {
	pricing.BillingSettings        // copied shared billing policy
	ID                      int64  `json:"id"`          // stored configuration row identifier
	Name                    string `json:"name"`        // shared configuration business name
	Description             string `json:"description"` // configuration display description
	Status                  string `json:"status"`      // active or disabled state
	// BillingModelSource uses group_mapped when the stored value is empty;
	// callers receive the configured source otherwise for price administration.
	BillingModelSource string `json:"billing_model_source"` // effective charging model source

	GroupIDs     []int64                `json:"group_ids"`     // associated group identifier list
	ModelPricing []modelPricingResponse `json:"model_pricing"` // ordered user-facing model cards

	ProviderStatsPricingRules []providerStatsPricingRuleResponse `json:"provider_stats_pricing_rules"` // ordered provider-cost price rules
	// CreatedAt and UpdatedAt use second-resolution strings with a literal Z
	// suffix. Conversion formats supplied times in their existing location, so
	// stored time values determine the displayed clock.
	CreatedAt string `json:"created_at"` // formatted creation clock text
	UpdatedAt string `json:"updated_at"` // formatted update clock text
}

// modelPricingResponse exposes one card's optional amounts, modifiers, and
// ordered intervals.
type modelPricingResponse struct {
	// ReferenceModel selects a concrete target in the same profile. Empty values
	// retain custom prices; populated references reject supplied price overrides.
	ReferenceModel string `json:"reference_model,omitempty"`

	ID int64 `json:"id"` // stored model-card row identifier
	// Models supplies complete IDs or trailing-star patterns for card matching;
	// configuration validation checks the submitted patterns and overlapping
	// model rules.
	Models []string `json:"models"` // complete model identity patterns
	// BillingMode chooses the card's charging units; an empty input or stored
	// value is converted to token mode for service and HTTP use.
	BillingMode string `json:"billing_mode"` // token request image or video
	// PriceMultiplier reports the stored user amount factor after overrides. Nil
	// leaves user prices unscaled by this setting; provider statistics
	// calculates its configured amounts directly.
	PriceMultiplier *float64 `json:"price_multiplier"` // configured amount scaling factor
	// FastModeMultiplier retains the stored legacy factor, and FastMultiplier
	// reports the selected current factor. Conversion prefers a present current
	// pointer, including zero; an absent current pointer exposes the legacy
	// value through both fields.
	FastModeMultiplier *float64 `json:"fast_mode_multiplier"` // legacy Fast scaling factor
	FastMultiplier     *float64 `json:"fast_multiplier"`      // preferred Fast scaling factor
	// FlexMultiplier and MaxReasoningEffortMultiplier report stored user token
	// factors. Nil retains inherited metadata; the supplied Max factor scales
	// token costs when the final forwarded effort is max. Provider statistics
	// writes reject these factors.
	FlexMultiplier               *float64 `json:"flex_multiplier"`                 // configured Flex scaling factor
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier"` // final max-effort scaling factor
	// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, and
	// CacheReadPrice retain optional amount presence through conversion. Nil
	// leaves a bucket unspecified; a supplied zero remains available for its
	// billing consumer to interpret.
	InputPrice      *float64 `json:"input_price"`       // USD per text-input token
	OutputPrice     *float64 `json:"output_price"`      // USD per text-output token
	CacheWritePrice *float64 `json:"cache_write_price"` // USD per short-TTL cache-write token
	// CacheWrite1hPrice can independently supply an hourly amount, including
	// zero; when absent, a CacheWritePrice override also covers that hourly
	// bucket.
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price"`  // USD per hourly cache-write token
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price"` // USD per twelve-hour write token
	CacheReadPrice     *float64 `json:"cache_read_price"`      // USD per cache-read token
	// ImageInputPrice is the image-input subset rate; user token-card
	// application uses the selected text-input amount when this field is omitted
	// or zero.
	ImageInputPrice *float64 `json:"image_input_price"` // USD per image-input token
	// ImageOutputPrice is a separately recorded image-output rate. User token
	// pricing treats omitted or zero overrides as free image-output tokens.
	ImageOutputPrice *float64 `json:"image_output_price"` // USD per image-output token
	// PerRequestPrice exposes the optional stored unit default. Nil means the
	// card provides no default amount; zero represents a configured free unit.
	PerRequestPrice *float64 `json:"per_request_price"` // USD per request or media unit
	// Intervals supplies context ranges or labeled unit tiers according to
	// BillingMode; user token resolution can inherit missing buckets while
	// provider statistics uses its independent card rules.
	Intervals []pricingIntervalResponse `json:"intervals"` // context or labeled unit tiers
	// TimePricing exposes a stored user token-card schedule. Nil carries no
	// card-level rule; service writes normalize empty period lists to nil and
	// reject populated schedules in provider-statistics cards.
	TimePricing *timePricingResponse `json:"time_pricing"` // configured daily token-price schedule
}

// timePricingResponse exposes a card's daily schedule with copied period
// records.
type timePricingResponse struct {
	// Timezone, WeekdaysOnly, and Periods describe one daily token schedule.
	// Billing callers load the timezone and evaluate local dates and clocks;
	// times outside applicable periods use factor one.
	Timezone     string                      `json:"timezone"`      // daily schedule timezone name
	WeekdaysOnly bool                        `json:"weekdays_only"` // Monday-through-Friday schedule restriction
	Periods      []timePricingPeriodResponse `json:"periods"`       // daily token-price multiplier periods
}

// timePricingPeriodResponse exposes one stored daily clock range and
// token-price multiplier.
type timePricingPeriodResponse struct {
	// StartTime and EndTime define a daily [start, end) range in HH:mm or
	// HH:mm:ss form. An end of 00:00 or 00:00:00 represents the current day's
	// end.
	StartTime  string  `json:"start_time"` // inclusive local clock start
	EndTime    string  `json:"end_time"`   // exclusive local clock end
	Multiplier float64 `json:"multiplier"` // daily token-price scaling factor
}

// pricingIntervalResponse exposes stored context endpoints, optional amounts,
// and unit-tier labels.
type pricingIntervalResponse struct {
	ID int64 `json:"id"` // stored interval row identifier
	// MinTokens and MaxTokens describe a (MinTokens, MaxTokens] context range. A
	// nil upper endpoint leaves that range unbounded; billing mode also permits
	// labeled request or media unit tiers.
	MinTokens int    `json:"min_tokens"`           // excluded lower context-token endpoint
	MaxTokens *int   `json:"max_tokens"`           // included upper context-token endpoint
	TierLabel string `json:"tier_label,omitempty"` // request or media tier label
	// InputPrice, OutputPrice, CacheWritePrice, CacheWrite1hPrice, and
	// CacheReadPrice retain optional interval amounts. User token intervals
	// inherit missing buckets; provider statistics treats missing buckets under
	// its independent price calculation.
	InputPrice      *float64 `json:"input_price"`       // USD per interval input token
	OutputPrice     *float64 `json:"output_price"`      // USD per interval output token
	CacheWritePrice *float64 `json:"cache_write_price"` // USD per short-TTL interval cache-write token
	// CacheWrite1hPrice supplies a separate hourly interval amount, including
	// zero; absent hourly input lets an interval CacheWritePrice override cover
	// that bucket.
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price"`  // USD per hourly interval cache-write token
	CacheWrite30mPrice *float64 `json:"cache_write_30m_price"` // USD per thirty-minute write token
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price"` // USD per twelve-hour write token
	CacheReadPrice     *float64 `json:"cache_read_price"`      // USD per interval cache-read token
	// InputMultiplier, OutputMultiplier, CacheWriteMultiplier, and
	// CacheReadMultiplier scale inherited user token amounts when the
	// corresponding explicit amount is absent. Provider statistics calculations
	// ignore these interval factors.
	InputMultiplier      *float64 `json:"input_multiplier"`       // interval input-price scaling factor
	OutputMultiplier     *float64 `json:"output_multiplier"`      // interval output-price scaling factor
	CacheWriteMultiplier *float64 `json:"cache_write_multiplier"` // interval cache-write scaling factor
	CacheReadMultiplier  *float64 `json:"cache_read_multiplier"`  // interval cache-read scaling factor
	// PerRequestPrice supplies this unit tier, including free pricing at zero.
	// FilterValidRequestIntervals excludes rows with a nil amount from request
	// or media unit matching.
	PerRequestPrice *float64 `json:"per_request_price"` // USD per matched unit
	SortOrder       int      `json:"sort_order"`        // stored interval ordering key
}

// providerStatsPricingRuleResponse exposes an ordered provider-cost rule and
// its model cards.
type providerStatsPricingRuleResponse struct {
	ID   int64  `json:"id"`   // stored statistics-rule row identifier
	Name string `json:"name"` // administrator statistics-rule display name
	// GroupIDs and ProviderIDs are alternative selectors for this statistics
	// rule. Either matching list admits the rule; administrator writes require
	// at least one selector and a nonempty Pricing list.
	GroupIDs    []int64                `json:"group_ids"`    // matching group identifier list
	ProviderIDs []int64                `json:"provider_ids"` // matching provider identifier list
	Pricing     []modelPricingResponse `json:"pricing"`      // independent provider-cost model cards
}

// NewPricingHandler binds shared-price administration and catalog queries.
// Callers supply pricingConfigs for configuration routes and the catalog
// readers used by their query routes. The refresh endpoint reports an absent
// catalog or updater as unavailable.
func NewPricingHandler(pricingConfigs *routing.PricingConfigService, catalog *routing.PricingCatalog) *PricingHandler {
	return &PricingHandler{pricingConfigs: pricingConfigs, catalog: catalog}
}

// firstNonNilFloat selects primary when it is present and otherwise returns
// fallback. Pointer presence gives an explicit zero priority over the legacy
// amount, and the selected pointer remains shared with its input.
func firstNonNilFloat(primary, fallback *float64) *float64 {
	// Use pointer presence to resolve the current Fast field before its legacy
	// alias, since an explicit zero is still supplied pricing input.
	if primary != nil {
		return primary
	}
	return fallback
}

// pricingConfigToResponse maps a stored shared configuration or returns nil
// for nil input. Empty model-source values become group_mapped and absent
// collections become empty arrays. Billing settings' optional amounts are
// cloned, while group/provider selector slices, model-name slices, and
// amount, modifier, or endpoint pointers can retain source storage. Callers
// keep retained values stable during encoding.
func pricingConfigToResponse(ch *routing.PricingConfig) *pricingConfigResponse {
	if ch == nil {
		return nil
	}
	resp := &pricingConfigResponse{
		BillingSettings: ch.BillingSettings.Clone(),
		ID:              ch.ID,
		Name:            ch.Name,
		Description:     ch.Description,
		Status:          ch.Status,

		GroupIDs: ch.GroupIDs,

		CreatedAt: ch.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: ch.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	resp.BillingModelSource = ch.BillingModelSource
	if resp.BillingModelSource == "" {
		resp.BillingModelSource = routing.BillingModelSourceGroupMapped
	}
	if resp.GroupIDs == nil {
		resp.GroupIDs = []int64{}
	}

	resp.ModelPricing = make([]modelPricingResponse, 0, len(ch.ModelPricing))
	for _, p := range ch.ModelPricing {
		resp.ModelPricing = append(resp.ModelPricing, pricingToResponse(&p))
	}

	resp.ProviderStatsPricingRules = make([]providerStatsPricingRuleResponse, 0, len(ch.ProviderStatsPricingRules))
	for _, rule := range ch.ProviderStatsPricingRules {
		ruleResp := providerStatsPricingRuleResponse{
			ID:          rule.ID,
			Name:        rule.Name,
			GroupIDs:    rule.GroupIDs,
			ProviderIDs: rule.ProviderIDs,
			Pricing:     make([]modelPricingResponse, 0, len(rule.Pricing)),
		}
		if ruleResp.GroupIDs == nil {
			ruleResp.GroupIDs = []int64{}
		}
		if ruleResp.ProviderIDs == nil {
			ruleResp.ProviderIDs = []int64{}
		}
		for i := range rule.Pricing {
			ruleResp.Pricing = append(ruleResp.Pricing, pricingToResponse(&rule.Pricing[i]))
		}
		resp.ProviderStatsPricingRules = append(resp.ProviderStatsPricingRules, ruleResp)
	}

	return resp
}

// pricingToResponse maps a nonnil model card into administrator JSON fields.
// It defaults empty billing mode to token, represents missing model names as
// an empty list, and selects current Fast input before its legacy alias.
// Interval and time records are allocated; model-name slices and amount,
// modifier, or endpoint pointers can retain source storage. Keep those
// retained values read-only during encoding.
func pricingToResponse(p *routing.ModelPricingEntry) modelPricingResponse {
	models := p.Models
	if models == nil {
		models = []string{}
	}
	billingMode := string(p.BillingMode)
	if billingMode == "" {
		billingMode = string(routing.BillingModeToken)
	}
	intervals := make([]pricingIntervalResponse, 0, len(p.Intervals))
	for _, iv := range p.Intervals {
		intervals = append(intervals, intervalToResponse(iv))
	}
	return modelPricingResponse{
		ID: p.ID,

		Models:                       models,
		ReferenceModel:               p.ReferenceModel,
		BillingMode:                  billingMode,
		PriceMultiplier:              p.PriceMultiplier,
		FastModeMultiplier:           p.FastModeMultiplier,
		FastMultiplier:               firstNonNilFloat(p.FastMultiplier, p.FastModeMultiplier),
		FlexMultiplier:               p.FlexMultiplier,
		MaxReasoningEffortMultiplier: p.MaxReasoningEffortMultiplier,
		InputPrice:                   p.InputPrice,
		OutputPrice:                  p.OutputPrice,
		CacheWritePrice:              p.CacheWritePrice,
		CacheWrite1hPrice:            p.CacheWrite1hPrice, CacheWrite30mPrice: p.CacheWrite30mPrice, CacheWrite12hPrice: p.CacheWrite12hPrice,
		CacheReadPrice:   p.CacheReadPrice,
		ImageInputPrice:  p.ImageInputPrice,
		ImageOutputPrice: p.ImageOutputPrice,
		PerRequestPrice:  p.PerRequestPrice,
		Intervals:        intervals,
		TimePricing:      timePricingToResponse(p.TimePricing),
	}
}

// timePricingToResponse returns a new HTTP schedule and period list, or nil
// for nil input. It retains the configured timezone, clock spellings, weekday
// flag, and original period order for administrator display.
func timePricingToResponse(value *routing.TimePricingConfig) *timePricingResponse {
	if value == nil {
		return nil
	}
	periods := make([]timePricingPeriodResponse, 0, len(value.Periods))
	for _, period := range value.Periods {
		periods = append(periods, timePricingPeriodResponse{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &timePricingResponse{
		Timezone:     value.Timezone,
		WeekdaysOnly: value.WeekdaysOnly,
		Periods:      periods,
	}
}

// intervalToResponse maps a stored interval into administrator fields without
// resolving inherited prices. Optional bounds, amount pointers, and
// multiplier pointers remain shared with the interval, so consumers treat
// them as read-only while encoding.
func intervalToResponse(iv routing.PricingInterval) pricingIntervalResponse {
	return pricingIntervalResponse{
		ID:                iv.ID,
		MinTokens:         iv.MinTokens,
		MaxTokens:         iv.MaxTokens,
		TierLabel:         iv.TierLabel,
		InputPrice:        iv.InputPrice,
		OutputPrice:       iv.OutputPrice,
		CacheWritePrice:   iv.CacheWritePrice,
		CacheWrite1hPrice: iv.CacheWrite1hPrice, CacheWrite30mPrice: iv.CacheWrite30mPrice, CacheWrite12hPrice: iv.CacheWrite12hPrice,
		CacheReadPrice:       iv.CacheReadPrice,
		InputMultiplier:      iv.InputMultiplier,
		OutputMultiplier:     iv.OutputMultiplier,
		CacheWriteMultiplier: iv.CacheWriteMultiplier,
		CacheReadMultiplier:  iv.CacheReadMultiplier,
		PerRequestPrice:      iv.PerRequestPrice,
		SortOrder:            iv.SortOrder,
	}
}

// pricingRequestToService converts HTTP cards into service entries in request
// order. Nil or empty input returns a nonnil empty slice. Empty billing mode
// becomes token and current Fast pointer presence takes precedence over its
// legacy alias.
//
// It allocates entries, intervals, and daily schedule records, while model
// slices, amount and modifier pointers, and upper endpoints remain shared
// with decoded input. Nil and zero amounts retain distinct representations;
// the service and pricing consumers validate and interpret those amounts.
func pricingRequestToService(reqs []modelPricingRequest) []routing.ModelPricingEntry {
	result := make([]routing.ModelPricingEntry, 0, len(reqs))
	for _, r := range reqs {
		billingMode := routing.BillingMode(r.BillingMode)
		if billingMode == "" {
			billingMode = routing.BillingModeToken
		}
		intervals := make([]routing.PricingInterval, 0, len(r.Intervals))
		for _, iv := range r.Intervals {
			intervals = append(intervals, routing.PricingInterval{
				MinTokens:         iv.MinTokens,
				MaxTokens:         iv.MaxTokens,
				TierLabel:         iv.TierLabel,
				InputPrice:        iv.InputPrice,
				OutputPrice:       iv.OutputPrice,
				CacheWritePrice:   iv.CacheWritePrice,
				CacheWrite1hPrice: iv.CacheWrite1hPrice, CacheWrite30mPrice: iv.CacheWrite30mPrice, CacheWrite12hPrice: iv.CacheWrite12hPrice,
				CacheReadPrice:       iv.CacheReadPrice,
				InputMultiplier:      iv.InputMultiplier,
				OutputMultiplier:     iv.OutputMultiplier,
				CacheWriteMultiplier: iv.CacheWriteMultiplier,
				CacheReadMultiplier:  iv.CacheReadMultiplier,
				PerRequestPrice:      iv.PerRequestPrice,
				SortOrder:            iv.SortOrder,
			})
		}
		result = append(result, routing.ModelPricingEntry{
			Models:                       r.Models,
			ReferenceModel:               r.ReferenceModel,
			BillingMode:                  billingMode,
			PriceMultiplier:              r.PriceMultiplier,
			FastModeMultiplier:           r.FastModeMultiplier,
			FastMultiplier:               firstNonNilFloat(r.FastMultiplier, r.FastModeMultiplier),
			FlexMultiplier:               r.FlexMultiplier,
			MaxReasoningEffortMultiplier: r.MaxReasoningEffortMultiplier,
			InputPrice:                   r.InputPrice,
			OutputPrice:                  r.OutputPrice,
			CacheWritePrice:              r.CacheWritePrice,
			CacheWrite1hPrice:            r.CacheWrite1hPrice, CacheWrite30mPrice: r.CacheWrite30mPrice, CacheWrite12hPrice: r.CacheWrite12hPrice,
			CacheReadPrice:   r.CacheReadPrice,
			ImageInputPrice:  r.ImageInputPrice,
			ImageOutputPrice: r.ImageOutputPrice,
			PerRequestPrice:  r.PerRequestPrice,
			Intervals:        intervals,
			TimePricing:      timePricingRequestToService(r.TimePricing),
		})
	}
	return result
}

// timePricingRequestToService returns a service schedule with newly allocated
// period records. Nil input remains nil, and supplied timezone, clock
// spellings, weekday restriction, and period order are copied for later
// service validation.
func timePricingRequestToService(value *timePricingRequest) *routing.TimePricingConfig {
	if value == nil {
		return nil
	}
	periods := make([]routing.TimePricingPeriod, 0, len(value.Periods))
	for _, period := range value.Periods {
		periods = append(periods, routing.TimePricingPeriod{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &routing.TimePricingConfig{
		Timezone:     value.Timezone,
		WeekdaysOnly: value.WeekdaysOnly,
		Periods:      periods,
	}
}

// providerStatsPricingRuleRequestToService maps selectors and converts this
// rule's model cards. Selector slices remain shared with decoded input; the
// enclosing HTTP writer assigns rule order before passing the result to the
// service.
func providerStatsPricingRuleRequestToService(r providerStatsPricingRuleRequest) routing.ProviderStatsPricingRule {
	return routing.ProviderStatsPricingRule{
		Name:        r.Name,
		GroupIDs:    r.GroupIDs,
		ProviderIDs: r.ProviderIDs,
		Pricing:     pricingRequestToService(r.Pricing),
	}
}

// List returns shared configurations with requested status, search, sorting,
// and pagination. Trimmed search is capped by bytes, and default ordering is
// descending created_at; service results are mapped into administrator DTOs
// with the returned matching total.
func (h *PricingHandler) List(c *gin.Context) {
	page, pageSize := httpx.ParsePagination(c)
	status := c.Query("status")
	search := strings.TrimSpace(c.Query("search"))
	// Cap filter bytes so price-configuration search input stays bounded.
	if len(search) > 100 {
		search = search[:100]
	}

	pricingConfigs, pag, err := h.pricingConfigs.List(c.Request.Context(), pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}, status, search)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	out := make([]*pricingConfigResponse, 0, len(pricingConfigs))
	for i := range pricingConfigs {
		out = append(out, pricingConfigToResponse(&pricingConfigs[i]))
	}
	httpx.Paginated(c, out, pag.Total, page, pageSize)
}

// GetByID parses the route's numeric price-configuration ID and returns its
// administrator DTO. Parse failures return INVALID_PRICING_CONFIG_ID, while
// the service validates whether the parsed ID identifies a stored
// configuration.
func (h *PricingHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	pricingConfig, err := h.pricingConfigs.GetByID(c.Request.Context(), id)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, pricingConfigToResponse(pricingConfig))
}

// Create strictly decodes shared price configuration input and forwards
// converted cards and settings to the service. Statistics rules require a
// group/provider selector and pricing entries; their request order becomes
// SortOrder before the service validates and persists the configuration.
func (h *PricingHandler) Create(c *gin.Context) {
	var req createPricingConfigRequest
	if err := httpx.BindJSONStrict(c, &req); err != nil {
		httpx.ErrorFrom(c, infraerrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	pricing := pricingRequestToService(req.ModelPricing)

	var statsRules []routing.ProviderStatsPricingRule
	for i, r := range req.ProviderStatsPricingRules {
		if len(r.GroupIDs) == 0 && len(r.ProviderIDs) == 0 {
			httpx.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_SCOPE",
				fmt.Sprintf("pricing rule #%d must have at least one group or provider", i+1)))
			return
		}
		if len(r.Pricing) == 0 {
			httpx.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_PRICING",
				fmt.Sprintf("pricing rule #%d must have at least one pricing entry", i+1)))
			return
		}
		rule := providerStatsPricingRuleRequestToService(r)
		// Keep request priority in stored positions because provider-cost selection
		// uses the first matching rule that can calculate this model's amount.
		rule.SortOrder = i
		statsRules = append(statsRules, rule)
	}

	pricingConfig, err := h.pricingConfigs.Create(c.Request.Context(), &routing.CreatePricingConfigInput{
		BillingSettingsPatch: req.patch(),
		Name:                 req.Name,
		Description:          req.Description,
		GroupIDs:             req.GroupIDs,
		ModelPricing:         pricing,

		BillingModelSource: req.BillingModelSource,

		ProviderStatsPricingRules: statsRules,
	})
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, pricingConfigToResponse(pricingConfig))
}

// Update strictly decodes changes for the route's numeric price-configuration
// ID. Omitted collection pointers leave stored cards and statistics rules
// unchanged; supplied collections replace them, with statistics rule request
// order assigned to SortOrder. Service validation and persistence complete
// before the administrator DTO is returned.
func (h *PricingHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	var req updatePricingConfigRequest
	if err := httpx.BindJSONStrict(c, &req); err != nil {
		httpx.ErrorFrom(c, infraerrors.BadRequest("VALIDATION_ERROR", err.Error()))
		return
	}

	input := &routing.UpdatePricingConfigInput{
		BillingSettingsPatch: req.patch(),
		Name:                 req.Name,
		Description:          req.Description,
		Status:               req.Status,
		GroupIDs:             req.GroupIDs,

		BillingModelSource: req.BillingModelSource,
	}
	if req.ModelPricing != nil {
		pricing := pricingRequestToService(*req.ModelPricing)
		input.ModelPricing = &pricing
	}
	if req.ProviderStatsPricingRules != nil {
		statsRules := make([]routing.ProviderStatsPricingRule, 0, len(*req.ProviderStatsPricingRules))
		for i, r := range *req.ProviderStatsPricingRules {
			if len(r.GroupIDs) == 0 && len(r.ProviderIDs) == 0 {
				httpx.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_SCOPE",
					fmt.Sprintf("pricing rule #%d must have at least one group or provider", i+1)))
				return
			}
			if len(r.Pricing) == 0 {
				httpx.ErrorFrom(c, infraerrors.BadRequest("PRICING_RULE_EMPTY_PRICING",
					fmt.Sprintf("pricing rule #%d must have at least one pricing entry", i+1)))
				return
			}
			rule := providerStatsPricingRuleRequestToService(r)
			// Keep request priority in stored positions because provider-cost
			// selection uses the first matching rule that can calculate this model's
			// amount.
			rule.SortOrder = i
			statsRules = append(statsRules, rule)
		}
		input.ProviderStatsPricingRules = &statsRules
	}

	pricingConfig, err := h.pricingConfigs.Update(c.Request.Context(), id, input)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, pricingConfigToResponse(pricingConfig))
}

// Delete parses the route's numeric price-configuration ID and delegates
// removal to the service. Parsing and service errors use standard HTTP
// responses; success returns the configuration deletion message after removal
// completes.
func (h *PricingHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.ErrorFrom(c, infraerrors.BadRequest("INVALID_PRICING_CONFIG_ID", "Invalid pricing configuration ID"))
		return
	}

	if err := h.pricingConfigs.Delete(c.Request.Context(), id); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, gin.H{"message": "PricingConfig deleted successfully"})
}

// GetModelDefaultPricing returns catalog token amounts for model-form
// autofill. A nonblank trimmed model is required; any lookup error produces
// successful found=false. Cache-write autofill uses a positive 5m rate when
// breakdown is supported, otherwise retains the aggregate rate, and exposes
// the 1h rate whenever breakdown is supported.
func (h *PricingHandler) GetModelDefaultPricing(c *gin.Context) {
	model := strings.TrimSpace(c.Query("model"))
	if model == "" {
		httpx.ErrorFrom(c, infraerrors.BadRequest("MISSING_PARAMETER", "model parameter is required").
			WithMetadata(map[string]string{"param": "model"}))
		return
	}
	pricing, err := h.catalog.DefaultPricing(model)
	// Autofill treats a failed catalog lookup as an unavailable default, letting
	// the administrator continue entering a manual model price.
	if err != nil {
		httpx.Success(c, gin.H{"found": false})
		return
	}

	cacheWritePrice := pricing.CacheCreationPricePerToken
	var cacheWrite1hPrice *float64
	// Keep the aggregate cache-write amount when no positive 5m amount exists;
	// breakdown metadata still exposes its 1h amount, including zero.
	if pricing.SupportsCacheBreakdown {
		if pricing.CacheCreation5mPrice > 0 {
			cacheWritePrice = pricing.CacheCreation5mPrice
		}
		cacheWrite1hPrice = &pricing.CacheCreation1hPrice
	}
	httpx.Success(c, gin.H{
		"found":                           true,
		"input_price":                     pricing.InputPricePerToken,
		"output_price":                    pricing.OutputPricePerToken,
		"cache_write_price":               cacheWritePrice,
		"cache_write_1h_price":            cacheWrite1hPrice,
		"cache_write_30m_price":           pricing.CacheCreation30mPrice,
		"cache_write_12h_price":           pricing.CacheCreation12hPrice,
		"cache_read_price":                pricing.CacheReadPricePerToken,
		"max_reasoning_effort_multiplier": pricing.MaxReasoningEffortMultiplier,
		"image_input_price":               pricing.ImageInputPricePerToken,
		"image_output_price":              pricing.ImageOutputPricePerToken,
	})
}
