//go:build integration

package routing_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/stretchr/testify/require"
)

func TestPricingConfigCRUDPreservesIndependentGroupPolicies(t *testing.T) {
	ctx := context.Background()
	client, db := routingDatabase(t)
	groups := newGroupStoreFixture(client, db)
	source := &routing.Group{
		Name: "price-policy-source", Platform: routing.PlatformOpenAI, Status: routing.StatusActive, RateMultiplier: 1,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(routing.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(routing.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
		RoutingPolicy: routing.GroupRoutingPolicy{
			Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceGroupMapped,
			ModelMapping: map[string]map[string]string{"openai": {"alias": "gpt-allowed"}}, AllowedModels: map[string][]string{"openai": {"gpt-allowed"}},
			FeaturesConfig: map[string]any{"codex_image_generation_bridge": map[string]any{"openai": false}},
		},
	}
	require.NoError(t, groups.Create(ctx, source))
	loaded, err := groups.GetByID(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, source.RoutingPolicy, loaded.RoutingPolicy)
	duplicate := routing.CloneGroupForDuplicate(loaded, "pricing-policy-copy")
	require.NoError(t, groups.CreateFromSource(ctx, duplicate, source.ID))
	copied, err := groups.GetByID(ctx, duplicate.ID)
	require.NoError(t, err)
	require.Equal(t, source.RoutingPolicy, copied.RoutingPolicy)
	copied.RoutingPolicy.RestrictionModelSource = routing.BillingModelSourceRequested
	require.NoError(t, groups.Update(ctx, copied))
	require.Equal(t, routing.BillingModelSourceGroupMapped, source.RoutingPolicy.RestrictionModelSource)

	store := postgres.NewPricingConfigStore(db)
	service := routing.NewPricingConfigService(store, nil, routing.PricingConfigOptions{ReadGroup: groups.GetByIDLite, LoadLocation: time.LoadLocation})
	zero := 0.0
	config, err := service.Create(ctx, &routing.CreatePricingConfigInput{Name: "shared-price", GroupIDs: []int64{source.ID, duplicate.ID}, ModelPricing: []routing.ModelPricingEntry{{Platform: "openai", Models: []string{"gpt-other"}, InputPrice: &zero, BillingMode: routing.BillingModeToken}}})
	require.NoError(t, err)
	require.Len(t, config.ModelPricing, 1)
	require.NotNil(t, config.ModelPricing[0].InputPrice)
	require.Zero(t, *config.ModelPricing[0].InputPrice)
	require.ElementsMatch(t, []int64{source.ID, duplicate.ID}, config.GroupIDs)
	check := func() {
		t.Helper()
		require.Equal(t, "gpt-allowed", service.ResolveGroupMapping(ctx, source.ID, "alias").MappedModel)
		require.False(t, service.IsModelRestricted(ctx, source.ID, "gpt-allowed"))
		require.True(t, service.IsModelRestricted(ctx, source.ID, "gpt-other"))
	}
	check()
	_, err = service.Update(ctx, config.ID, &routing.UpdatePricingConfigInput{Status: routing.StatusDisabled, BillingModelSource: routing.BillingModelSourceUpstream})
	require.NoError(t, err)
	check()
	require.NoError(t, service.Delete(ctx, config.ID))
	check()
	loaded, err = groups.GetByID(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, source.RoutingPolicy, loaded.RoutingPolicy)
}
