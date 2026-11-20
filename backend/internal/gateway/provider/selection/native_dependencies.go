package selection

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// native_dependencies.go adapts provider snapshots and exposes the shared
// services used during selection.
// Selection keeps provider credentials in gateway execution targets while
// scheduler inputs carry metadata. Snapshot reads use the gateway constructors
// to recover full targets, and accessors expose the app-bound block stores,
// response state, parameters, and platform model rules needed at each call
// site.

// modelRejectionSources converts candidates to model-rejection inputs for routing.
func modelRejectionSources(providers []gatewayprovider.ExecutionProvider) []routing.ModelRejectionSource {
	sources := make([]routing.ModelRejectionSource, len(providers))
	for i := range providers {
		value := &providers[i]
		sources[i] = gatewayprovider.ModelRejectionProvider(gatewayprovider.ExecutionRecord(value))
	}
	return sources
}

// readSnapshotProvider decodes one cached provider and wraps its record as an
// execution target. A nil cached record remains nil, and the source error
// accompanies the converted target for the selection caller.
func readSnapshotProvider(ctx context.Context, source Snapshots, id int64) (*gatewayprovider.ExecutionProvider, error) {
	value, err := source.GetProvider(ctx, id)
	return gatewayprovider.NewExecutionProvider(value), err
}

// readSnapshotProviders decodes the source's candidate records into gateway
// execution values. Nil and empty slice distinctions, the mixed-scheduling
// flag, and the source error pass through to the selector consuming the result.
func readSnapshotProviders(ctx context.Context, source Snapshots, group *int64, platform string, forced bool) ([]gatewayprovider.ExecutionProvider, bool, error) {
	values, mixed, err := source.ListProviders(ctx, group, platform, forced)
	return gatewayprovider.ExecutionProviders(values), mixed, err
}

// debugModelRoutingEnabled reports whether startup options request
// model-routing diagnostics for this selector. A nil selector returns false,
// letting optional diagnostic callers skip detailed routing output when no
// instance is bound.
func (s *Generic) debugModelRoutingEnabled() bool { return s != nil && s.options.DebugRouting }

// windowCostGuard returns the window guard bound when the generic selector was
// constructed.
func (s *Generic) windowCostGuard() *billing.WindowCostGuard { return s.window }

// runtimeBlockState returns the compatible selector's bound credential failure
// observation store.
func (s *Compatible) runtimeBlockState() *providercore.RuntimeBlockState { return s.runtime }

// getOpenAIProviderModelTransientState returns the selector's bound
// provider-model cooldown store. A nil selector returns nil so callers can skip
// optional cooldown observations when no compatible runtime is available.
func (s *Compatible) getOpenAIProviderModelTransientState() *providercore.ModelTransientState {
	if s == nil {
		return nil
	}
	return s.modelTransient
}

// getOpenAIProxyStreamCircuit returns the bound stream-quarantine circuit for
// compatible selection. A nil selector returns nil, allowing callers that
// already guard optional circuit access to handle an unavailable runtime.
func (s *Compatible) getOpenAIProxyStreamCircuit() *egress.ProxyStreamCircuit {
	if s == nil {
		return nil
	}
	return s.proxyCircuit
}

// ResponseStateStore returns the selector's shared OpenAI response state store.
// A nil selector returns nil; callers using continuation bindings handle an
// unavailable store before looking up response ownership.
func (s *Compatible) ResponseStateStore() session.OpenAIWSStateStore {
	if s == nil {
		return nil
	}
	return s.responseState
}

// OpenAIHTTPResponseStickyTTL returns the positive configured response-binding
// lifetime when supplied. A nil selector or nonpositive configured value uses
// one hour, as required by continuation-binding callers refreshing response
// affinity.
func (s *Compatible) OpenAIHTTPResponseStickyTTL() time.Duration {
	if s != nil && s.options.ResponseTTL > 0 {
		return s.options.ResponseTTL
	}
	return time.Hour
}

// stickyStats returns the constructed selector's hash-migration counters for
// sticky operations. A nil selector gets a fresh empty counter set, so
// incidental fallback observations do not retain shared runtime state.
func (s *Compatible) stickyStats() *schedulercore.StickyStats {
	if s == nil {
		return &schedulercore.StickyStats{}
	}
	return s.stickyMetrics
}

// resolveProviderUpstreamModel delegates final upstream identity resolution to
// the execution target's model policy.
func resolveProviderUpstreamModel(ctx context.Context, value *gatewayprovider.ExecutionProvider, model string) string {
	return gatewayprovider.ExecutionModelPolicy(value).UpstreamModel(ctx, model)
}

// defaultWindowCostGuard constructs the default Anthropic window-cost guard for
// generic selection. It has no external cache or usage reader, uses the shared
// billing metrics, and sends logs through gateway diagnostics.
func defaultWindowCostGuard() *billing.WindowCostGuard {
	return billing.NewWindowCostGuard(nil, nil, billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) }, Debug: slog.Debug})
}
