package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIKeyAuthSnapshotGroupForceOpenAIFastRoundtrip 验证组级 Fast 开关经序列化缓存后仍能恢复到可信分组上下文。
func TestAPIKeyAuthSnapshotGroupForceOpenAIFastRoundtrip(t *testing.T) {
	groupID := int64(50)
	apiKey := &APIKey{
		ID: 82, UserID: 40, GroupID: &groupID, Key: "sk-fast-roundtrip", Status: StatusActive,
		User: &User{ID: 40, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "fast-roundtrip", Platform: PlatformOpenAI, Status: StatusActive,
			Hydrated: true, ForceOpenAIFast: true, FreeOpenAIFast: true,
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.True(t, materialized.Group.Hydrated)
	require.True(t, materialized.Group.ForceOpenAIFast)
	require.True(t, materialized.Group.FreeOpenAIFast)
	require.Equal(t, apiKeyAuthSnapshotVersion, cached.Snapshot.Version)
}

// Ultra Fast 和关闭策略经序列化后必须恢复为相同的可信分组配置。
func TestAuthSnapshotGroupOpenAIFastPolicy(t *testing.T) {
	for _, policy := range []string{"force_ultrafast", "force_off"} {
		key := &APIKey{ID: 1, UserID: 2, Status: StatusActive, User: &User{ID: 2, Status: StatusActive}, Group: &Group{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, OpenAIFastPolicy: policy}}
		svc := &APIKeyService{}
		payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), key)})
		require.NoError(t, err)
		var entry APIKeyAuthCacheEntry
		require.NoError(t, json.Unmarshal(payload, &entry))
		result, used, err := svc.applyAuthCacheEntry("sk-test", &entry)
		require.NoError(t, err)
		require.True(t, used)
		require.Equal(t, policy, result.Group.OpenAIFastPolicy)
	}
}
