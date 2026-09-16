package anthropic

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/stretchr/testify/require"
)

// 流事件只在重分类时写回两个数字字段，其它原始字段和未改写的表示保持原状。
func TestCacheTTLAdaptersPreserveWireShape(t *testing.T) {
	value := protocol.TokenUsage{CacheCreationInputTokens: 9}
	require.False(t, ApplyCacheTTLOverride(&value, "5m"))
	require.Equal(t, 9, value.CacheCreation5mTokens)
	raw := map[string]any{"cache_creation": map[string]any{"ephemeral_5m_input_tokens": float64(3), "ephemeral_1h_input_tokens": float64(4), "unknown": "kept"}, "cache_creation_input_tokens": float64(99)}
	require.True(t, RewriteCacheCreationJSON(raw, "1h"))
	require.Equal(t, map[string]any{"ephemeral_5m_input_tokens": float64(0), "ephemeral_1h_input_tokens": float64(7), "unknown": "kept"}, raw["cache_creation"])
	require.Equal(t, float64(99), raw["cache_creation_input_tokens"])
	require.False(t, RewriteCacheCreationJSON(raw, "1h"))
	unchanged := map[string]any{"cache_creation": map[string]any{"ephemeral_5m_input_tokens": float64(0), "ephemeral_1h_input_tokens": "7"}}
	require.False(t, RewriteCacheCreationJSON(unchanged, "1h"))
	creation, ok := unchanged["cache_creation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "7", creation["ephemeral_1h_input_tokens"])
	require.False(t, RewriteCacheCreationJSON(map[string]any{}, "1h"))
}
