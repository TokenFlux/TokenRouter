package selection

import (
	"context"
	"strings"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
)

// grok_team_rate_limit.go filters Grok candidates against team model cooldowns
// using their scheduling model names.
// Grok OAuth providers in one team share observed model cooldowns in the
// provider package. Filtering resolves each candidate's effort-aware scheduling
// model before the team check and compacts survivors into the incoming slice,
// preserving their relative order and shared record data.

// isGrokTeamModelRateLimited delegates the team and model cooldown lookup to
// provider rules. Nil, non-Grok-OAuth, and missing-team providers return false
// under those rules; now determines whether the observed deadline remains
// active.
func isGrokTeamModelRateLimited(provider *gatewayprovider.ExecutionProvider, model string, now time.Time) bool {
	return providercore.IsGrokTeamModelRateLimited(gatewayprovider.ExecutionRecord(provider), model, now)
}

// filterGrokTeamModelRateLimitedProviders removes providers whose team is
// cooling down for their mapped scheduling model under ctx. It reuses the input
// slice's backing array, so callers relinquish its prior contents; an entirely
// filtered nonempty pool returns nil.
func filterGrokTeamModelRateLimitedProviders(ctx context.Context, providers []gatewayprovider.ExecutionProvider, model string, now time.Time) []gatewayprovider.ExecutionProvider {
	if len(providers) == 0 || strings.TrimSpace(model) == "" {
		return providers
	}
	out := providers[:0]
	kept := false
	for i := range providers {
		upstreamModel := gatewayprovider.ExecutionModelPolicy(&providers[i]).WithContext(ctx).CanonicalSchedulingModel(model)
		if isGrokTeamModelRateLimited(&providers[i], upstreamModel, now) {
			continue
		}
		out = append(out, providers[i])
		kept = true
	}
	if !kept && len(out) == 0 {
		return nil
	}
	return out
}
