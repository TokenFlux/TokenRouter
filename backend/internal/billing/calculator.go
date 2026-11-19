package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// calculator.go resolves billing prices and calculates or displays token,
// request, and media charges.
// A model price may come from a catalog entry or a configured card, and its
// billing mode decides whether reported tokens, requests or media units form
// the charge. Unified calculation resolves that choice before delegating
// arithmetic; the catalog-only entry points require token prices and apply
// current catalog schedules. Display lookup also retains missing pricing as an
// unpriced result.
//
// Configured time schedules need the request's supplied PricingAt, while
// catalog schedules can use the injected current clock when that value is
// zero. Image resolution replaces unit amounts on a copy so a caller's
// per-request tiers remain intact, and display reports which image sizes have
// available prices.

const (
	// CacheWriteFamilyClaude and CacheWriteFamilyOpenAI identify the two-tier
	// cache-write lifetime families used when settling cache-creation tokens.
	CacheWriteFamilyClaude = purepricing.CacheWriteFamilyClaude // five-minute and one-hour lifetimes
	CacheWriteFamilyOpenAI = purepricing.CacheWriteFamilyOpenAI // thirty-minute and twelve-hour lifetimes
)

// ModelPricing aliases normalized model rates consumed by pure billing
// calculations.
type ModelPricing = purepricing.ModelPricing

// UsageTokens aliases reported token buckets used by the billing calculator.
type UsageTokens = purepricing.UsageTokens

// CostBreakdown aliases component costs, base amount and user charge results.
type CostBreakdown = purepricing.CostBreakdown

// CacheWriteFamily aliases the two-tier cache-write lifetime scheme.
type CacheWriteFamily = purepricing.CacheWriteFamily

// CacheWriteRates aliases resolved USD-per-token cache-write rates.
type CacheWriteRates = purepricing.CacheWriteRates

// CacheWriteAnomaly aliases raw upstream cache-write diagnostic counters.
type CacheWriteAnomaly = purepricing.CacheWriteAnomaly

// CacheWriteSettlement aliases settled cache-write charges and bucket counts.
type CacheWriteSettlement = purepricing.CacheWriteSettlement

// CostInput combines reported usage with model, funding-rate and schedule
// inputs.
type CostInput struct {
	// Ctx is passed to price resolution when Resolver exists and Resolved is
	// absent. The configuration reader requires a usable context for that lookup.
	Ctx   context.Context // shared price lookup context
	Model string          // full billing model identity
	// GroupID selects shared pricing configuration during resolution. Nil skips
	// the configuration lookup, leaving the chosen full model to catalog pricing.
	GroupID *int64      // shared pricing configuration group
	Tokens  UsageTokens // reported token category counts
	// RequestCount supplies per-request, image or video quantity when UsageUnits
	// is nonpositive. A nonpositive count then becomes one in resolved-mode
	// calculation; catalog-only token calculation ignores this field.
	RequestCount int // request or media output count
	// UsageUnits replaces RequestCount when positive in per-request, image or
	// video calculation. Video cards use total output seconds; configured audio
	// cards use minutes, millions of characters or hours to match their unit
	// amount. Callers validate finite mode-specific quantities.
	UsageUnits     float64 // continuous billable unit quantity
	SizeTier       string  // requested price tier label
	RateMultiplier float64 // effective user charge factor
	// PricingAt supplies configured schedule time; zero leaves configured time
	// pricing inactive. ProjectCostInput separately supplies an injected
	// observation for catalog schedules in that case.
	PricingAt               time.Time        // configured schedule observation time
	ServiceTier             string           // forwarded billing service tier
	ReasoningEffort         string           // forwarded billing reasoning effort
	DefaultCacheWriteFamily CacheWriteFamily // protocol fallback cache-write TTL family
	// Resolver and Resolved govern reuse together: unified calculation consults
	// Resolved only when Resolver is present. Reused pricing belongs to this
	// request's full model and group.
	// Resolver enables configured-card mode dispatch in CalculateCostUnified. Nil
	// selects catalog token calculation even when Resolved contains a pricing
	// record.
	Resolver *PriceResolver // shared card and catalog resolver
	// Resolved reuses a prior price selection when Resolver is present. Callers
	// match it to this model and group; image preparation changes a shallow copy.
	Resolved *ResolvedPricing // previously resolved request pricing
}

// ModelDisplayPricing aliases price availability and unit amounts returned for
// display.
type ModelDisplayPricing = purepricing.ModelDisplayPricing

// audioPriceConfig aliases optional rates for realtime, TTS and STT
// operations.
type audioPriceConfig = purepricing.AudioPriceConfig

// PriceCatalog supplies catalog model amounts and an optional live refresh
// implementation.
type PriceCatalog interface {
	// GetModelPricing returns raw amounts for a full model identity or nil when
	// absent. Consumers retain missing entries as unavailable pricing and follow
	// the source's nested-data ownership rules.
	GetModelPricing(string) *purepricing.CatalogModelPricing
	// ForceUpdate requests refreshing the catalog used by billing price lookups.
	ForceUpdate() error
}

// CalculatorOptions supplies wall-clock observations and configured pricing
// time-zone resolution.
type CalculatorOptions struct {
	// Now supplies current catalog time observations. NewCalculator defaults it
	// to time.Now when nil, and WithPriceCatalog replaces it with one captured
	// query instant.
	Now func() time.Time // catalog pricing observation clock
	// LoadLocation resolves schedule time zones. NewCalculator defaults it to
	// time.LoadLocation; calculations discard its error and retain the companion
	// location. A nil location selects the unit time multiplier.
	LoadLocation func(string) (*time.Location, error) // pricing schedule zone loader
}

// Calculator resolves runtime prices and delegates arithmetic to the pure
// pricing package.
type Calculator struct {
	// catalog may be nil when no catalog source is bound. Lookup then reports
	// unavailable token pricing; full configured cards can still be resolved
	// through PriceResolver.
	catalog PriceCatalog      // model pricing catalog reader
	options CalculatorOptions // calculation clock and zone options
}

type (
	// CatalogModelPricing aliases raw catalog amounts before token calculation
	// normalization.
	CatalogModelPricing = purepricing.CatalogModelPricing
	// ModelPricingEntry aliases one configured model card with amounts and
	// modifiers.
	ModelPricingEntry = purepricing.ModelPricingEntry
)

// applyCostBreakdownMultiplier scales component costs and both totals in
// place. Nil breakdowns and a unit factor remain unchanged; callers supply
// finite factors, while billing mode and long-context flags retain their
// values.
func applyCostBreakdownMultiplier(cost *CostBreakdown, multiplier float64) {
	purepricing.ApplyCostBreakdownMultiplier(cost, multiplier)
}

// maxReasoningEffortBillingMultiplier returns the positive configured factor
// for normalized max effort. Missing prices/factors, other efforts and
// nonpositive factors select one; the model name does not participate in this
// delegated selection.
func maxReasoningEffortBillingMultiplier(model, effort string, pricing *ModelPricing) float64 {
	return purepricing.MaxReasoningEffortBillingMultiplier(model, effort, pricing)
}

// NewCalculator binds a catalog and calculation runtime without loading
// prices. Nil clock and location loader select time.Now and time.LoadLocation;
// a nil catalog leaves model lookup unavailable until configured data supplies
// pricing through a resolver.
func NewCalculator(catalog PriceCatalog, options CalculatorOptions) *Calculator {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LoadLocation == nil {
		options.LoadLocation = time.LoadLocation
	}
	return &Calculator{catalog: catalog, options: options}
}

// GetModelPricing resolves token calculation rates for the full lowercased
// model identity. Catalog absence or an unusable selected entry returns the
// pricing unavailable error; resolution applies the pricing package's catalog
// conversion rules.
func (s *Calculator) GetModelPricing(model string) (*ModelPricing, error) {
	model = strings.ToLower(model)
	var raw *CatalogModelPricing
	if s.catalog != nil {
		raw = s.catalog.GetModelPricing(model)
	}
	return purepricing.ResolveModelPricing(model, raw)
}

// GetModelPricingWithConfig loads catalog calculation rates before applying
// the supplied card overrides. Catalog errors return before override
// application, so this helper requires a catalog base even when a configured
// card contains standalone amounts.
func (s *Calculator) GetModelPricingWithConfig(model string, configPricing *ModelPricingEntry) (*ModelPricing, error) {
	pricing, err := s.GetModelPricing(model)
	if err != nil {
		return nil, err
	}
	return purepricing.ApplyConfigPrice(pricing, configPricing), nil
}

// CalculateCostUnified returns a charge using the resolved billing mode and
// reported usage. With no Resolver it uses catalog token calculation, then
// applies the selected max-effort factor. A supplied Resolved value is reused
// when Resolver exists, while absent resolved data invokes that resolver with
// Ctx and GroupID.
//
// Image mode resolves the size unit amount into a shallow pricing copy and
// clears its request tiers before pure calculation. Per-request mode retains
// label, context and default-price selection. This copy keeps the caller's
// resolved tier slice available for later uses.
func (s *Calculator) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	if input.Resolver == nil {
		breakdown, err := s.calculateCostInternalAtWithFamily(input.Model, input.Tokens, input.RateMultiplier, input.ServiceTier, nil, input.PricingAt, input.DefaultCacheWriteFamily)
		if err == nil {
			// The token charge is already available; a failed second lookup leaves
			// effort selection with the unit factor rather than discarding that
			// successful charge.
			price, _ := s.GetModelPricing(input.Model)
			applyCostBreakdownMultiplier(breakdown, maxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, price))
		}
		return breakdown, err
	}

	// A supplied resolution fixes the request's price selection here; obtain
	// another resolution only when that earlier selection is absent.
	resolved := input.Resolved
	if resolved == nil {
		resolved = input.Resolver.Resolve(input.Ctx, PricingInput{
			Model:   input.Model,
			GroupID: input.GroupID,
		})
	}

	// Image charging needs one selected size amount, so update a shallow result
	// copy and remove its request tiers; per-request selection retains those
	// tiers.
	if resolved.Mode == purepricing.BillingModeImage {
		price, err := s.resolvedImageUnitPrice(input.Model, input.SizeTier, resolved)
		if err != nil {
			return nil, err
		}
		copy := *resolved
		copy.RequestTiers = nil
		copy.DefaultPerRequestPrice = price
		copy.DefaultPerRequestPricePresent = true
		resolved = &copy
	}
	return purepricing.CalculateCost(resolved, s.ProjectCostInput(input, resolved))
}

// ComputeTokenBreakdown delegates token component arithmetic using the
// supplied non-nil prices. Callers provide nonnegative counts with
// representable context sums and finite amounts; service-tier and eligible
// long-context rules apply when requested, while a negative user rate becomes
// zero.
func (s *Calculator) ComputeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	return purepricing.ComputeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, applyLongCtx)
}

// CalculateCost charges catalog token usage with the supplied user rate and
// ordinary service tier. Catalog schedules use the injected current time,
// long-context rules are enabled, and unavailable model pricing returns an
// error.
func (s *Calculator) CalculateCost(model string, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, error) {
	return s.CalculateCostInternal(model, tokens, rateMultiplier, "", nil)
}

// CalculateCostWithServiceTier charges catalog token usage for the supplied
// service tier and user rate. Catalog schedules use the injected current time;
// long-context rules are enabled, and missing model rates return an error.
func (s *Calculator) CalculateCostWithServiceTier(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.CalculateCostInternal(model, tokens, rateMultiplier, serviceTier, nil)
}

// CalculateCostInternal resolves catalog token rates, optionally applying a
// supplied card, and computes component charges. Catalog lookup errors return
// before overrides; the catalog time schedule runs only when configPricing is
// nil. Callers supply finite rates and representable nonnegative token counts.
func (s *Calculator) CalculateCostInternal(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, configPricing *ModelPricingEntry) (*CostBreakdown, error) {
	return s.calculateCostInternalAt(model, tokens, rateMultiplier, serviceTier, configPricing, time.Time{})
}

// calculateCostInternalAt resolves the catalog for the supplied pricing instant.
// A missing instant uses the calculator clock. Calendar tier conditions and
// catalog time multipliers both see the same instant for a single charge.
func (s *Calculator) calculateCostInternalAt(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, configPricing *ModelPricingEntry, at time.Time) (*CostBreakdown, error) {
	return s.calculateCostInternalAtWithFamily(model, tokens, rateMultiplier, serviceTier, configPricing, at, "")
}

// calculateCostInternalAtWithFamily resolves catalog token rates for the
// supplied instant and forwards defaultFamily into cache-write settlement.
func (s *Calculator) calculateCostInternalAtWithFamily(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, configPricing *ModelPricingEntry, at time.Time, defaultFamily CacheWriteFamily) (*CostBreakdown, error) {
	var pricing *ModelPricing
	var err error
	if configPricing != nil {
		pricing, err = s.GetModelPricingWithConfig(model, configPricing)
	} else {
		pricing, err = s.GetModelPricing(model)
	}
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = s.options.Now()
	}
	if configPricing == nil {
		pricing = s.applyCatalogTimePricing(pricing, at)
	}

	return purepricing.ComputeTokenBreakdownAtWithFamily(pricing, tokens, rateMultiplier, serviceTier, true, at, defaultFamily), nil
}

// SettleCacheWrite partitions cache-creation tokens into billed TTL buckets and
// computes optional anomaly diagnostics through the shared pricing engine.
func SettleCacheWrite(rates CacheWriteRates, tokens UsageTokens, defaultFamily CacheWriteFamily, multiplier float64) CacheWriteSettlement {
	return purepricing.SettleCacheWrite(rates, tokens, defaultFamily, multiplier)
}

// ForceUpdatePricing asks the bound catalog to refresh its pricing data. A
// missing catalog returns an initialization error; a present catalog's refresh
// result is returned directly to the administrator caller.
func (s *Calculator) ForceUpdatePricing() error {
	if s.catalog != nil {
		return s.catalog.ForceUpdate()
	}
	return fmt.Errorf("pricing service not initialized")
}

// DisplayPricing returns available video or image unit amounts before attempting
// catalog token display. A negative user rate becomes zero; unavailable token
// lookup or buckets yield an unpriced display result, while catalog time rules
// use the injected clock.
func (s *Calculator) DisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	// A negative effective rate produces zero displayed amounts while keeping
	// model price availability as a separate display decision.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	if quote, found := purepricing.BuildVideoDisplayPricing(nil, s.RawModelPricing(model), rateMultiplier); found {
		return quote
	}
	if mediaPricing, found := s.imageDisplayPricing(model, rateMultiplier); found {
		return mediaPricing
	}

	pricing, err := s.GetModelPricing(model)
	if err != nil || pricing == nil || !hasAnyDisplayTokenPricing(pricing) {
		// Lookup failure or absent token buckets leaves no usable token display;
		// return the unpriced marker so callers do not show fabricated free amounts.
		return unknownDisplayPricing()
	}

	return buildTokenDisplayPricing(s.applyCatalogTimePricing(pricing, s.options.Now()), rateMultiplier)
}

// DisplayPricingWithResolvedMultipliers builds video, image, per-request or token
// display from resolved prices. Missing resolved data can use ordinary catalog
// display, and negative user rates become zero. A declared video, image or
// per-request mode stays unpriced when its mode-specific display cannot be
// built.
//
// An unpriced token result can still use independent catalog media prices when
// no configured card was selected. Catalog-sourced token base prices receive
// their time multiplier on a shallow result copy before display. Other
// unsuccessful resolved display attempts return to the ordinary catalog
// lookup.
func (s *Calculator) DisplayPricingWithResolvedMultipliers(model string, rateMultiplier float64, resolved *ResolvedPricing) ModelDisplayPricing {
	if resolved != nil && resolved.LookupModel != nil {
		model = *resolved.LookupModel
	}
	// A negative effective rate produces zero displayed amounts while keeping
	// model price availability as a separate display decision.
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	// Declared media and per-request modes use their own display availability; a
	// failed mode-specific quote remains unknown so unrelated catalog token
	// amounts are not substituted.
	if resolved != nil && resolved.Mode == purepricing.BillingModeImage {
		if quote, ok := s.imageDisplayPricingWithResolved(model, rateMultiplier, resolved); ok {
			return quote
		}
		return unknownDisplayPricing()
	}
	if resolved != nil && (resolved.Mode == purepricing.BillingModePerRequest || resolved.Mode == purepricing.BillingModeVideo) {
		if quote, ok := displayPricingFromResolved(model, rateMultiplier, resolved); ok {
			return quote
		}
		return unknownDisplayPricing()
	}
	if resolved == nil {
		return s.DisplayPricing(model, rateMultiplier)
	}
	if resolved.ConfigPricing == nil {
		if quote, found := purepricing.BuildVideoDisplayPricing(nil, s.RawModelPricing(model), rateMultiplier); found {
			return quote
		}
	}
	if resolved.IsUnpriced() {
		// An unpriced token record can still describe a catalog image model; check
		// its independent image units only when a configured card did not select
		// pricing.
		if resolved.ConfigPricing == nil {
			if mediaPricing, found := s.imageDisplayPricing(model, rateMultiplier); found {
				return mediaPricing
			}
		}
		return unknownDisplayPricing()
	}
	if resolved != nil && resolved.Source == PricingSourceCatalog && resolved.BasePricing != nil {
		copy := *resolved
		copy.BasePricing = s.applyCatalogTimePricing(resolved.BasePricing, s.options.Now())
		resolved = &copy
	}
	if pricing, ok := displayPricingFromResolved(model, rateMultiplier, resolved); ok {
		return pricing
	}
	return s.DisplayPricing(model, rateMultiplier)
}

// imageDisplayPricing delegates catalog image display and reports whether any
// supported size has a price. It uses the supplied multiplier and the same
// image classification and size availability rules as the resolved helper.
func (s *Calculator) imageDisplayPricing(model string, rateMultiplier float64) (ModelDisplayPricing, bool) {
	return s.imageDisplayPricingWithResolved(model, rateMultiplier, nil)
}

// imageDisplayPricingWithResolved quotes available 1K, 2K and 4K unit amounts
// and lists the sizes found. Catalog-only queries require image metadata, a
// recognized image model name or a token-absent entry; supplied resolved
// pricing takes the unit selection path directly.
//
// Each size is resolved independently, and lookup errors omit that size from
// ImagePriceSizes while its numeric slot stays zero. At least one successful
// size makes found true, including a known free amount. Callers supply a
// finite multiplier; the helper reads pricing inputs without changing them.
func (s *Calculator) imageDisplayPricingWithResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	raw := s.RawModelPricing(model)
	knownImage := raw != nil && len(raw.ImagePrices) > 0
	if resolved == nil && !knownImage && !hasExplicitImageGenerationPricing(raw) && (raw == nil || !raw.TokenPricingAbsent) {
		return ModelDisplayPricing{}, false
	}
	prices := make([]float64, 3)
	found := false
	var sizes []string
	for i, size := range []string{"1K", "2K", "4K"} {
		price, err := purepricing.ResolveImageUnitPrice(resolved, raw, size)
		if err != nil {
			// A failed size lookup omits that dimension from ImagePriceSizes, while
			// other independently priced dimensions remain available in the returned
			// display.
			continue
		}
		found = true
		sizes = append(sizes, size)
		prices[i] = price * rateMultiplier
	}
	result := buildImageDisplayPricing(prices[0], prices[1], prices[2])
	result.ImagePriceSizes = sizes
	return result, found
}

// displayPricingFromResolved delegates resolved-price display and reports
// whether an available view can be built. Nil or unsupported pricing can
// return false, allowing the owning caller to choose its catalog or unknown
// result.
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	return purepricing.DisplayPricingFromResolved(model, rateMultiplier, resolved)
}

// buildTokenDisplayPricing delegates token-unit display from a non-nil
// normalized price record. The caller supplies a finite effective rate and
// validates availability before this builder marks its returned token view as
// priced.
func buildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	return purepricing.BuildTokenDisplayPricing(pricing, rateMultiplier)
}

// buildImageDisplayPricing delegates 1K, 2K and 4K image-unit display
// construction.
func buildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return purepricing.BuildImageDisplayPricing(price1K, price2K, price4K)
}

// unknownDisplayPricing returns unknown mode and unpriced status for display
// consumers.
func unknownDisplayPricing() ModelDisplayPricing { return purepricing.UnknownDisplayPricing() }

// CalculateWebSearchCost charges positive calls using a configured
// USD-per-call amount or catalog default. A present zero is known free;
// missing rates or nonpositive counts return an empty breakdown. Callers
// validate finite nonnegative prices and rates.
func (s *Calculator) CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateWebSearchCost(callCount, operationPrice("web_search", groupPrice, s.operationPrices().WebSearchPricePerCall), rateMultiplier)
}

// CalculateSearchCost charges calls using a configured or catalog
// USD-per-thousand-call amount. Missing, negative or zero selected prices and
// nonpositive counts return an empty breakdown; a negative user rate becomes
// zero, and callers supply finite inputs.
func (s *Calculator) CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateSearchCost(numCalls, operationPrice("search", groupPricePer1k, s.operationPrices().SearchPricePer1k), rateMultiplier)
}

// CalculateAudioCost charges selected realtime minutes, TTS millions of
// characters or STT hours. Configured mode rates precede catalog defaults;
// unknown modes, nonpositive usage or nonpositive selected prices return an
// empty breakdown. Mode comparison ignores case but keeps whitespace.
func (s *Calculator) CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *audioPriceConfig, rateMultiplier float64) *CostBreakdown {
	return purepricing.CalculateAudioCost(mode, durationOrUnits, s.audioPrices(mode, groupConfig), rateMultiplier)
}

// CalculateImageCost charges imageCount using the catalog unit amount for the
// normalized image size. Nonpositive counts return an empty breakdown before
// lookup; positive counts require a validated finite price, including known
// zero, and a negative user rate becomes zero.
func (s *Calculator) CalculateImageCost(model, imageSize string, imageCount int, rateMultiplier float64) (*CostBreakdown, error) {
	if imageCount <= 0 {
		return purepricing.CalculateImageCost(0, imageCount, rateMultiplier), nil
	}
	imageSize = purepricing.NormalizeImageBillingTierOrDefault(imageSize)
	price, err := s.DefaultImagePrice(model, imageSize)
	if err != nil {
		return nil, err
	}
	return purepricing.CalculateImageCost(price, imageCount, rateMultiplier), nil
}

// CalculateVideoCost charges videoCount copies of a normalized catalog-priced
// duration. Nonpositive counts return an empty breakdown before lookup;
// positive counts normalize resolution and duration, validate the unit amount,
// and use zero for a negative user rate.
func (s *Calculator) CalculateVideoCost(model, resolution string, videoCount, durationSeconds int, rateMultiplier float64) (*CostBreakdown, error) {
	if videoCount <= 0 {
		return purepricing.CalculateVideoCost(0, videoCount, durationSeconds, rateMultiplier), nil
	}
	resolution = purepricing.NormalizeVideoBillingResolutionOrDefault(resolution)
	durationSeconds = purepricing.NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	price, err := s.DefaultVideoPrice(model, resolution)
	if err != nil {
		return nil, err
	}
	return purepricing.CalculateVideoCost(price, videoCount, durationSeconds, rateMultiplier), nil
}

// DefaultImagePrice returns a validated per-image amount for the full model
// and normalized size. Missing dimensions or entries return pricing
// unavailable, while an explicitly present zero remains free; nonempty size
// maps define the allowed dimensions.
func (s *Calculator) DefaultImagePrice(model, imageSize string) (float64, error) {
	var raw *CatalogModelPricing
	if s.catalog != nil {
		raw = s.catalog.GetModelPricing(model)
	}
	price, found := purepricing.DefaultImagePrice(raw, imageSize)
	if !found {
		return 0, fmt.Errorf("image pricing not found for model %s: %w", model, purepricing.ErrModelPricingUnavailable)
	}
	return purepricing.ValidateImageUnitPrice(price)
}

// RawModelPricing returns the selected raw catalog entry or nil when
// unavailable. Nil receivers and missing catalogs return nil; callers follow
// the catalog's ownership rules for the returned entry and its nested data.
func (s *Calculator) RawModelPricing(model string) *CatalogModelPricing {
	if s == nil || s.catalog == nil {
		return nil
	}
	return s.catalog.GetModelPricing(model)
}

// hasExplicitImageGenerationPricing delegates recognition of explicit catalog
// image-generation unit amounts.
func hasExplicitImageGenerationPricing(pricing *CatalogModelPricing) bool {
	return purepricing.HasExplicitImageGenerationPricing(pricing)
}

// hasAnyDisplayTokenPricing delegates recognition of available normalized
// token display amounts.
func hasAnyDisplayTokenPricing(pricing *ModelPricing) bool {
	return purepricing.HasAnyDisplayTokenPricing(pricing)
}

// DefaultVideoPrice returns a validated catalog amount per output second for
// the normalized resolution. Missing entries or dimensions return pricing
// unavailable, while a present zero remains free; validation rejects negative
// or nonfinite amounts.
func (s *Calculator) DefaultVideoPrice(model string, resolution string) (float64, error) {
	if price, ok := purepricing.DefaultVideoPrice(s.RawModelPricing(model), resolution); ok {
		return purepricing.ValidateImageUnitPrice(price)
	}
	return 0, fmt.Errorf("video pricing not found for model %s: %w", model, purepricing.ErrModelPricingUnavailable)
}

// ProjectCostInput builds pure calculation inputs while keeping configured and
// catalog schedule observations separate. PricingAt passes through unchanged
// for configured time rules; a zero PricingAt selects the injected clock only
// for ModelPricingAt, so that zero does not activate configured schedules.
//
// Configured and base-model time zones are loaded independently. Each lookup
// retains its returned location and discards its error; a nil location selects
// the unit time multiplier. Callers who want load failures to leave amounts
// unscaled supply a loader that returns nil with its error. Usage and scalar
// fields retain the supplied request values for mode dispatch and arithmetic.
func (s *Calculator) ProjectCostInput(input CostInput, resolved *ResolvedPricing) purepricing.CostInput {
	var location *time.Location
	if resolved != nil && resolved.ConfigPricing != nil && resolved.ConfigPricing.TimePricing != nil {
		// Configured matching uses the companion location even when loading
		// reports an error; only a nil location disables this schedule's multiplier.
		location, _ = s.options.LoadLocation(resolved.ConfigPricing.TimePricing.Timezone)
	}
	modelAt := input.PricingAt
	if modelAt.IsZero() {
		modelAt = s.options.Now()
	}
	var modelLocation *time.Location
	if resolved != nil && resolved.BasePricing != nil && resolved.BasePricing.TimePricing != nil {
		// The loader's error is discarded; its returned catalog location governs
		// matching independently of the configured schedule's location.
		modelLocation, _ = s.options.LoadLocation(resolved.BasePricing.TimePricing.Timezone)
	}
	return purepricing.CostInput{ModelTimeLocation: modelLocation, Model: input.Model, Tokens: input.Tokens, RequestCount: input.RequestCount, UsageUnits: input.UsageUnits, SizeTier: input.SizeTier, RateMultiplier: input.RateMultiplier, PricingAt: input.PricingAt, ModelPricingAt: modelAt, ServiceTier: input.ServiceTier, ReasoningEffort: input.ReasoningEffort, TimePricingLocation: location, DefaultCacheWriteFamily: input.DefaultCacheWriteFamily}
}

// GetModelModalities queries exact identity metadata when the catalog
// implements that optional capability. A nil calculator or unsupported catalog
// returns nil lists; modality results follow that metadata reader's rules,
// independently of price lookup aliases.
func (s *Calculator) GetModelModalities(model string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	if source, ok := s.catalog.(interface {
		// GetModelModalities returns input and output modalities for the exact
		// catalog identity. Unknown or absent identity data can yield nil lists
		// independently of model price availability.
		GetModelModalities(string) ([]string, []string)
	}); ok {
		return source.GetModelModalities(model)
	}
	return nil, nil
}
