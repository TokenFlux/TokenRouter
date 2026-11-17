package provider

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// model_availability.go supplies persistent provider candidates and
// protocol-aware model checks for availability diagnosis.
// Model-availability diagnosis reads persistently active, schedulable
// providers even when transient health windows prevent execution. The adapter
// narrows platforms for forced entries, applies request protocol policy, and
// lets routing distinguish a missing model from temporary capacity using
// provider model rules.

// AvailabilityProviders supplies persistent candidate records for
// model-availability diagnosis and display.
type AvailabilityProviders interface {
	// ListModelAvailabilityCandidates reads active, schedulable records for diagnosis.
	// The optional group scopes memberships; with no group, grouped chooses all
	// matching records or those without memberships, while transient windows remain unfiltered.
	ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]provider.Record, error)
}

// modelRejectionRules adapts provider model configuration to routing rejection
// diagnostics.
type modelRejectionRules struct {
	*provider.Record /* borrowed provider diagnostic record */
}

// NewModelAvailability binds group model mapping and optional persistent
// provider reads for diagnosis. A nil source leaves only mapping configured;
// forced request platforms replace the requested platform list before each
// read, and lookup errors propagate.
// @project-doc docs/architecture/provider_scheduling_and_cache.md#advanced_scheduler_selection
func NewModelAvailability(source AvailabilityProviders, groupPolicies *routing.PricingConfigService, compatible bool) *routing.ModelAvailability {
	result := &routing.ModelAvailability{
		MapModel: groupPolicies.ResolveRoutingModel,
	}
	if source == nil {
		return result
	}
	result.Read = func(ctx context.Context, group *int64, platforms []string, grouped bool) ([]routing.AvailabilityProvider, error) {
		if forced, ok := apikey.ForcePlatformFromContext(ctx); ok && strings.TrimSpace(forced) != "" {
			platforms = []string{forced}
		}
		values, err := source.ListModelAvailabilityCandidates(ctx, group, platforms, grouped)
		if err != nil {
			return nil, err
		}
		out := make([]routing.AvailabilityProvider, len(values))
		for i := range values {
			record := &values[i]
			out[i] = routing.AvailabilityProvider{
				Platform: record.Platform,
				Supports: func(ctx context.Context, model string) bool {
					policy := ModelPolicy{Record: record}
					if !policy.AllowsProtocol(ctx, model) {
						return false
					}
					if record.IsQoderCosy() {
						// Missing catalog evidence is temporary unavailability, not proof
						// that the requested model is absent. Actual admission still rejects it.
						if _, err := record.QoderCatalog(time.Now()); err != nil {
							return true
						}
					}
					if compatible {
						return policy.SupportsCompatibleRouting(ctx, model)
					}
					return policy.Supports(ctx, model)
				},
			}
		}
		return out, nil
	}
	return result
}

// SupportsCompatibleRouting trims the candidate model before applying
// execution support rules. A blank model returns true even with a missing
// record; nonblank models require a record and its platform-specific support
// decision.
func (p ModelPolicy) SupportsCompatibleRouting(ctx context.Context, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	if p.Record == nil {
		return false
	}
	return p.Supports(ctx, model)
}

// GetConfiguredRequestModels returns provider-configured candidates using the
// shared platform model defaults.
func (r modelRejectionRules) GetConfiguredRequestModels() []string {
	return r.Record.GetConfiguredRequestModels(provideradapter.ModelDefaults())
}

// IsModelSupported delegates provider support checks using current defaults
// and platform rules.
func (r modelRejectionRules) IsModelSupported(model string) bool {
	return r.Record.IsModelSupported(model, provideradapter.ModelDefaults(), provideradapter.ModelRules(r.Record))
}

// ModelRejectionProvider binds a nonnil provider's platform and current model
// rules for routing diagnosis. Its default-model callback closes over that
// record; callers keep its configuration stable while rejection checks run.
func ModelRejectionProvider(value *provider.Record) routing.ModelRejectionSource {
	return routing.ModelRejectionSource{Platform: value.Platform, Rules: modelRejectionRules{value}, Defaults: func(platform string) ([]string, error) {
		return provideradapter.DefaultProviderModels(value), nil
	}}
}
