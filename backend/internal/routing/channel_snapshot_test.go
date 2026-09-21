//go:build unit

package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 缓存副本中的任意 JSON 数组/对象修改都不能污染已发布快照。
func TestS06ChannelNestedSnapshotIsolation(t *testing.T) {
	source := &Channel{FeaturesConfig: map[string]any{"extension": []any{map[string]any{"enabled": true}, []any{"original"}}}}
	copied := source.Clone()
	values, ok := copied.FeaturesConfig["extension"].([]any)
	require.True(t, ok)
	object, ok := values[0].(map[string]any)
	require.True(t, ok)
	array, ok := values[1].([]any)
	require.True(t, ok)
	object["enabled"] = false
	array[0] = "changed"
	originalValues, ok := source.FeaturesConfig["extension"].([]any)
	require.True(t, ok)
	originalObject, ok := originalValues[0].(map[string]any)
	require.True(t, ok)
	originalArray, ok := originalValues[1].([]any)
	require.True(t, ok)
	require.Equal(t, true, originalObject["enabled"])
	require.Equal(t, "original", originalArray[0])
}

// 发布快照后输入仍由存储调用者拥有，后续修改不得改变缓存值或平台路由。
func TestS06ChannelPublicationOwnsSnapshot(t *testing.T) {
	channels := []Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{9}, FeaturesConfig: map[string]any{"enabled": true}}}
	platforms := map[int64]string{9: PlatformOpenAI}
	cache := populateChannelCache(channels, platforms)
	channels[0].FeaturesConfig["enabled"] = false
	channels[0].Status = "disabled"
	platforms[9] = PlatformGemini
	require.Equal(t, true, cache.byID[1].FeaturesConfig["enabled"])
	require.Equal(t, StatusActive, cache.byID[1].Status)
	require.Equal(t, PlatformOpenAI, cache.groupPlatform[9])
}
