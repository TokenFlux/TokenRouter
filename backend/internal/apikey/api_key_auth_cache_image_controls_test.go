package apikey_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_SnapshotRoundTrip_PreservesGroupCaptureControls(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()
	groupID := int64(9)
	videoPrice480P := 0.08
	videoPrice720P := 0.14
	videoPrice1080P := 0.25
	stickyWeighted := false
	lbTopK := 3
	apiKey := &apikey.APIKey{
		ID:      1,
		UserID:  2,
		GroupID: &groupID,
		Key:     "k-images-roundtrip",
		Status:  billing.StatusActive,
		User: &identity.User{
			ID:          2,
			Status:      billing.StatusActive,
			Role:        identity.RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &routing.Group{
			ID:            groupID,
			Name:          "openai-images",
			Platform:      capability.PlatformOpenAI,
			SchedulerType: routing.GroupSchedulerTypeAdvanced,
			AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
				StickyWeightedEnabled: &stickyWeighted,
				LBTopK:                &lbTopK,
			},
			Status:                  billing.StatusActive,
			RateMultiplier:          1,
			SessionIsolationEnabled: true,
			AllowImageGeneration:    true,
			ModelPricing: []routing.ChannelModelPricing{{Models: []string{"*"}, BillingMode: routing.BillingModeVideo, Intervals: []routing.PricingInterval{
				{TierLabel: "480p", PerRequestPrice: &videoPrice480P},
				{TierLabel: "720p", PerRequestPrice: &videoPrice720P},
				{TierLabel: "1080p", PerRequestPrice: &videoPrice1080P},
			}}},
		},
	}

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.KeySnapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, routing.GroupSchedulerTypeAdvanced, roundTrip.Group.SchedulerType)
	require.NotNil(t, roundTrip.Group.AdvancedSchedulerOverrides.StickyWeightedEnabled)
	require.False(t, *roundTrip.Group.AdvancedSchedulerOverrides.StickyWeightedEnabled)
	require.Equal(t, 3, *roundTrip.Group.AdvancedSchedulerOverrides.LBTopK)
	require.NotSame(t, apiKey.Group.AdvancedSchedulerOverrides.LBTopK, roundTrip.Group.AdvancedSchedulerOverrides.LBTopK)
	require.True(t, roundTrip.Group.SessionIsolationEnabled)
	require.True(t, roundTrip.Group.AllowImageGeneration)
	require.Equal(t, apiKey.Group.ModelPricing, roundTrip.Group.ModelPricing)
	require.NotSame(t, apiKey.Group.ModelPricing[0].Intervals[0].PerRequestPrice, roundTrip.Group.ModelPricing[0].Intervals[0].PerRequestPrice)
}
