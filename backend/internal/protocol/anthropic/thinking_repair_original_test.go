//go:build unit

package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

func TestStripEmptyTextBlocks(t *testing.T) {
	t.Run("strips top-level empty text", func(t *testing.T) {
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":""}]}]}`)
		out := anthropic.StripEmptyTextBlocks(input)
		var req map[string]any
		require.NoError(t, json.Unmarshal(out, &req))
		msgs := testassert.MustType[[]any](req["messages"])
		content := testassert.MustType[[]any](testassert.MustType[map[string]any](msgs[0])["content"])
		require.Len(t, content, 1)
		require.Equal(t, "hello", testassert.MustType[map[string]any](content[0])["text"])
	})

	t.Run("strips nested empty text in tool_result", func(t *testing.T) {
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"ok"},{"type":"text","text":""}]}]}]}`)
		out := anthropic.StripEmptyTextBlocks(input)
		var req map[string]any
		require.NoError(t, json.Unmarshal(out, &req))
		msgs := testassert.MustType[[]any](req["messages"])
		content := testassert.MustType[[]any](testassert.MustType[map[string]any](msgs[0])["content"])
		toolResult := testassert.MustType[map[string]any](content[0])
		nestedContent := testassert.MustType[[]any](toolResult["content"])
		require.Len(t, nestedContent, 1)
		require.Equal(t, "ok", testassert.MustType[map[string]any](nestedContent[0])["text"])
	})

	t.Run("no-op when no empty text", func(t *testing.T) {
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		out := anthropic.StripEmptyTextBlocks(input)
		require.Equal(t, input, out)
	})

	t.Run("preserves non-map blocks in content", func(t *testing.T) {
		// tool_result content can be a string; non-map blocks should pass through unchanged
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"string content"},{"type":"text","text":""}]}]}`)
		out := anthropic.StripEmptyTextBlocks(input)
		var req map[string]any
		require.NoError(t, json.Unmarshal(out, &req))
		msgs := testassert.MustType[[]any](req["messages"])
		content := testassert.MustType[[]any](testassert.MustType[map[string]any](msgs[0])["content"])
		require.Len(t, content, 1)
		toolResult := testassert.MustType[map[string]any](content[0])
		require.Equal(t, "tool_result", toolResult["type"])
		require.Equal(t, "string content", toolResult["content"])
	})

	t.Run("handles deeply nested tool_result", func(t *testing.T) {
		// Recursive: tool_result containing another tool_result with empty text
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":""},{"type":"text","text":"deep"}]}]}]}]}`)
		out := anthropic.StripEmptyTextBlocks(input)
		var req map[string]any
		require.NoError(t, json.Unmarshal(out, &req))
		msgs := testassert.MustType[[]any](req["messages"])
		content := testassert.MustType[[]any](testassert.MustType[map[string]any](msgs[0])["content"])
		outer := testassert.MustType[map[string]any](content[0])
		innerContent := testassert.MustType[[]any](outer["content"])
		inner := testassert.MustType[map[string]any](innerContent[0])
		deepContent := testassert.MustType[[]any](inner["content"])
		require.Len(t, deepContent, 1)
		require.Equal(t, "deep", testassert.MustType[map[string]any](deepContent[0])["text"])
	})
}

func TestRemoveThinkingDependentContextStrategies_NoContextManagement(t *testing.T) {
	input := []byte(`{"thinking":{"type":"enabled"},"messages":[]}`)
	out := anthropic.RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "无 context_management 字段时应原样返回")
}

func TestRemoveThinkingDependentContextStrategies_EmptyEdits(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[]},"messages":[]}`)
	out := anthropic.RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "edits 为空数组时应原样返回")
}

func TestRemoveThinkingDependentContextStrategies_NoClearThinkingEntry(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"other_strategy"}]},"messages":[]}`)
	out := anthropic.RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "edits 中无 clear_thinking_20251015 时应原样返回")
}

func TestRemoveThinkingDependentContextStrategies_RemovesSingleEntry(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	out := anthropic.RemoveThinkingDependentContextStrategies(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	_, hasEdits := cm["edits"]
	require.False(t, hasEdits, "所有 edits 均为 clear_thinking_20251015 时应删除 edits 键")
}

func TestRemoveThinkingDependentContextStrategies_MixedEntries(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"other_strategy","param":1}]},"messages":[]}`)
	out := anthropic.RemoveThinkingDependentContextStrategies(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	edits, ok := cm["edits"].([]any)
	require.True(t, ok)
	require.Len(t, edits, 1, "仅移除 clear_thinking_20251015，保留其他条目")
	edit0, ok := edits[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "other_strategy", edit0["type"])
}
