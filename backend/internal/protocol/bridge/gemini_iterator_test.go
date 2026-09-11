package bridge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 消费者在第一个工具事件后停止时，不应预先生成后续工具 ID。
func TestNativeGeminiCompatIteratorPreservesPartialUsageAndLazyIDs(t *testing.T) {
	generated := 0
	runtime := NativeGeminiRuntime{RandomHex: func(size int) string { generated++; return strings.Repeat("a", size*2) }}
	state := NewNativeGeminiCompatStream(runtime)
	response := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
		map[string]any{"functionCall": map[string]any{"name": "first", "args": map[string]any{}}},
		map[string]any{"functionCall": map[string]any{"name": "second", "args": map[string]any{}}},
	}}}}}
	raw := []byte(`{"usageMetadata":{"promptTokenCount":12,"cachedContentTokenCount":2,"candidatesTokenCount":3}}`)
	for event := range state.Process(response, raw) {
		if event.Type == "content_block_start" {
			break
		}
	}
	require.Equal(t, 1, generated)
	require.Equal(t, 10, state.Usage().InputTokens)
	require.Equal(t, 3, state.Usage().OutputTokens)
	require.Equal(t, 2, state.Usage().CacheReadInputTokens)
}

// Messages 直转链原本在分片处理后才累计 usage，与 OpenAI 兼容链保持区别。
func TestNativeGeminiMessagesIteratorKeepsUsageUpdateAfterOutput(t *testing.T) {
	state := NewNativeGeminiMessagesStream(NativeGeminiRuntime{})
	response := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "hello"}}}}}}
	raw := []byte(`{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3}}`)
	for event := range state.Process(response, raw) {
		if event.Name == "content_block_delta" {
			break
		}
	}
	require.Zero(t, state.Usage().InputTokens)
}
