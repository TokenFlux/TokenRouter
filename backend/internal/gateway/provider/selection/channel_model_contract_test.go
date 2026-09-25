package selection_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis 验证长连接每轮都严格按所选依据检查 R、C 或 U。
func TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis(t *testing.T) {
	price := 0.01
	tests := []struct {
		name           string
		billingSource  string
		pricingModel   string
		expectRejected bool
	}{
		{name: "requested", billingSource: routing.BillingModelSourceRequested, pricingModel: "client-alias"},
		{name: "channel_mapped", billingSource: routing.BillingModelSourceChannelMapped, pricingModel: "channel-model"},
		{name: "upstream", billingSource: routing.BillingModelSourceUpstream, pricingModel: "upstream-model"},
		{name: "upstream_rejected", billingSource: routing.BillingModelSourceUpstream, pricingModel: "other-model", expectRejected: true},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(4300 + index)
			channel := routing.Channel{
				ID:                 int64(80 + index),
				Status:             billing.StatusActive,
				RestrictModels:     true,
				BillingModelSource: tt.billingSource,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": "channel-model"},
				},
				ModelPricing: []routing.ChannelModelPricing{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := selection.NewCompatible(selection.CompatibleDependencies{Shared: selection.Shared{Channels: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)}}, selection.DefaultOptions())
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 90,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"channel-model": "upstream-model"},
					"model_whitelist": []any{"upstream-model"},
				}},
			}

			routingModel, err := svc.ResolveOpenAIWSRoutingModelForAccount(
				context.Background(), &groupID, account, "client-alias", accountcore.OpenAIEndpointCapabilityTextGeneration,
			)
			if tt.expectRejected {
				require.Error(t, err)
				require.Empty(t, routingModel)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "channel-model", routingModel)
		})
	}
}

// TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel 验证后续 turn 不能绕过固定账号的最终白名单。
func TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 91,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"different-upstream-model"},
		}},
	}
	svc := selection.NewCompatible(selection.CompatibleDependencies{}, selection.DefaultOptions())

	routingModel, err := svc.ResolveOpenAIWSRoutingModelForAccount(
		context.Background(), nil, account, "channel-model", accountcore.OpenAIEndpointCapabilityTextGeneration,
	)
	require.Error(t, err)
	require.Empty(t, routingModel)
}
