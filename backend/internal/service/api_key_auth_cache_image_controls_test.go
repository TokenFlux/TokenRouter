package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_SnapshotRoundTrip_PreservesGroupCaptureControls(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()
	groupID := int64(9)
	videoPrice480P := 0.08
	videoPrice720P := 0.14
	videoPrice1080P := 0.25
	stickyWeighted := false
	lbTopK := 3
	apiKey := &APIKey{
		ID:      1,
		UserID:  2,
		GroupID: &groupID,
		Key:     "k-images-roundtrip",
		Status:  StatusActive,
		User: &User{
			ID:          2,
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &Group{
			ID:            groupID,
			Name:          "openai-images",
			Platform:      PlatformOpenAI,
			SchedulerType: GroupSchedulerTypeAdvanced,
			AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
				StickyWeightedEnabled: &stickyWeighted,
				LBTopK:                &lbTopK,
			},
			Status:                  StatusActive,
			RateMultiplier:          1,
			SessionIsolationEnabled: true,
			AllowImageGeneration:    true,
			ModelPricing:            testVideoModelPricing(map[string]*float64{"480p": &videoPrice480P, "720p": &videoPrice720P, "1080p": &videoPrice1080P}),
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, GroupSchedulerTypeAdvanced, roundTrip.Group.SchedulerType)
	require.NotNil(t, roundTrip.Group.AdvancedSchedulerOverrides.StickyWeightedEnabled)
	require.False(t, *roundTrip.Group.AdvancedSchedulerOverrides.StickyWeightedEnabled)
	require.Equal(t, 3, *roundTrip.Group.AdvancedSchedulerOverrides.LBTopK)
	require.NotSame(t, apiKey.Group.AdvancedSchedulerOverrides.LBTopK, roundTrip.Group.AdvancedSchedulerOverrides.LBTopK)
	require.True(t, roundTrip.Group.SessionIsolationEnabled)
	require.True(t, roundTrip.Group.AllowImageGeneration)
	require.Equal(t, apiKey.Group.ModelPricing, roundTrip.Group.ModelPricing)
	require.NotSame(t, apiKey.Group.ModelPricing[0].Intervals[0].PerRequestPrice, roundTrip.Group.ModelPricing[0].Intervals[0].PerRequestPrice)
}
