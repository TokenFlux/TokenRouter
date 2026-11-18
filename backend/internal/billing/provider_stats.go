package billing

import (
	"context"
	"strings"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// provider_stats.go resolves provider reporting costs from configured rules
// and catalog prices.
// Provider cost statistics can use configured cost cards independently
// of the user's charge. A custom rule can deliberately return a
// zero cost, while nil leaves the usage consumer to use its ordinary
// total-cost base. The provider multiplier is applied after this
// base is selected, so it is not part of these returned costs.
//
// Resolution requires the statistics source to return a configuration,
// then tries configured rules before catalog token calculation. Source
// errors or a missing configuration return nil immediately; catalog
// lookup is reached when loaded rules supply no override. For
// request-name preference, ordered requested, group-mapped, and
// upstream candidates keep a provider's public model identity available
// without discarding its later mapping names.

// ProviderStatsPricingConfig retains loaded model-cost rules for provider statistics.
type ProviderStatsPricingConfig struct {
	Rules []purepricing.ProviderStatsPricingRule // ordered provider-cost rules
}

// ProviderStatsSource reads group-configured provider cost rules for statistics
// resolution.
type ProviderStatsSource interface {
	// ProviderStatsGroup returns the provider statistics configuration
	// for a group or a read error. ResolveProviderStats treats an error
	// or nil configuration as unavailable and skips both overrides
	// and catalog calculation on that path.
	ProviderStatsGroup(context.Context, int64) (*ProviderStatsPricingConfig, error)
}

// ProviderStatsCostInput supplies model identity, usage, and forwarded
// modifiers for provider cost.
type ProviderStatsCostInput struct {
	PricingAt time.Time // instant used for catalog calendar rates
	// DisableCatalogFallback preserves unknown expense for cross-vendor aliases.
	// Configured provider cards still apply, but another vendor's catalog cannot.
	DisableCatalogFallback bool

	// PreferRequestedModel enables public request-name preference for
	// configured rules. completion.Recorder.applyProviderStatsCost supplies true
	// for Qoder platform rows, while catalog calculation continues
	// using UpstreamModel.
	PreferRequestedModel bool
	// ProviderID and GroupID select configured statistics rules.
	// MatchProviderStatsRule accepts a match in either configured identity
	// list, while a rule with both lists empty matches neither source.
	ProviderID, GroupID int64 // ProviderID: execution provider identity; GroupID: final routing group
	// UpstreamModel, RequestedModel, and MappedModel retain execution,
	// client, and group-mapped identities. ProviderStatsRuleModels
	// selects their rule lookup order, while the default catalog path
	// receives the upstream model directly.
	UpstreamModel, RequestedModel, MappedModel string      // UpstreamModel: execution model name; RequestedModel: client model name; MappedModel: group-mapped model name
	Tokens                                     UsageTokens // observed token category counts
	// RequestCount is forwarded to custom statistics cards. ModelFileStatsCost
	// omits both this field and Resolver, so CalculateCostUnified uses catalog
	// token rates directly and operation count does not affect that path.
	RequestCount int // observed priced operation count
	// ServiceTier and ReasoningEffort describe the final forwarded
	// modifiers used by catalog cost calculation. Configured statistics
	// overrides receive usage and operation count, with their own
	// independent cost cards.
	ServiceTier, ReasoningEffort string // ServiceTier: forwarded pricing tier; ReasoningEffort: forwarded effort value
}

// ResolveProviderStats returns a provider cost base from configured
// rules or catalog prices. A returned zero pointer is an explicit
// custom cost; nil lets the usage writer retain its total-cost
// base before applying the provider multiplier.
//
// Missing resolver/source, an empty upstream model, source errors,
// or nil configuration return nil before custom or catalog pricing.
// Loaded rules use the selected identity order and preserve a
// handled override. When no override is handled, an available
// calculator tries catalog token pricing with the final service tier
// and effort; its unavailable or nonpositive result also becomes nil.
func (r *PriceResolver) ResolveProviderStats(ctx context.Context, input ProviderStatsCostInput) *float64 {
	cost, _ := r.ResolveProviderStatsWithMode(ctx, input)
	return cost
}

// ResolveProviderStatsWithMode resolves provider expense and its pricing unit.
// A missing source or unavailable amount returns nil; token catalog fallbacks
// return token mode, and explicit free custom cards preserve their selected mode.
//
// The amount and unit come from the same configured rule snapshot and ordered
// model candidates. System accounting needs both to distinguish partial token
// costs from an operation charge that requires a completed request. Existing
// customer callers can continue using ResolveProviderStats without changing the
// cost precedence, multiplier ownership, or missing-configuration behavior.
func (r *PriceResolver) ResolveProviderStatsWithMode(ctx context.Context, input ProviderStatsCostInput) (*float64, purepricing.BillingMode) {
	if r == nil || r.providerStats == nil || input.UpstreamModel == "" {
		return nil, ""
	}
	configPricing, err := r.providerStats.ProviderStatsGroup(ctx, input.GroupID)
	// A failed statistics read supplies no trusted rule configuration.
	// Return nil to the usage consumer instead of continuing into
	// catalog calculation as though the configuration had been loaded.
	if err != nil || configPricing == nil {
		return nil, ""
	}
	if cost, mode := purepricing.ResolveProviderStatsOverrideWithMode(purepricing.ProviderStatsInput{
		Rules: configPricing.Rules, ProviderID: input.ProviderID, GroupID: input.GroupID,
		Models: ProviderStatsRuleModels(input.PreferRequestedModel, input.UpstreamModel, input.RequestedModel, input.MappedModel),
		Tokens: input.Tokens, RequestCount: input.RequestCount,
	}); cost != nil {
		return cost, mode
	}
	if r.calculator == nil || input.DisableCatalogFallback {
		return nil, ""
	}
	// Loaded configuration supplied no custom cost. Catalog calculation
	// uses the upstream identity and forwarded modifiers as the
	// remaining source for this provider base.
	return r.calculator.modelFileStatsCostAt(input.UpstreamModel, input.Tokens, input.ServiceTier, input.ReasoningEffort, input.PricingAt), purepricing.BillingModeToken
}

// ModelFileStatsCost calculates catalog token cost at unit user rate using
// normalized service tier and the supplied final effort. Its CostInput omits
// Resolver, so CalculateCostUnified chooses direct catalog token calculation
// regardless of operation counts. Errors, absent breakdowns, and totals at or
// below zero return nil; the custom-rule path can represent explicit zero.
func (s *Calculator) ModelFileStatsCost(model string, tokens UsageTokens, serviceTier, effort string) *float64 {
	return s.modelFileStatsCostAt(model, tokens, serviceTier, effort, time.Time{})
}

// modelFileStatsCostAt computes provider cost at the same instant as user billing.
// A zero time delegates to the calculator clock. Only usable positive catalog
// amounts produce a provider cost pointer, matching the established fallback.
func (s *Calculator) modelFileStatsCostAt(model string, tokens UsageTokens, serviceTier, effort string, at time.Time) *float64 {
	breakdown, err := s.CalculateCostUnified(CostInput{Model: model, Tokens: tokens, RateMultiplier: 1, ServiceTier: purepricing.NormalizeBillingServiceTier(serviceTier), ReasoningEffort: effort, PricingAt: at})
	// Catalog lookup cannot represent an explicit free-cost override
	// through this helper. An unavailable or nonpositive result leaves
	// provider-cost selection to the usage consumer's existing nil
	// handling.
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}

// ProviderStatsRuleModels returns trimmed identities in the order
// used for custom statistics cards. Without request preference or
// a requested name, it selects only upstream and returns nil
// when that name is empty. Exactly matching requested/upstream text
// with no different mapping also uses that single upstream candidate.
//
// With request preference and a differing identity, requested comes
// first, followed by the first optional mapped name and upstream.
// The shared helper removes empty and case-insensitive duplicate
// names while retaining the first spelling. Additional mapped
// arguments are ignored.
func ProviderStatsRuleModels(preferRequested bool, upstreamModel, requestedModel string, groupMappedModel ...string) []string {
	upstreamModel = strings.TrimSpace(upstreamModel)
	requestedModel = strings.TrimSpace(requestedModel)
	mappedModel := ""
	if len(groupMappedModel) > 0 {
		mappedModel = strings.TrimSpace(groupMappedModel[0])
	}
	if !preferRequested || requestedModel == "" ||
		(requestedModel == upstreamModel && (mappedModel == "" || mappedModel == requestedModel)) {
		if upstreamModel == "" {
			return nil
		}
		return []string{upstreamModel}
	}
	models := []string{requestedModel}
	models = append(models, mappedModel)
	models = append(models, upstreamModel)
	return purepricing.UniqueNonEmptyProviderStatsModels(models)
}
