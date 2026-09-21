//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestGrokMediaCapabilityKeepsOnlyUnobservedOAuthAsProbeCandidate(t *testing.T) {
	unobserved := &Account{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}
	eligible, reason := unobserved.GrokMediaGenerationEligibility()
	require.False(t, eligible)
	require.Equal(t, "billing_unobserved", reason)
	require.True(t, unobserved.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))

	inconclusive := &Account{
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{accountcore.GrokUsageBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
			Partial:    true,
		}},
	}
	require.False(t, inconclusive.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))
}

func TestGrokMediaCapabilityFiltersOnlyGeneration(t *testing.T) {
	account := &Account{
		ID:          1,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra:       map[string]any{accountcore.GrokMediaEligibleExtraKey: false},
	}

	require.True(t, account.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityTextGeneration))
	require.False(t, account.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(
		context.Background(), account, capability.PlatformGrok, "grok-imagine-video", false,
		accountcore.OpenAIEndpointCapabilityGrokMediaGeneration,
	))
}
