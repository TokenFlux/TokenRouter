package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeGeminiMessagesFinishReturnsCacheUsage supplies a cache-only prompt
// before stream finalization. The terminal wire event must preserve the cache
// count and explicitly report zero uncached input.
func TestNativeGeminiMessagesFinishReturnsCacheUsage(t *testing.T) {
	state := NewNativeGeminiMessagesStream(NativeGeminiRuntime{})
	raw := []byte(`{"usageMetadata":{"promptTokenCount":60,"cachedContentTokenCount":60,"candidatesTokenCount":10}}`)
	for range state.Process(map[string]any{}, raw) {
	}
	found := false
	for event := range state.Finish() {
		if event.Name != "message_delta" {
			continue
		}
		found = true
		encoded, err := json.Marshal(event.Data)
		require.NoError(t, err)
		var decoded AnthropicStreamEvent
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.NotNil(t, decoded.Usage)
		require.Zero(t, decoded.Usage.InputTokens)
		require.Equal(t, 60, decoded.Usage.CacheReadInputTokens)
		require.Equal(t, 10, decoded.Usage.OutputTokens)
	}
	require.True(t, found)
}

// Native Messages iteration updates usage after its output and flush yields
// finish. A consumer stopping at the visible delta therefore retains earlier
// counts; this fixture checks that ordering with one text part and supplied
// prompt metadata.

// TestNativeGeminiMessagesIteratorKeepsUsageUpdateAfterOutput breaks on the
// text delta before the remaining flush and usage update. It checks that the
// supplied prompt count has not reached the state.
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
