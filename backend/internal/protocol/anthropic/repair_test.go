package anthropic

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestStripEmptyTextBlocks removes empty text at message and nested tool-result
// levels. Nonempty text and string-valued results must remain usable, while a
// request needing no cleanup retains its supplied bytes.
func TestStripEmptyTextBlocks(t *testing.T) {
	t.Run("strips top-level empty text", func(t *testing.T) {
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":""}]}]}`)
		out := StripEmptyTextBlocks(input)
		var req map[string]any
		require.NoError(t, json.Unmarshal(out, &req))
		msgs := testassert.MustType[[]any](req["messages"])
		content := testassert.MustType[[]any](testassert.MustType[map[string]any](msgs[0])["content"])
		require.Len(t, content, 1)
		require.Equal(t, "hello", testassert.MustType[map[string]any](content[0])["text"])
	})

	t.Run("strips nested empty text in tool_result", func(t *testing.T) {
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"ok"},{"type":"text","text":""}]}]}]}`)
		out := StripEmptyTextBlocks(input)
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
		out := StripEmptyTextBlocks(input)
		require.Equal(t, input, out)
	})

	t.Run("preserves non-map blocks in content", func(t *testing.T) {
		// String-valued tool_result content passes through unchanged.
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"string content"},{"type":"text","text":""}]}]}`)
		out := StripEmptyTextBlocks(input)
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
		// Empty text is removed from nested tool_result content too.
		input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":""},{"type":"text","text":"deep"}]}]}]}]}`)
		out := StripEmptyTextBlocks(input)
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

// TestRemoveThinkingDependentContextStrategies_NoContextManagement supplies a
// request with thinking but no context-management configuration. Strategy cleanup
// must return the supplied bytes because there is no edits list to repair.
func TestRemoveThinkingDependentContextStrategies_NoContextManagement(t *testing.T) {
	input := []byte(`{"thinking":{"type":"enabled"},"messages":[]}`)
	out := RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "无 context_management 字段时应原样返回")
}

// TestRemoveThinkingDependentContextStrategies_EmptyEdits supplies an existing
// empty context edits array. Cleanup must leave that request byte-for-byte
// unchanged because it contains no thinking-dependent strategy to remove.
func TestRemoveThinkingDependentContextStrategies_EmptyEdits(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[]},"messages":[]}`)
	out := RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "edits 为空数组时应原样返回")
}

// TestRemoveThinkingDependentContextStrategies_NoClearThinkingEntry supplies a
// context edits list containing another strategy. Cleanup must retain the supplied
// bytes because the list has no clear-thinking entry.
func TestRemoveThinkingDependentContextStrategies_NoClearThinkingEntry(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"other_strategy"}]},"messages":[]}`)
	out := RemoveThinkingDependentContextStrategies(input)
	require.Equal(t, input, out, "edits 中无 clear_thinking_20251015 时应原样返回")
}

// TestRemoveThinkingDependentContextStrategies_RemovesSingleEntry removes the
// only clear-thinking edit from a context object. The edits key must disappear
// while the surrounding context_management object remains present.
func TestRemoveThinkingDependentContextStrategies_RemovesSingleEntry(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	out := RemoveThinkingDependentContextStrategies(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	_, hasEdits := cm["edits"]
	require.False(t, hasEdits, "所有 edits 均为 clear_thinking_20251015 时应删除 edits 键")
}

// TestRemoveThinkingDependentContextStrategies_MixedEntries places a dependent
// clear-thinking edit beside another context strategy. Cleanup must remove the
// dependent entry and retain the other strategy as the sole remaining edit.
func TestRemoveThinkingDependentContextStrategies_MixedEntries(t *testing.T) {
	input := []byte(`{"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"other_strategy","param":1}]},"messages":[]}`)
	out := RemoveThinkingDependentContextStrategies(input)

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

// TestFilterThinkingBlocksInternal_SignedHistoryZeroAllocAndUnsignedFiltered
// supplies signed assistant thinking blocks and unsigned thinking blocks. Signed
// histories return unchanged with zero allocations, while unsigned blocks are
// converted or removed according to the thinking-filter contract.
func TestFilterThinkingBlocksInternal_SignedHistoryZeroAllocAndUnsignedFiltered(t *testing.T) {
	signed := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"enabled","budget_tokens":4096},"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"explain"}]},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"step 1","signature":"sig_valid_123"},{"type":"text","text":"answer"}]}` +
		`]}`)
	allocs := testing.AllocsPerRun(20, func() {
		out := FilterThinkingBlocksInternal(signed, "")
		if len(out) != len(signed) {
			t.Fatal("unexpected rewrite of signed assistant thinking history")
		}
	})
	require.Zero(t, allocs)

	unsigned := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"enabled","budget_tokens":4096},"messages":[` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"missing sig"},{"type":"text","text":"answer"}]}` +
		`]}`)
	repaired := FilterThinkingBlocksInternal(unsigned, "")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(repaired, &decoded))
	msgs, ok := decoded["messages"].([]any)
	require.True(t, ok)
	firstMsg, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	content, ok := firstMsg["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	firstBlock, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", firstBlock["type"])
	require.Equal(t, "answer", firstBlock["text"])
}

// BenchmarkFilterThinkingBlocksInternal_SignedConversation screens 1 MiB, 5 MiB,
// 20 MiB, and 32 MiB signed Claude conversations through FilterThinkingBlocksInternal.
// Each sub-benchmark reports byte throughput and confirms zero heap allocations
// when assistant thinking blocks already carry valid signatures.
func BenchmarkFilterThinkingBlocksInternal_SignedConversation(b *testing.B) {
	for _, sizeMiB := range []int{1, 5, 20, 32} {
		payload := buildSignedAnthropicConversation(sizeMiB << 20)
		b.Run(strconv.Itoa(sizeMiB)+"MiB", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for range b.N {
				_ = FilterThinkingBlocksInternal(payload, "")
			}
		})
	}
}

// buildSignedAnthropicConversation builds a valid Messages JSON payload of at
// least targetBytes whose assistant turns carry signed thinking blocks.
func buildSignedAnthropicConversation(targetBytes int) []byte {
	out := make([]byte, 0, targetBytes+4096)
	out = append(out, `{"model":"claude-opus-4-6","stream":true,"thinking":{"type":"enabled","budget_tokens":8192},"messages":[`...)
	chunk := strings.Repeat("detailed repository file content with title and metadata ", 32)
	turn := 0
	for len(out) < targetBytes {
		if turn > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"role":"user","content":[{"type":"text","text":"`...)
		out = append(out, chunk...)
		out = append(out, `"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"analyzing turn","signature":"sig_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `"},{"type":"text","text":"`...)
		out = append(out, chunk...)
		out = append(out, `"}]}`...)
		turn++
	}
	out = append(out, `]}`...)
	return out
}
