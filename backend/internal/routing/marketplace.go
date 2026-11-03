package routing

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// marketplace.go assembles authorized group model listings with quotes, route
// attributes, and optional observations.
// Visible groups must have active provider counts and requestable model
// definitions before prices or observations are assembled. Group visibility
// requires explicit grants for exclusive groups and a resolved catalog.
// Exclusive results precede public groups; repository order remains stable
// within each category. Public entrypoints supply no exclusive grants.
//
// Provider prefetch can share rows across groups; failure selects per-group
// model resolution instead. Visible definitions retain public IDs, pricing
// identities, and full provider attribute targets so an alias can display
// metadata for its route without borrowing a different model's price.
// Attribute, capacity, availability, and currency-setting failures leave the
// model listing available with unknown optional information. An installed
// attribute reader that returns no data stays authoritative, while an absent
// reader permits legacy pricing-modality lookup.

// Marketplace setting keys select bounded probe-history windows and currency
// inputs used when displaying the group's configured credit conversion values.
const (
	SettingKeyMarketplaceAvailabilityWindowDays    = "marketplace_availability_window_days"
	SettingKeyMarketplaceAvailabilityBucketMinutes = "marketplace_availability_bucket_minutes"
	SettingKeyReasoningPointRMBUnitPrice           = "reasoning_point_rmb_unit_price"
	SettingKeyUSDExchangeRate                      = "usd_exchange_rate"
)

// ModelMarketplaceGroup contains a visible product group with models and
// optional observations.
type ModelMarketplaceGroup struct {
	SearchTerms []string          // saved localized search variants
	Resolution  locale.Resolution // display-language selection metadata
	ID          int64             // visible group row ID
	IsExclusive bool              // requires explicit user authorization
	Name        string            // resolved localized display name
	Description string            // resolved localized group description

	// DisplayBrand uses the trimmed configured value and otherwise the
	// administrative group Name. It is independent of localized display copy and
	// provider platform.
	DisplayBrand   string  // resolved product brand label
	SortOrder      int     // saved administrative display position
	RateMultiplier float64 // base group price scaling factor
	// HideMaxDiscount and HideRateMultiplier suppress independent group header
	// tags. Only saved boolean true values hide tags; absent feature options keep
	// the corresponding tag visible.
	HideMaxDiscount    bool // hide discount header tag
	HideRateMultiplier bool // hide multiplier header tag
	// OfficialPriceRatio and OfficialPriceRMBEquivalent are emitted only when both
	// currency settings are valid. They derive from the group factor, with nil
	// representing unavailable conversion metadata.
	OfficialPriceRatio         *float64 // configured group currency conversion ratio
	OfficialPriceRMBEquivalent *float64 // configured group RMB equivalent
	// Capacity is omitted when IncludeCapacity is false, a capacity reader is
	// absent or fails, or the returned map contains no entry for this group.
	Capacity *GroupCapacitySummary // optional group capacity summary
	// Availability is queried for visible groups with enabled probe configuration.
	// Nil represents absent readers, failed queries, or an unavailable history
	// entry. The supplied summary and its nested buckets remain borrowed from the
	// history reader and are treated as read-only.
	Availability *GroupAvailabilitySummary // optional recorded probe summary
	ModelCount   int                       // returned requestable model count
	Models       []ModelMarketplaceModel   // eligible visible model presentations
}

// ModelMarketplaceModel contains one public request ID with quote and display
// metadata.
type ModelMarketplaceModel struct {
	// Attributes points to a value copy of the callback's attribute result. Its
	// nested pointers and slices follow that supplied storage, so public consumers
	// treat the presentation as read-only.
	Attributes *EffectiveModelAttributes // optional resolved route attribute value
	// ProtocolAttributes contains available ranges by client protocol. The values
	// borrow callback storage and remain read-only while the response is encoded.
	ProtocolAttributes map[capability.ProtocolID]modelcatalog.Presentation // per-protocol display attribute ranges
	ID                 string                                              // public request model ID
	// DisplayName uses the resolved attribute's non-nil display-name value,
	// including an explicitly empty string. Otherwise public assembly retains the
	// label supplied in the model definition.
	DisplayName string                      // resolved model display label
	Pricing     pricing.ModelDisplayPricing // resolved public model quote
	// InputModalities and OutputModalities follow resolved attributes when their
	// reader is installed. Missing results remain unknown; legacy pricing metadata
	// is used only when that reader is absent.
	InputModalities  []string // known model input modalities
	OutputModalities []string // known model output modalities
	// Protocols and NativeProtocols retain slices from the model definition in
	// resolver order. Callers leave them unchanged while their source and the
	// public presentation reference the same storage.
	Protocols []capability.ProtocolID // ordered requestable client protocols
	// NativeProtocols identifies admitted client protocols whose resolved routes
	// all execute natively. This distinguishes the native subset from the overall
	// requestable entries carried in Protocols.
	NativeProtocols []capability.ProtocolID // protocols native across resolved routes
}

// MarketplaceListOptions chooses whether a marketplace query includes runtime
// capacity observations.
type MarketplaceListOptions struct {
	// IncludeCapacity gates capacity aggregation for this listing. False skips
	// that reader and yields nil Capacity fields while probe-history and model
	// queries continue.
	IncludeCapacity bool // query optional runtime capacity
}

// marketplaceDiscountConfig contains validated currency settings for group
// conversion metadata.
type marketplaceDiscountConfig struct {
	// reasoningPointRMBUnitPrice and usdExchangeRate are validated together as
	// positive finite values before either group conversion output is emitted.
	// Their values come from one settings read.
	reasoningPointRMBUnitPrice float64 // RMB per reasoning point
	usdExchangeRate            float64 // RMB per USD
}

// MarketplaceModelDef retains public, pricing, and provider-route identities
// during marketplace assembly.
type MarketplaceModelDef struct {
	// AttributeTargets, UpstreamModels, Protocols, and NativeProtocols retain the
	// resolver's slice storage. Attribute batching and public assembly read those
	// slices, so callers keep them immutable while a query or returned
	// presentation retains them.
	//
	// AttributeTargets keeps complete provider/model routes and execution
	// conditions, while the public ID remains the key used to return resolved
	// attributes.
	AttributeTargets []modelcatalog.Target   // resolved provider attribute routes
	UpstreamModels   []string                // resolved upstream model IDs
	Protocols        []capability.ProtocolID // ordered requestable client protocols
	NativeProtocols  []capability.ProtocolID // protocols native across resolved routes
	ID               string                  // public request model ID
	DisplayName      string                  // initial public model label
	// PricingModel is chosen by requestable resolution and supplied to quoting
	// directly. Blank values use ID, preventing marketplace assembly from applying
	// another mapping step.
	PricingModel string // resolved complete pricing model
	// PricingAmbiguous keeps the public quote unknown even when a price reader is
	// installed. This prevents display assembly from selecting one route's price
	// for an unresolved mixed-route identity.
	PricingAmbiguous bool // routes lack one pricing identity
}

// MarketplaceGroups supplies active group records for marketplace visibility
// filtering.
type MarketplaceGroups interface {
	// ListActive returns saved active groups in the reader's display ordering.
	ListActive(context.Context) ([]Group, error)
}

// MarketplaceSettings reads stored observation and currency settings in
// requested batches.
type MarketplaceSettings interface {
	// GetMultiple returns stored strings indexed by the requested setting keys.
	GetMultiple(context.Context, []string) (map[string]string, error)
}

// MarketplaceModels supplies provider prefetch and per-group requestable model
// resolution.
type MarketplaceModels interface {
	// Prefetch returns shared provider candidates when its reader supports that
	// operation. The boolean distinguishes supported empty results from an
	// unavailable optimization, and errors let the caller retry model resolution
	// per group.
	Prefetch(context.Context) ([]CatalogueProvider, bool, error)
	// ResolveRequestableModels returns eligible configured model definitions for
	// the requested group.
	ResolveRequestableModels(context.Context, *int64, string) RequestableModelsResult
}

// MarketplaceCapacity reads group capacity summaries used as optional marketplace
// observations.
type MarketplaceCapacity interface {
	// GetGroupCapacityByIDs returns capacity summaries indexed by the selected
	// group IDs.
	GetGroupCapacityByIDs(context.Context, []int64) (map[int64]GroupCapacitySummary, error)
}

// MarketplaceAvailability reads saved probe summaries for selected authorized
// groups.
type MarketplaceAvailability interface {
	// GetSummaryByGroupIDs reads availability counts and the latest stored status
	// for the selected groups. Window and bucket sizes govern the time series,
	// while the supplied timezone and query time define its observation range.
	GetSummaryByGroupIDs(context.Context, []int64, int, int, string, time.Time) (map[int64]*GroupAvailabilitySummary, error)
}

// MarketplaceQuoteRequest identifies the group and model used for a public
// price quote.
type MarketplaceQuoteRequest struct {
	Model          string  // complete intended pricing model
	GroupID        int64   // associated product group ID
	RateMultiplier float64 // group user-price scaling factor
	// FreeFastApplicable enables the quote reader's configured Standard-equivalent
	// display when supported. PublicModelPricing supplies true; the pricing
	// configuration and reader determine whether free Fast applies.
	FreeFastApplicable bool // apply configured free-Fast display
}

// MarketplacePrices supplies model quotes and optional legacy modality
// metadata.
type MarketplacePrices interface {
	// Quote calculates public display pricing for the supplied model and group.
	Quote(context.Context, MarketplaceQuoteRequest) pricing.ModelDisplayPricing
	// GetModelModalities returns legacy catalog input and output modalities for
	// the model.
	GetModelModalities(string) ([]string, []string)
}

// MarketplaceOptions supplies batched attributes, observation timezone, clock,
// and diagnostics.
type MarketplaceOptions struct {
	// Attributes receives visible groups' definitions in one call with borrowed
	// target and upstream-model slices. Its presence selects authoritative
	// attribute modalities, even when an error leaves results unknown.
	// optional batched route attribute reader
	Attributes func(context.Context, map[int64][]RequestableModel) (map[int64]map[string]MarketplaceModelAttributes, error)
	// Timezone is trimmed before probe-summary queries and defaults to UTC when
	// blank. The history reader decides its response to an invalid nonblank
	// timezone.
	Timezone string // availability bucket interpretation timezone
	// Now is called when querying a nonempty set of visible probe-enabled groups.
	// NewMarketplace retains it as supplied, so that observation path requires a
	// configured callback.
	Now func() time.Time // availability query time callback
	// Warn is invoked on provider-prefetch and attribute-reader failures.
	// NewMarketplace retains it as supplied, so callers configure it before using
	// paths that can report those diagnostics.
	Warn func(string, ...any) // synchronous marketplace diagnostic callback
}

// Marketplace assembles eligible visible models with shared quotes and optional
// observations.
type Marketplace struct {
	groups       MarketplaceGroups       // active product group reader
	settings     MarketplaceSettings     // currency and observation settings reader
	models       MarketplaceModels       // provider and requestable model reader
	requestable  RequestableResolver     // prefetched-provider model eligibility resolver
	prices       MarketplacePrices       // public model quote reader
	capacity     MarketplaceCapacity     // optional runtime capacity reader
	availability MarketplaceAvailability // optional probe-history summary reader
	options      MarketplaceOptions      // attribute and observation callbacks
}

// NewMarketplace retains readers for active groups, models, prices, and
// optional observations. Construction performs no queries; callers configure
// Now when querying enabled probe history and Warn for provider-prefetch or
// attribute-error reporting paths.
func NewMarketplace(groups MarketplaceGroups, settings MarketplaceSettings, models MarketplaceModels, resolver RequestableResolver, prices MarketplacePrices, capacity MarketplaceCapacity, availability MarketplaceAvailability, options MarketplaceOptions) *Marketplace {
	return &Marketplace{groups: groups, settings: settings, models: models, requestable: resolver, prices: prices, capacity: capacity, availability: availability, options: options}
}

// ListPublic returns only public groups using the shared marketplace assembly.
// No exclusive grants are supplied, even when the HTTP caller happens to have
// a session. Repository failures and model-resolution failures return errors.
func (s *Marketplace) ListPublic(ctx context.Context, options MarketplaceListOptions) ([]ModelMarketplaceGroup, error) {
	return s.ListVisible(ctx, nil, options)
}

// ListVisible assembles public groups and explicitly authorized exclusive groups.
// Callers obtain exclusiveGroupIDs from trusted, current user permissions;
// nil grants restrict the result to public groups. Active repository records
// require active providers and requestable models before display enrichment.
//
// Authorization filtering precedes model resolution, attribute batching, and
// optional observation reads. Exclusive groups precede public groups while
// preserving repository order within each category. Provider prefetch failures
// fall back to individual reads; model-resolution failures abort the query.
// Optional attributes, observations, and currency metadata may remain absent
// without hiding otherwise eligible groups. IncludeCapacity disables capacity
// work when false, while probe history remains limited to visible groups.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#marketplace_visibility
func (s *Marketplace) ListVisible(ctx context.Context, exclusiveGroupIDs []int64, options MarketplaceListOptions) ([]ModelMarketplaceGroup, error) {
	if s.requestable.Attributes != nil && ModelAttributesFromContext(ctx) == nil {
		ctx = WithModelAttributes(ctx, s.requestable.Attributes)
	}
	groups, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	allowed := make(map[int64]struct{}, len(exclusiveGroupIDs))
	for _, id := range exclusiveGroupIDs {
		allowed[id] = struct{}{}
	}

	providersByGroup, providersPrefetched := s.PrefetchProviders(ctx)
	visibleGroups := make([]Group, 0, len(groups))
	definitions := make(map[int64][]MarketplaceModelDef, len(groups))
	for i := range groups {
		group := &groups[i]
		_, authorized := allowed[group.ID]
		if (group.IsExclusive && !authorized) || group.ActiveProviderCount <= 0 {
			continue
		}

		var models []MarketplaceModelDef
		var resolveErr error
		if providersPrefetched {
			models, resolveErr = s.resolveGroupModelsWithProviders(ctx, group, providersByGroup[group.ID])
		} else {
			models, resolveErr = s.resolveGroupModels(ctx, group)
		}
		if resolveErr != nil {
			return nil, resolveErr
		}
		if len(models) == 0 {
			continue
		}
		visibleGroups = append(visibleGroups, *group)
		definitions[group.ID] = models
	}
	if len(visibleGroups) == 0 {
		return []ModelMarketplaceGroup{}, nil
	}

	slices.SortStableFunc(visibleGroups, func(left, right Group) int {
		if left.IsExclusive == right.IsExclusive {
			return 0
		}
		if left.IsExclusive {
			return -1
		}
		return 1
	})

	attributes := s.readAttributes(ctx, definitions)
	discountConfig, showDiscount := s.getOfficialPriceRatioConfig(ctx)
	var capacityMap map[int64]GroupCapacitySummary
	if options.IncludeCapacity {
		capacityMap = s.getVisibleCapacityMap(ctx, visibleGroups)
	}
	availabilityMap := s.getVisibleAvailabilityMap(ctx, visibleGroups)
	out := make([]ModelMarketplaceGroup, 0, len(visibleGroups))
	for i := range visibleGroups {
		group := &visibleGroups[i]
		models := s.buildPublicModels(ctx, group, definitions[group.ID], attributes[group.ID])

		var officialPriceRatio *float64
		var officialPriceRMBEquivalent *float64
		if showDiscount {
			officialPriceRatio = discountConfig.officialPriceRatio(group.RateMultiplier)
			officialPriceRMBEquivalent = discountConfig.officialPriceRMBEquivalent(group.RateMultiplier)
		}
		display, resolution := GroupDisplay(group, locale.FromContext(ctx))
		// Missing or non-boolean display options keep tags visible so historical
		// feature maps continue to produce the default marketplace header.
		hideMaxDiscount, _ := group.RoutingPolicy.FeaturesConfig["hide_marketplace_max_discount"].(bool)
		hideRateMultiplier, _ := group.RoutingPolicy.FeaturesConfig["hide_marketplace_rate_multiplier"].(bool)
		out = append(out, ModelMarketplaceGroup{
			SearchTerms: GroupSearchTexts(group), Resolution: resolution,
			ID:          group.ID,
			IsExclusive: group.IsExclusive,
			Name:        display.DisplayName,
			Description: display.Description,

			DisplayBrand:               marketplaceGroupDisplayBrand(group),
			SortOrder:                  group.SortOrder,
			RateMultiplier:             group.RateMultiplier,
			HideMaxDiscount:            hideMaxDiscount,
			HideRateMultiplier:         hideRateMultiplier,
			OfficialPriceRatio:         officialPriceRatio,
			OfficialPriceRMBEquivalent: officialPriceRMBEquivalent,
			Capacity:                   marketplaceGroupCapacity(capacityMap, group.ID),
			Availability:               marketplaceGroupAvailability(availabilityMap, group.ID),
			ModelCount:                 len(models),
			Models:                     models,
		})
	}

	return out, nil
}

// PrefetchProviders groups prefetched provider values by membership and sorts
// each pool by global priority then ID. Nil service or model reader,
// unsupported prefetch, and observed read errors return false so ListVisible can
// use per-group resolution.
//
// ProviderGroupIDs and GroupIDs are both accepted; IDs already present in
// ProviderGroupIDs are skipped when reading the second list. The function does
// not normalize duplicates within either supplied list. Provider structs and
// their nested data remain borrowed from the prefetch result, so consumers keep
// them read-only while this query resolves its models.
func (s *Marketplace) PrefetchProviders(ctx context.Context) (map[int64][]CatalogueProvider, bool) {
	if s == nil || s.models == nil {
		return nil, false
	}
	providers, available, err := s.models.Prefetch(ctx)
	if !available && err == nil {
		return nil, false
	}
	if err != nil {
		// A failed shared provider read leaves per-group resolution available, so
		// report the failure and let ListVisible query each group's candidates through
		// its normal reader.
		s.options.Warn("failed to prefetch marketplace providers", "error", err)
		return nil, false
	}

	providersByGroup := make(map[int64][]CatalogueProvider)
	for i := range providers {
		provider := providers[i]
		seenGroups := make(map[int64]struct{}, len(provider.GroupIDs)+len(provider.ProviderGroupIDs))
		for _, providerGroup := range provider.ProviderGroupIDs {
			if providerGroup <= 0 {
				continue
			}
			seenGroups[providerGroup] = struct{}{}
			providersByGroup[providerGroup] = append(providersByGroup[providerGroup], provider)
		}
		for _, groupID := range provider.GroupIDs {
			if groupID <= 0 {
				continue
			}
			if _, exists := seenGroups[groupID]; exists {
				continue
			}
			providersByGroup[groupID] = append(providersByGroup[groupID], provider)
		}
	}

	for groupID := range providersByGroup {
		groupProviders := providersByGroup[groupID]
		sort.SliceStable(groupProviders, func(i, j int) bool {
			if groupProviders[i].Priority != groupProviders[j].Priority {
				return groupProviders[i].Priority < groupProviders[j].Priority
			}
			return groupProviders[i].ID < groupProviders[j].ID
		})
		providersByGroup[groupID] = groupProviders
	}
	return providersByGroup, true
}

// getVisibleCapacityMap reads optional capacity for authorized groups with active
// provider counts. Missing readers or usable IDs yield nil; a reader error also
// yields nil so model and price output can continue.
func (s *Marketplace) getVisibleCapacityMap(ctx context.Context, groups []Group) map[int64]GroupCapacitySummary {
	if s.capacity == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.ActiveProviderCount <= 0 {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	// Capacity does not determine saved model eligibility here; a failed
	// observation query therefore omits its values while retaining the visible
	// groups and their prices.
	capacityMap, err := s.capacity.GetGroupCapacityByIDs(ctx, groupIDs)
	if err != nil {
		return nil
	}
	return capacityMap
}

// marketplaceGroupCapacity returns a separate summary value for the group's
// capacity entry. Missing maps or entries return nil, allowing consumers to
// distinguish unavailable observations from a present summary whose counters
// are all zero.
func marketplaceGroupCapacity(capacityMap map[int64]GroupCapacitySummary, groupID int64) *GroupCapacitySummary {
	if len(capacityMap) == 0 {
		return nil
	}
	capacity, ok := capacityMap[groupID]
	if !ok {
		return nil
	}
	return &capacity
}

// getVisibleAvailabilityMap reads optional history for authorized probe-enabled
// groups with active providers. Missing readers, empty usable IDs, or query
// failure returns nil; it passes the normalized display window, trimmed
// timezone or UTC, and injected query time.
func (s *Marketplace) getVisibleAvailabilityMap(ctx context.Context, groups []Group) map[int64]*GroupAvailabilitySummary {
	if s.availability == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.ActiveProviderCount <= 0 {
			continue
		}
		if !group.AvailabilityProbeConfig.Enabled {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	timezone := "UTC"
	if strings.TrimSpace(s.options.Timezone) != "" {
		timezone = strings.TrimSpace(s.options.Timezone)
	}
	// Probe history is optional observation data, so a failed read yields unknown
	// history while the already resolved model catalog and quotes remain
	// available.
	windowDays, bucketMinutes := s.resolveMarketplaceAvailabilityWindow(ctx)
	availabilityMap, err := s.availability.GetSummaryByGroupIDs(ctx, groupIDs, windowDays, bucketMinutes, timezone, s.options.Now())
	if err != nil {
		return nil
	}
	return availabilityMap
}

// resolveMarketplaceAvailabilityWindow reads the current marketplace window and
// bucket settings. Missing settings readers or read failures select shared
// defaults; usable stored values are parsed and normalized together before an
// availability query.
func (s *Marketplace) resolveMarketplaceAvailabilityWindow(ctx context.Context) (int, int) {
	if s == nil || s.settings == nil {
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	settings, err := s.settings.GetMultiple(ctx, []string{
		SettingKeyMarketplaceAvailabilityWindowDays,
		SettingKeyMarketplaceAvailabilityBucketMinutes,
	})
	if err != nil {
		// Missing display settings during a read failure select the shared window
		// defaults, keeping optional history queries within the established
		// response-size limits.
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	return ParseMarketplaceAvailabilityWindowSettings(settings)
}

// ParseMarketplaceAvailabilityWindowSettings parses positive stored day and
// minute values independently before applying shared window normalization.
// Missing, malformed, or nonpositive values retain their defaults, and
// normalization can widen buckets to bound the returned series.
func ParseMarketplaceAvailabilityWindowSettings(settings map[string]string) (int, int) {
	windowDays := DefaultMarketplaceAvailabilityWindowDays
	bucketMinutes := DefaultMarketplaceAvailabilityBucketMinutes
	// Parse the two stored values independently so a valid day count still applies
	// when only the bucket value is malformed, with defaults bounding the remaining
	// unknown input.
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityWindowDays])); err == nil && parsed > 0 {
		windowDays = parsed
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityBucketMinutes])); err == nil && parsed > 0 {
		bucketMinutes = parsed
	}
	return NormalizeMarketplaceAvailabilityWindow(windowDays, bucketMinutes)
}

// marketplaceGroupAvailability returns the borrowed history summary indexed by
// the group ID. Missing maps or entries return nil; supplied summaries and
// their nested buckets remain read-only when passed through to public output.
func marketplaceGroupAvailability(availabilityMap map[int64]*GroupAvailabilitySummary, groupID int64) *GroupAvailabilitySummary {
	if len(availabilityMap) == 0 {
		return nil
	}
	return availabilityMap[groupID]
}

// marketplaceGroupDisplayBrand returns the trimmed configured brand for a
// non-nil group. A blank brand uses the administrative Name rather than the
// localized display copy, keeping branding independent of provider platform.
func marketplaceGroupDisplayBrand(group *Group) string {
	if brand := strings.TrimSpace(group.DisplayBrand); brand != "" {
		return brand
	}
	return group.Name
}

// officialPriceRatio returns the group's configured credit-to-currency
// conversion ratio for display. It multiplies the group factor by the RMB price
// per point and divides by the configured RMB-per-USD exchange rate.
//
// Callers use validated positive currency settings; a nonpositive, NaN, or
// infinite calculated ratio yields nil. For a factor of two, one RMB per point,
// and seven RMB per USD, the ratio is 2/7. This group-level metadata is
// calculated independently of individual model-card amounts.
func (c marketplaceDiscountConfig) officialPriceRatio(rateMultiplier float64) *float64 {
	ratio := rateMultiplier * c.reasoningPointRMBUnitPrice / c.usdExchangeRate
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil
	}
	return &ratio
}

// officialPriceRMBEquivalent returns the RMB equivalent implied by the group
// factor and price per point. The calculation multiplies those configured
// values, so a factor of two at one RMB per point produces two RMB of
// equivalent value.
//
// Nonpositive, NaN, or infinite results yield nil for display. The method uses
// no per-model price fields; ListVisible calls it after both point-price and
// exchange-rate settings have passed validation, sharing that
// currency-information gate with officialPriceRatio.
func (c marketplaceDiscountConfig) officialPriceRMBEquivalent(rateMultiplier float64) *float64 {
	amount := rateMultiplier * c.reasoningPointRMBUnitPrice
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil
	}
	return &amount
}

// getOfficialPriceRatioConfig reads the two currency settings required for
// group conversion display. A nil reader, storage error, missing setting, or
// nonpositive or nonfinite parsed value returns false.
//
// Point price and exchange rate are validated together before ListVisible
// exposes either ratio or RMB equivalent. This gate keeps partly configured
// currency data from producing misleading output, even though the
// equivalent-value formula itself uses only point price and group factor. Both
// values come from one GetMultiple call.
func (s *Marketplace) getOfficialPriceRatioConfig(ctx context.Context) (marketplaceDiscountConfig, bool) {
	if s.settings == nil {
		return marketplaceDiscountConfig{}, false
	}

	settings, err := s.settings.GetMultiple(ctx, []string{
		SettingKeyReasoningPointRMBUnitPrice,
		SettingKeyUSDExchangeRate,
	})
	if err != nil {
		// A settings read failure omits currency conversion metadata rather than
		// guessing an exchange rate, while the model listing can still use its price
		// quotes.
		return marketplaceDiscountConfig{}, false
	}

	// Validate both currency inputs before exposing either group conversion value,
	// preventing one configured amount from making an incomplete currency setup
	// look ready for display.
	price, priceOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyReasoningPointRMBUnitPrice])
	exchangeRate, exchangeRateOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyUSDExchangeRate])
	if !priceOK || !exchangeRateOK {
		return marketplaceDiscountConfig{}, false
	}

	return marketplaceDiscountConfig{
		reasoningPointRMBUnitPrice: price,
		usdExchangeRate:            exchangeRate,
	}, true
}

// parsePositiveMarketplaceSettingFloat parses a trimmed numeric setting and
// reports a positive finite value. Malformed text, zero, negative values, NaN,
// and infinities return false, so currency calculations can be gated before
// use.
func parsePositiveMarketplaceSettingFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

// ModelsForGroup resolves saved requestable definitions and uses
// BuildPublicModels for their quotes and metadata. Nil groups and empty
// resolutions yield nil, following the resolver's configured model eligibility
// rules.
func (s *Marketplace) ModelsForGroup(ctx context.Context, group *Group) []ModelMarketplaceModel {
	if s.requestable.Attributes != nil && ModelAttributesFromContext(ctx) == nil {
		ctx = WithModelAttributes(ctx, s.requestable.Attributes)
	}
	models, err := s.resolveGroupModels(ctx, group)
	if err != nil {
		return nil
	}
	return s.BuildPublicModels(ctx, group, models)
}

// BuildPublicModels resolves display attributes and prices for supplied group
// model definitions. Empty definitions return nil before using group fields;
// otherwise callers supply a non-nil group, and attribute failures leave its
// models available with unknown attributes.
func (s *Marketplace) BuildPublicModels(ctx context.Context, group *Group, modelDefs []MarketplaceModelDef) []ModelMarketplaceModel {
	if len(modelDefs) == 0 {
		return nil
	}

	attributes := s.readAttributes(ctx, map[int64][]MarketplaceModelDef{group.ID: modelDefs})
	return s.buildPublicModels(ctx, group, modelDefs, attributes[group.ID])
}

// readAttributes converts visible model definitions into one grouped attribute
// query. Nil callbacks or empty definitions return nil, while callback failures
// are observed and converted to unavailable attributes.
//
// Each requestable input retains the public ID, upstream models, and full
// attribute targets from its definition so alias display can resolve the actual
// provider route. The newly allocated input lists borrow those nested slices;
// callbacks keep them read-only. Price identities are not substituted for those
// route targets when assembling attribute input.
func (s *Marketplace) readAttributes(ctx context.Context, definitions map[int64][]MarketplaceModelDef) map[int64]map[string]MarketplaceModelAttributes {
	if s.options.Attributes == nil || len(definitions) == 0 {
		return nil
	}
	groups := make(map[int64][]RequestableModel, len(definitions))
	for id, modelDefs := range definitions {
		inputs := make([]RequestableModel, 0, len(modelDefs))
		for _, value := range modelDefs {
			inputs = append(inputs, RequestableModel{ID: value.ID, UpstreamModels: value.UpstreamModels, AttributeTargets: value.AttributeTargets})
		}
		groups[id] = inputs
	}
	attributes, err := s.options.Attributes(ctx, groups)
	if err != nil {
		// Keep model definitions and quotes after attribute loading fails. Because
		// the reader is installed, later assembly leaves attributes unknown rather
		// than substituting legacy guessed modalities.
		s.options.Warn("failed to read model attributes", "error", err)
		return nil
	}
	return attributes
}

// buildPublicModels combines supplied definitions, optional resolved
// attributes, and shared quotes in definition order. Ambiguous pricing remains
// unknown, while an available attribute name can replace the display label
// without changing the public model ID.
//
// An installed attribute reader makes its data authoritative for modalities. A
// missing or failed result from that reader leaves fields unknown; legacy
// pricing metadata is consulted only when no attribute reader is installed.
// Protocol slices, per-protocol attribute maps, and nested attribute storage
// retain their source allocations. The returned presentation remains read-only.
func (s *Marketplace) buildPublicModels(ctx context.Context, group *Group, modelDefs []MarketplaceModelDef, attributes map[string]MarketplaceModelAttributes) []ModelMarketplaceModel {
	models := make([]ModelMarketplaceModel, 0, len(modelDefs))
	for _, modelDef := range modelDefs {
		pricing := pricing.UnknownDisplayPricing()
		if s.prices != nil && !modelDef.PricingAmbiguous {
			pricing = s.RequestableModelPricing(ctx, group, modelDef)
		}
		var inputModalities, outputModalities []string
		var presentation *EffectiveModelAttributes
		var protocolAttributes map[capability.ProtocolID]modelcatalog.Presentation
		if s.options.Attributes != nil {
			if value, ok := attributes[modelDef.ID]; ok {
				presentation = &value.Presentation
				protocolAttributes = value.Protocols
				attrs := value.Presentation
				if attrs.DisplayName != nil {
					modelDef.DisplayName = *attrs.DisplayName
				}
				if attrs.InputModalities != nil {
					inputModalities = *attrs.InputModalities
				}
				if attrs.OutputModalities != nil {
					outputModalities = *attrs.OutputModalities
				}
			}
		} else {
			// Legacy modality metadata is used only when no attribute reader is
			// installed, keeping a failed authoritative read from silently changing
			// display data sources.
			inputModalities, outputModalities = s.ModelModalities(modelDef)
		}

		models = append(models, ModelMarketplaceModel{
			ID:                 modelDef.ID,
			Attributes:         presentation,
			ProtocolAttributes: protocolAttributes,
			DisplayName:        modelDef.DisplayName,
			Pricing:            pricing,
			InputModalities:    inputModalities,
			OutputModalities:   outputModalities,
			Protocols:          modelDef.Protocols,
			NativeProtocols:    modelDef.NativeProtocols,
		})
	}

	return models
}

// ModelModalities queries legacy pricing metadata for the trimmed PricingModel
// or, when blank, the public ID. A missing price reader returns unknown nil
// modalities; callers use resolved attributes instead when an attribute reader
// is installed.
func (s *Marketplace) ModelModalities(modelDef MarketplaceModelDef) ([]string, []string) {
	if s.prices == nil {
		return nil, nil
	}
	pricingModel := strings.TrimSpace(modelDef.PricingModel)
	if pricingModel == "" {
		pricingModel = modelDef.ID
	}
	return s.prices.GetModelModalities(pricingModel)
}

// RequestableModelPricing quotes the resolver's trimmed PricingModel, falling
// back to the public ID when blank. Marketplace performs no further model
// mapping here, so the resolver's selected billing identity remains the
// argument supplied to the quote reader.
func (s *Marketplace) RequestableModelPricing(ctx context.Context, group *Group, model MarketplaceModelDef) pricing.ModelDisplayPricing {
	pricingModel := strings.TrimSpace(model.PricingModel)
	if pricingModel == "" {
		pricingModel = model.ID
	}
	return s.PublicModelPricing(ctx, group, pricingModel)
}

// PublicModelPricing requests a model quote with the group's base factor and
// free-Fast display applicability. A missing price reader returns unknown
// pricing; otherwise callers supply a non-nil group and the complete intended
// pricing model.
func (s *Marketplace) PublicModelPricing(ctx context.Context, group *Group, model string) pricing.ModelDisplayPricing {
	if s.prices == nil {
		return pricing.UnknownDisplayPricing()
	}
	return s.prices.Quote(ctx, MarketplaceQuoteRequest{Model: model, GroupID: group.ID, RateMultiplier: group.RateMultiplier, FreeFastApplicable: true})
}

// resolveGroupModels returns definitions from the configured per-group
// requestable resolver. Missing group or model reader and an empty resolved
// catalog yield nil, keeping public listings empty when no eligible configured
// candidates are available.
func (s *Marketplace) resolveGroupModels(ctx context.Context, group *Group) ([]MarketplaceModelDef, error) {
	if s.models != nil && group != nil {
		groupID := group.ID
		resolution := s.models.ResolveRequestableModels(ctx, &groupID, "")
		if resolution.Err != nil {
			return nil, resolution.Err
		}
		if len(resolution.Models) > 0 {
			return buildMarketplaceModelDefsFromRequestable(resolution.Models), nil
		}
		// An empty eligibility result keeps the group catalog empty so static
		// platform suggestions cannot introduce models absent from its configured
		// provider and group rules.
		return nil, nil
	}

	if group == nil {
		return nil, nil
	}
	return nil, nil
}

// resolveGroupModelsWithProviders resolves one group using the supplied
// prefetched provider pool. Missing service, model reader, or group yields nil;
// an empty resolution stays empty so prefetch cannot introduce platform-default
// candidates. Resolver policy failures and cancellation observed during its
// checks also yield no definitions.
func (s *Marketplace) resolveGroupModelsWithProviders(ctx context.Context, group *Group, providers []CatalogueProvider) ([]MarketplaceModelDef, error) {
	if s == nil || s.models == nil || group == nil {
		return nil, nil
	}
	groupID := group.ID
	resolution := s.requestable.ResolveWithProviders(ctx, &groupID, "", nil, providers)
	if resolution.Err != nil {
		return nil, resolution.Err
	}
	if len(resolution.Models) == 0 {
		return nil, nil
	}
	return buildMarketplaceModelDefsFromRequestable(resolution.Models), nil
}

// buildMarketplaceModelDefsFromRequestable converts requestable model results
// into public assembly definitions. Public IDs become the initial display
// names, while selected pricing identity, ambiguity, upstream models,
// protocols, and attribute targets are retained.
//
// Nested slices are assigned by value and remain shared with the resolver
// results. Keeping the full route targets beside the public ID prevents an
// alias from losing the provider model used by attribute resolution. Callers
// treat these definitions as read-only and let quote assembly preserve
// ambiguity as unknown pricing.
func buildMarketplaceModelDefsFromRequestable(models []RequestableModel) []MarketplaceModelDef {
	defs := make([]MarketplaceModelDef, 0, len(models))
	for _, model := range models {
		defs = append(defs, MarketplaceModelDef{
			ID:               model.ID,
			AttributeTargets: model.AttributeTargets,
			UpstreamModels:   model.UpstreamModels,
			Protocols:        model.Protocols,
			NativeProtocols:  model.NativeProtocols,
			DisplayName:      model.ID,
			PricingModel:     model.PricingModel,
			PricingAmbiguous: model.PricingAmbiguous,
		})
	}
	return defs
}
