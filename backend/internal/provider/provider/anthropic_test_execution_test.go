package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// anthropic_test_execution_test.go checks sampling fields for the final probe
// model across credential families. The controlled lookup verifies group
// forwarding and catalogue conditions while payload assertions protect the
// unrelated output token limit.

// TestAnthropicProbeUsesServiceSamplingCapabilities resolves sampling across
// API-key, OAuth, Vertex, and Bedrock model identities. Temperature presence
// must follow service capability while max_tokens remains unchanged and the
// catalogue view is read once.
func TestAnthropicProbeUsesServiceSamplingCapabilities(t *testing.T) {
	for _, tc := range []struct {
		// kind: provider authentication type; model: selected service model identity.
		kind, model string
		sampling    bool // expected temperature field presence
	}{
		{"apikey", "claude-opus-4-7", false},
		{"oauth", "claude-opus-4-7", false},
		{"service_account", "claude-opus-4-7@default", false},
		{"bedrock", "us.anthropic.claude-opus-4-7", false},
		{"apikey", "claude-sonnet-4-5", true},
		{"apikey", "custom-unknown", true},
	} {
		t.Run(tc.kind+"/"+tc.model, func(t *testing.T) {
			reads := 0
			executor := AnthropicProviderTest{Attributes: func(_ context.Context, value *providercore.Record, groupID int64, model, protocol string) (modelrules.Resolution, error) {
				reads++
				require.Equal(t, int64(59), groupID)
				return testkit.OfflineView().Resolve(value.ModelAttributeTarget(model, protocol), modelcatalog.Attributes{}), nil
			}}
			payload := map[string]any{"temperature": 1, "max_tokens": 256}
			require.NoError(t, executor.applyModelAttributes(t.Context(), &providercore.Record{Platform: "anthropic", Type: tc.kind}, 59, tc.model, payload))
			_, present := payload["temperature"]
			require.Equal(t, tc.sampling, present)
			require.Equal(t, 256, payload["max_tokens"])
			require.Equal(t, 1, reads)
		})
	}
}
