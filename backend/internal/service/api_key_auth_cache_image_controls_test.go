package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_SnapshotRoundTrip_PreservesImageGenerationControls(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	groupID := int64(9)
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
			ID:                   groupID,
			Name:                 "openai-images",
			Platform:             PlatformOpenAI,
			Status:               StatusActive,
			RateMultiplier:       1,
			AllowImageGeneration: true,
			ImageRateIndependent: true,
			ImageRateMultiplier:  0.5,
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.True(t, roundTrip.Group.AllowImageGeneration)
	require.True(t, roundTrip.Group.ImageRateIndependent)
	require.InDelta(t, 0.5, roundTrip.Group.ImageRateMultiplier, 1e-12)
}
