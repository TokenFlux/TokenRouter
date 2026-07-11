package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_SnapshotRoundTrip_PreservesGroupCaptureControls(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	groupID := int64(9)
	videoPrice480P := 0.08
	videoPrice720P := 0.14
	videoPrice1080P := 0.25
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
			ID:                      groupID,
			Name:                    "openai-images",
			Platform:                PlatformOpenAI,
			Status:                  StatusActive,
			RateMultiplier:          1,
			DataSharingEnabled:      true,
			SessionIsolationEnabled: true,
			AllowImageGeneration:    true,
			ImageRateIndependent:    true,
			ImageRateMultiplier:     0.5,
			VideoRateIndependent:    true,
			VideoRateMultiplier:     0.25,
			VideoPrice480P:          &videoPrice480P,
			VideoPrice720P:          &videoPrice720P,
			VideoPrice1080P:         &videoPrice1080P,
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.True(t, roundTrip.Group.DataSharingEnabled)
	require.True(t, roundTrip.Group.SessionIsolationEnabled)
	require.True(t, roundTrip.Group.AllowImageGeneration)
	require.True(t, roundTrip.Group.ImageRateIndependent)
	require.InDelta(t, 0.5, roundTrip.Group.ImageRateMultiplier, 1e-12)
	require.True(t, roundTrip.Group.VideoRateIndependent)
	require.InDelta(t, 0.25, roundTrip.Group.VideoRateMultiplier, 1e-12)
	require.InDelta(t, videoPrice480P, *roundTrip.Group.VideoPrice480P, 1e-12)
	require.InDelta(t, videoPrice720P, *roundTrip.Group.VideoPrice720P, 1e-12)
	require.InDelta(t, videoPrice1080P, *roundTrip.Group.VideoPrice1080P, 1e-12)
}
