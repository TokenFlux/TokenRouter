package selection

import (
	"context"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// generic_support.go reads and updates generic session bindings and checks
// whether retained affinity should be cleared.
// Sticky billing and provider selection can reuse a prefetched group binding
// when it belongs to the current request. Provider unschedulability and an
// active model cooldown decide whether a binding should be cleared, while cache
// operations use the shared generic session lifetime.

// stickySessionTTL controls the generic session binding lifetime supplied to
// the shared sticky cache.
const stickySessionTTL = time.Hour

// providerWithLoad pairs a provider with its load snapshot for selection.
type providerWithLoad struct {
	// provider and loadInfo are observations for the same candidate during one
	// selection pass; a missing loadInfo lets scheduler rules supply their missing-load behavior.
	provider *gatewayprovider.ExecutionProvider // candidate execution target
	loadInfo *schedulercore.ProviderLoadInfo    // observed candidate load
}

// shortSessionHash returns at most eight bytes for bounded session diagnostics.
// Empty input remains empty and short inputs pass through; callers supply the
// encoded session hash when logging the returned prefix.
func shortSessionHash(sessionHash string) string {
	if sessionHash == "" {
		return ""
	}
	if len(sessionHash) <= 8 {
		return sessionHash
	}
	// Bound diagnostic output to the hash prefix.
	return sessionHash[:8]
}

// derefGroupID returns the supplied group identifier or zero for a nil pointer.
// Sticky-cache callers use that zero as their missing-group namespace input;
// membership predicates separately require a positive group.
func derefGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}

// prefetchedStickyProviderIDFromContext returns a positive prefetched provider
// belonging to the requested group. Missing group or provider hints, mismatched
// group IDs, and captured nonpositive results return zero for normal cache
// lookup.
func prefetchedStickyProviderIDFromContext(ctx context.Context, groupID *int64) int64 {
	prefetchedGroupID, ok := requeststate.PrefetchedStickyGroupIDFromContext(ctx)
	if !ok || prefetchedGroupID != derefGroupID(groupID) {
		return 0
	}
	if providerID, ok := requeststate.PrefetchedStickyProviderIDFromContext(ctx); ok && providerID > 0 {
		return providerID
	}
	return 0
}

// shouldClearStickySession reports whether provider unschedulability or an
// active model cooldown invalidates a binding. A nil provider returns false for
// separate missing-record handling, and cooldown duration is checked against
// the provider's model policy using ctx so effort- and thinking-scoped model
// routes inspect the active route's cooldown window.
func shouldClearStickySession(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	if provider == nil {
		return false
	}
	if !provider.View().IsSchedulable() {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if remaining := gatewayprovider.ExecutionModelPolicy(provider).LimitRemaining(ctx, requestedModel); remaining > 0 {
		return true
	}
	return false
}

// BindStickySession writes the provider into the generic group-scoped session
// cache for one hour. Empty sessions, nonpositive provider IDs, or an
// unavailable cache are ignored; cache write errors propagate to the caller.
func (s *Generic) BindStickySession(ctx context.Context, groupID *int64, sessionHash string, providerID int64) error {
	if sessionHash == "" || providerID <= 0 || s.cache == nil {
		return nil
	}
	return s.cache.SetSessionProviderID(ctx, derefGroupID(groupID), sessionHash, providerID, stickySessionTTL)
}

// GetCachedSessionProviderID reads the generic group's provider binding for
// sessionHash. Empty session input or an absent cache returns zero without
// error; a cache failure returns zero and the read error.
func (s *Generic) GetCachedSessionProviderID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	if sessionHash == "" || s.cache == nil {
		return 0, nil
	}
	providerID, err := s.cache.GetSessionProviderID(ctx, derefGroupID(groupID), sessionHash)
	if err != nil {
		return 0, err
	}
	return providerID, nil
}
