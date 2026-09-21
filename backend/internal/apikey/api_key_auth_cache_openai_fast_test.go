package apikey_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestAPIKeyAuthSnapshotGroupForceOpenAIFastRoundtrip 验证组级 Fast 开关经序列化缓存后仍能恢复到可信分组上下文。
func TestAPIKeyAuthSnapshotGroupForceOpenAIFastRoundtrip(t *testing.T) {
	groupID := int64(50)
	apiKey := &apikey.APIKey{
		ID: 82, UserID: 40, GroupID: &groupID, Key: "sk-fast-roundtrip", Status: billing.StatusActive,
		User: &identity.User{ID: 40, Status: billing.StatusActive},
		Group: &routing.Group{
			ID: groupID, Name: "fast-roundtrip", Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
			Hydrated: true, ForceOpenAIFast: true, FreeOpenAIFast: true,
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	payload, err := json.Marshal(&apikey.APIKeyAuthCacheEntry{Snapshot: svc.KeySnapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached apikey.APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.KeyApplyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.True(t, materialized.Group.Hydrated)
	require.True(t, materialized.Group.ForceOpenAIFast)
	require.True(t, materialized.Group.FreeOpenAIFast)
	require.Equal(t, apikey.KeyApiKeyAuthSnapshotVersion, cached.Snapshot.Version)
}

// Ultra Fast 和关闭策略经序列化后必须恢复为相同的可信分组配置。
func TestAuthSnapshotGroupOpenAIFastPolicy(t *testing.T) {
	for _, policy := range []string{"force_ultrafast", "force_off"} {
		key := &apikey.APIKey{ID: 1, UserID: 2, Status: billing.StatusActive, User: &identity.User{ID: 2, Status: billing.StatusActive}, Group: &routing.Group{ID: 3, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Hydrated: true, OpenAIFastPolicy: policy}}
		svc := newAPIKeyTestService(apiKeyTestDependencies{})
		payload, err := json.Marshal(&apikey.APIKeyAuthCacheEntry{Snapshot: svc.KeySnapshotFromAPIKey(context.Background(), key)})
		require.NoError(t, err)
		var entry apikey.APIKeyAuthCacheEntry
		require.NoError(t, json.Unmarshal(payload, &entry))
		result, used, err := svc.KeyApplyAuthCacheEntry("sk-test", &entry)
		require.NoError(t, err)
		require.True(t, used)
		require.Equal(t, policy, result.Group.OpenAIFastPolicy)
	}
}
