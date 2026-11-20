package selection

import (
	"context"
	"strings"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
)

// grok_model_quota_block.go filters providers whose mapped Grok model has an
// active quota block.
// A Grok quota observation can block one model while leaving the provider's
// other models available. Selection resolves the provider's effort-aware
// scheduling model before reading that block, so client aliases, wildcard
// mappings, and effort_model_mapping reach the cooldown's stored identity.

// filterGrokModelQuotaBlockedProviders returns candidates whose canonical
// scheduling model under ctx has no active quota block at now. Empty candidates
// or a blank model reuse the input slice; other results allocate slice storage
// while provider records retain their referenced data.
func filterGrokModelQuotaBlockedProviders(ctx context.Context, providers []gatewayprovider.ExecutionProvider, model string, now time.Time) []gatewayprovider.ExecutionProvider {
	if len(providers) == 0 || strings.TrimSpace(model) == "" {
		return providers
	}
	out := make([]gatewayprovider.ExecutionProvider, 0, len(providers))
	for i := range providers {
		upstreamModel := gatewayprovider.ExecutionModelPolicy(&providers[i]).WithContext(ctx).CanonicalSchedulingModel(model)
		if providercore.IsGrokModelQuotaBlocked(providers[i].Record.ID, upstreamModel, now) {
			continue
		}
		out = append(out, providers[i])
	}
	return out
}
