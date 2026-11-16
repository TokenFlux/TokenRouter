package apikey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// access_context_test.go verifies request-context storage and copy-on-write
// updates for per-Key Claude one-hour cache overrides on AccessSnapshot.
// Derived contexts keep earlier snapshots unchanged while runtime credential
// replacement preserves existing Key-level policy flags on matching Key IDs.

// TestAccessSnapshotForceClaude1hCacheContextRoundTrip writes and replaces the
// Claude one-hour cache override across absent, populated, and runtime-replaced
// request contexts. It checks derived contexts isolate mutations from earlier
// snapshots and WithRuntimeAPIKey preserves the override when refreshing a
// matching credential view.
func TestAccessSnapshotForceClaude1hCacheContextRoundTrip(t *testing.T) {
	emptyAccess, ok := AccessSnapshotFromContext(context.Background())
	require.False(t, ok)
	require.False(t, emptyAccess.ForceClaude1hCache())

	seedCtx := WithForceClaude1hCache(context.Background(), true)
	seeded, ok := AccessSnapshotFromContext(seedCtx)
	require.True(t, ok)
	require.True(t, seeded.ForceClaude1hCache())

	teamID := int64(5)
	baseCtx := WithAccessSnapshot(context.Background(), AccessSnapshot{
		KeyID:              42,
		OwnerUserID:        7,
		PayerUserID:        7,
		ActorUserID:        7,
		TeamID:             &teamID,
		fastModePolicy:     APIKeyFastModePolicyForceOn,
		forceClaude1hCache: true,
	})
	disabledCtx := WithForceClaude1hCache(baseCtx, false)

	baseSnapshot, ok := AccessSnapshotFromContext(baseCtx)
	require.True(t, ok)
	require.True(t, baseSnapshot.ForceClaude1hCache())

	disabledSnapshot, ok := AccessSnapshotFromContext(disabledCtx)
	require.True(t, ok)
	require.False(t, disabledSnapshot.ForceClaude1hCache())

	runtimeCtx := WithRuntimeAPIKey(baseCtx, &APIKey{
		ID:   42,
		User: &User{ID: 99},
	})
	runtimeSnapshot, ok := AccessSnapshotFromContext(runtimeCtx)
	require.True(t, ok)
	require.Equal(t, int64(99), runtimeSnapshot.PayerUserID)
	require.Equal(t, APIKeyFastModePolicyForceOn, runtimeSnapshot.FastModePolicy())
	require.True(t, runtimeSnapshot.ForceClaude1hCache())
}
