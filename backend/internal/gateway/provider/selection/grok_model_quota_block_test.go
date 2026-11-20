package selection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestGrokModelQuotaBlock_FiltersOnlyNamedModel records a quota block for one
// Grok model on one provider. Filtering removes that candidate while a
// different model and another provider remain eligible.
func TestGrokModelQuotaBlock_FiltersOnlyNamedModel(t *testing.T) {
	id := time.Now().UnixNano()%1_000_000 + 5000
	providercore.MarkGrokModelQuotaBlock(id, "grok-4.5", time.Now().Add(time.Hour))
	now := time.Now()
	require.True(t, providercore.IsGrokModelQuotaBlocked(id, "grok-4.5", now))
	require.False(t, providercore.IsGrokModelQuotaBlocked(id, "grok-4.3", now))

	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: id + 1, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
	}
	filtered := filterGrokModelQuotaBlockedProviders(context.Background(), providers, "grok-4.5", now)
	require.Len(t, filtered, 1)
	require.Equal(t, id+1, filtered[0].Record.ID)
}

// TestGrokModelQuotaBlockFiltersMappedUpstreamModel maps a client wildcard to a
// blocked Grok upstream model. Selection excludes the alias through its final
// scheduling identity, preventing a mapping from bypassing the block.
func TestGrokModelQuotaBlockFiltersMappedUpstreamModel(t *testing.T) {
	id := time.Now().UnixNano()%1_000_000 + 7000
	providercore.MarkGrokModelQuotaBlock(id, "grok-4.5", time.Now().Add(time.Hour))
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-*": "grok-4.5"},
			},
		},
	}

	require.Empty(t, filterGrokModelQuotaBlockedProviders(context.Background(), []gatewayprovider.ExecutionProvider{provider}, "gpt-5", time.Now()))

	// Effort-scoped Grok mapping must resolve against the active effort in ctx so
	// a quota block on the high-effort target filters only high-effort requests.
	effortID := id + 10
	providercore.MarkGrokModelQuotaBlock(effortID, "grok-4.5-high", time.Now().Add(time.Hour))
	effortProvider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: effortID,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				providercore.EffortModelMappingCredentialKey: map[string]any{
					"grok-4.5": map[string]any{"high": "grok-4.5-high"},
				},
			},
		},
	}
	require.Len(t, filterGrokModelQuotaBlockedProviders(context.Background(), []gatewayprovider.ExecutionProvider{effortProvider}, "grok-4.5", time.Now()), 1)
	highCtx := requeststate.WithRoutingReasoningEffort(context.Background(), "high")
	require.Empty(t, filterGrokModelQuotaBlockedProviders(highCtx, []gatewayprovider.ExecutionProvider{effortProvider}, "grok-4.5", time.Now()))
}
