package account

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// 索引必须独立持有和返回凭据值，调用方修改不能污染后续匹配。
func TestCodexImportIndexSnapshotIsolation(t *testing.T) {
	original := Record{ID: 7, Credentials: map[string]any{"access_token": "token", "refresh_token": "refresh", "chatgpt_account_id": "team", "nested": map[string]any{"value": "original"}}}
	index := BuildCodexAccountIndex([]Record{original})
	nested, ok := original.Credentials["nested"].(map[string]any)
	require.True(t, ok)
	nested["value"] = "outside"
	first, key := index.Find([]string{"account:team"}, "")
	require.NotNil(t, first)
	require.Equal(t, "account:team", key)
	copy, ok := first.Credentials["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "original", copy["value"])
	copy["value"] = "response"
	second, _ := index.Find([]string{"account:team"}, "")
	require.NotNil(t, second)
	again, ok := second.Credentials["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "original", again["value"])
}
