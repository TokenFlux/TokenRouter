//go:build unit

package provider

import (
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestGrokMediaCapabilityKeepsOnlyUnobservedOAuthAsProbeCandidate(t *testing.T) {
	unobserved := &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}
	eligible, reason := accountcore.GrokMediaGenerationEligibility(unobserved, GrokTierRules())
	require.False(t, eligible)
	require.Equal(t, "billing_unobserved", reason)
	require.True(t, SupportsOpenAIEndpoint(unobserved, accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))

	inconclusive := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
			Partial:    true,
		}},
	}
	require.False(t, SupportsOpenAIEndpoint(inconclusive, accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))
}
