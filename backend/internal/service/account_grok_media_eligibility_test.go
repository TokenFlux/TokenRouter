//go:build unit

package service

import (
	"context"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

func TestGrokMediaCapabilityFiltersOnlyGeneration(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra:       map[string]any{accountcore.GrokMediaEligibleExtraKey: false}},
	}

	require.True(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(account), accountcore.OpenAIEndpointCapabilityTextGeneration))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(account), accountcore.OpenAIEndpointCapabilityGrokMediaGeneration))
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(
		context.Background(), account, capability.PlatformGrok, "grok-imagine-video", false,
		accountcore.OpenAIEndpointCapabilityGrokMediaGeneration,
	))
}
