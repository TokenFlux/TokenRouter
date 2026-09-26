package apikey

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// 旧快照必须经过版本门禁拒绝；新字段的序列化对照使用明确转换的测试报文。
func TestV41SnapshotRetainsPreviousFields(t *testing.T) {
	for _, kind := range []string{"full", "empty"} {
		t.Run(kind, func(t *testing.T) {
			original, e := os.ReadFile("testdata/v40-" + kind + ".json")
			require.NoError(t, e)
			var entry APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(original, &entry))
			require.Equal(t, 40, entry.Snapshot.Version)
			cached, used, err := new(APIKeyService).KeyApplyAuthCacheEntry("legacy-v40", &entry)
			require.NoError(t, err)
			require.False(t, used)
			require.Nil(t, cached)
			var current map[string]any
			require.NoError(t, json.Unmarshal(original, &current))
			migrateSnapshotPricingFields(current)
			migrated, err := json.Marshal(current)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(migrated, &entry))
			actual, e := json.Marshal(entry)
			require.NoError(t, e)
			var expected, decoded map[string]any
			require.NoError(t, json.Unmarshal(migrated, &expected))
			require.NoError(t, json.Unmarshal(actual, &decoded))
			var stripPolicy func(any)
			stripPolicy = func(value any) {
				switch v := value.(type) {
				case map[string]any:
					delete(v, "routing_policy")
					for _, child := range v {
						stripPolicy(child)
					}
				case []any:
					for _, child := range v {
						stripPolicy(child)
					}
				}
			}
			stripPolicy(decoded)
			require.Equal(t, expected, decoded)
		})
	}
}

// migrateSnapshotPricingFields 只转换测试期望，生产代码始终重新加载过期的认证快照。
func migrateSnapshotPricingFields(value any) {
	switch node := value.(type) {
	case map[string]any:
		if id, ok := node["channel_id"]; ok {
			node["pricing_config_id"] = id
			delete(node, "channel_id")
		}
		if _, ok := node["version"]; ok {
			node["version"] = KeyApiKeyAuthSnapshotVersion
		}
		for _, child := range node {
			migrateSnapshotPricingFields(child)
		}
	case []any:
		for _, child := range node {
			migrateSnapshotPricingFields(child)
		}
	}
}
