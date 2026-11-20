package provider

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Routing snapshots expose candidate metadata while credential values remain
// in the source record. The fixture serializes a snapshot and mutates its
// protocol and pointer fields, checking the visible payload and source values
// separately.

// TestProviderRoutingSnapshotExcludesCredentialsAndCopies captures a provider
// with credential and Extra markers. The payload must exclude those markers,
// and protocol, parent, and expiry mutations must leave the asserted source values intact.
func TestProviderRoutingSnapshotExcludesCredentialsAndCopies(t *testing.T) {
	parent := int64(2)
	expires := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	r := &Record{
		ID: 1, Platform: PlatformOpenAI, Type: ProviderTypeAPIKey, ParentProviderID: &parent, ExpiresAt: &expires,
		Credentials: map[string]any{"api_key": "private-token-marker", UpstreamProtocolsKey: []string{"openai_responses"}},
		Extra:       map[string]any{"unknown-secret": "private-extra-marker"},
	}
	snapshot := r.RoutingSnapshot()
	require.Equal(t, []capability.ProtocolID{capability.ProtocolOpenAIResponses}, snapshot.EnabledProtocols)
	payload, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-")
	require.NotContains(t, string(payload), "Credentials")
	*snapshot.ParentProviderID = 3
	*snapshot.ExpiresAt = expires.Add(time.Hour)
	snapshot.EnabledProtocols[0] = capability.ProtocolOpenAIChatCompletions
	require.Equal(t, int64(2), parent)
	require.Equal(t, 0, expires.Hour())
	require.Equal(t, []capability.ProtocolID{capability.ProtocolOpenAIResponses}, r.UpstreamProtocols())
}

// TestProviderSnapshotResolveModelRouteForEffort builds a snapshot with
// effort_model_mapping and per-model protocols. Effort resolution must select
// the suffixed upstream model, flag EffortMappingMatched, and honor per-model
// protocol declarations while falling back to ordinary mapping on empty effort.
func TestProviderSnapshotResolveModelRouteForEffort(t *testing.T) {
	r := &Record{
		ID: 42, Platform: PlatformAnthropic, Type: ProviderTypeAPIKey,
		Credentials: map[string]any{
			UpstreamProtocolsKey: []string{"anthropic_messages"},
			"model_mapping":      map[string]any{"claude-opus-5-5": "claude-opus-5-5-medium"},
			EffortModelMappingCredentialKey: map[string]any{
				"claude-opus-5-5": map[string]any{
					"high": "claude-opus-5-5-high",
				},
			},
		},
	}
	snapshot := r.RoutingSnapshot()
	route, ok := snapshot.ResolveModelRouteForEffort("claude-opus-5-5", "high", capability.ProtocolAnthropicMessages, nil)
	require.True(t, ok)
	require.Equal(t, "claude-opus-5-5-high", route.Model)
	require.True(t, route.MappingMatched)
	require.True(t, route.EffortMappingMatched)

	fallback, ok := snapshot.ResolveModelRouteForEffort("claude-opus-5-5", "", capability.ProtocolAnthropicMessages, nil)
	require.True(t, ok)
	require.Equal(t, "claude-opus-5-5-medium", fallback.Model)
	require.True(t, fallback.MappingMatched)
	require.False(t, fallback.EffortMappingMatched)
}
