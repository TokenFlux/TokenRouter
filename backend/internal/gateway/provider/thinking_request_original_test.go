//go:build unit

package provider

import (
	"encoding/json"
	"testing"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ============ Gemini 原生格式解析测试 ============

func TestFilterThinkingBlocks(t *testing.T) {
	containsThinkingBlock := func(body []byte) bool {
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			return false
		}
		messages, ok := req["messages"].([]any)
		if !ok {
			return false
		}
		for _, msg := range messages {
			msgMap, ok := msg.(map[string]any)
			if !ok {
				continue
			}
			content, ok := msgMap["content"].([]any)
			if !ok {
				continue
			}
			for _, block := range content {
				blockMap, ok := block.(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := blockMap["type"].(string)
				if blockType == "thinking" {
					return true
				}
				if blockType == "" {
					if _, hasThinking := blockMap["thinking"]; hasThinking {
						return true
					}
				}
			}
		}
		return false
	}

	tests := []struct {
		name         string
		input        string
		shouldFilter bool
		expectError  bool
	}{
		{
			name:         "filters thinking blocks",
			input:        `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":[{"type":"text","text":"Hello"},{"type":"thinking","thinking":"internal","signature":"invalid"},{"type":"text","text":"World"}]}]}`,
			shouldFilter: true,
		},
		{
			name:         "does not filter signed thinking blocks when thinking adaptive",
			input:        `{"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"ok","signature":"sig_real_123"},{"type":"text","text":"B"}]}]}`,
			shouldFilter: false,
		},
		{
			name:         "filters unsigned thinking blocks when thinking adaptive",
			input:        `{"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"internal","signature":""},{"type":"text","text":"B"}]}]}`,
			shouldFilter: true,
		},
		{
			name:         "handles no thinking blocks",
			input:        `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}]}`,
			shouldFilter: false,
		},
		{
			name:         "handles invalid JSON gracefully",
			input:        `{invalid json`,
			shouldFilter: false,
			expectError:  true,
		},
		{
			name:         "handles multiple messages with thinking blocks",
			input:        `{"messages":[{"role":"user","content":[{"type":"text","text":"A"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"think"},{"type":"text","text":"B"}]}]}`,
			shouldFilter: true,
		},
		{
			name:         "filters thinking blocks without type discriminator",
			input:        `{"messages":[{"role":"assistant","content":[{"thinking":{"text":"internal"}},{"type":"text","text":"B"}]}]}`,
			shouldFilter: true,
		},
		{
			name:         "does not filter tool_use input fields named thinking",
			input:        `{"messages":[{"role":"user","content":[{"type":"tool_use","id":"t1","name":"foo","input":{"thinking":"keepme","x":1}},{"type":"text","text":"Hello"}]}]}`,
			shouldFilter: false,
		},
		{
			name:         "handles empty messages array",
			input:        `{"messages":[]}`,
			shouldFilter: false,
		},
		{
			name:         "handles missing messages field",
			input:        `{"model":"claude-3"}`,
			shouldFilter: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterThinkingBlocks([]byte(tt.input))

			if tt.expectError {
				// For invalid JSON, should return original
				require.Equal(t, tt.input, string(result))
				return
			}

			if tt.shouldFilter {
				require.False(t, containsThinkingBlock(result))
			} else {
				// Ensure we don't rewrite JSON when no filtering is needed.
				require.Equal(t, tt.input, string(result))
			}

			// Verify valid JSON returned (unless input was invalid)
			var parsed map[string]any
			err := json.Unmarshal(result, &parsed)
			require.NoError(t, err)
		})
	}
}

func TestFilterThinkingBlocksForRetry_DisablesThinkingAndPreservesAsText(t *testing.T) {
	input := []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Hi"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"Let me think...","signature":"bad_sig"},
				{"type":"text","text":"Answer"}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking)

	msgs, ok := req["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 2)

	assistant, ok := msgs[1].(map[string]any)
	require.True(t, ok)
	content, ok := assistant["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)

	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", first["type"])
	require.Equal(t, "Let me think...", first["text"])
}

func TestFilterThinkingBlocksForRetry_DisablesThinkingEvenWithoutThinkingBlocks(t *testing.T) {
	input := []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Hi"}]},
			{"role":"assistant","content":[{"type":"text","text":"Prefill"}]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking)
}

func TestFilterThinkingBlocksForRetry_RemovesRedactedThinkingAndKeepsValidContent(t *testing.T) {
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"assistant","content":[
				{"type":"redacted_thinking","data":"..."},
				{"type":"text","text":"Visible"}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking)

	msgs, ok := req["messages"].([]any)
	require.True(t, ok)
	msg0, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	content, ok := msg0["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	content0, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", content0["type"])
	require.Equal(t, "Visible", content0["text"])
}

func TestFilterThinkingBlocksForRetry_DropsThinkingBlockWithEmptyContent(t *testing.T) {
	// 跨模型场景：其他模型回过的 assistant 历史里携带了 type=thinking 但 thinking 字段为空，
	// 喂给开启 extended thinking 的 claude 时上游会报：
	//   "messages.1.content.0.thinking: each thinking block must contain thinking"
	// 重试应当把空 thinking 块丢弃，并保留其它有效内容。
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Hi"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"","signature":"sig"},
				{"type":"text","text":"Answer"}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking, "top-level thinking should be removed")

	msgs := testassert.MustType[[]any](req["messages"])
	assistant := testassert.MustType[map[string]any](msgs[1])
	content := testassert.MustType[[]any](assistant["content"])
	require.Len(t, content, 1, "empty thinking block should be dropped, only text remains")
	require.Equal(t, "text", testassert.MustType[map[string]any](content[0])["type"])
	require.Equal(t, "Answer", testassert.MustType[map[string]any](content[0])["text"])
}

func TestFilterThinkingBlocksForRetry_EmptyContentGetsPlaceholder(t *testing.T) {
	input := []byte(`{
		"thinking":{"type":"enabled"},
		"messages":[
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"..."}]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	msgs, ok := req["messages"].([]any)
	require.True(t, ok)
	msg0, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	content, ok := msg0["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	content0, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", content0["type"])
	require.NotEmpty(t, content0["text"])
}

func TestFilterThinkingBlocksForRetry_StripsEmptyTextBlocks(t *testing.T) {
	// Empty text blocks cause upstream 400: "text content blocks must be non-empty"
	input := []byte(`{
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":""}]},
			{"role":"assistant","content":[{"type":"text","text":""}]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	msgs, ok := req["messages"].([]any)
	require.True(t, ok)

	// First message: empty text block stripped, "hello" preserved
	msg0 := testassert.MustType[map[string]any](msgs[0])
	content0 := testassert.MustType[[]any](msg0["content"])
	require.Len(t, content0, 1)
	require.Equal(t, "hello", testassert.MustType[map[string]any](content0[0])["text"])

	// Second message: only had empty text block → gets placeholder
	msg1 := testassert.MustType[map[string]any](msgs[1])
	content1 := testassert.MustType[[]any](msg1["content"])
	require.Len(t, content1, 1)
	block1 := testassert.MustType[map[string]any](content1[0])
	require.Equal(t, "text", block1["type"])
	require.NotEmpty(t, block1["text"])
}

func TestFilterThinkingBlocksForRetry_StripsNestedEmptyTextInToolResult(t *testing.T) {
	// Empty text blocks nested inside tool_result content should also be stripped
	input := []byte(`{
		"messages":[
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"t1","content":[
					{"type":"text","text":"valid result"},
					{"type":"text","text":""}
				]}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	msgs := testassert.MustType[[]any](req["messages"])
	msg0 := testassert.MustType[map[string]any](msgs[0])
	content0 := testassert.MustType[[]any](msg0["content"])
	require.Len(t, content0, 1)
	toolResult := testassert.MustType[map[string]any](content0[0])
	require.Equal(t, "tool_result", toolResult["type"])
	nestedContent := testassert.MustType[[]any](toolResult["content"])
	require.Len(t, nestedContent, 1)
	require.Equal(t, "valid result", testassert.MustType[map[string]any](nestedContent[0])["text"])
}

func TestFilterThinkingBlocksForRetry_NestedAllEmptyGetsEmptySlice(t *testing.T) {
	// If all nested content blocks in tool_result are empty text, content becomes empty slice
	input := []byte(`{
		"messages":[
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"t1","content":[
					{"type":"text","text":""}
				]},
				{"type":"text","text":"hello"}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	msgs := testassert.MustType[[]any](req["messages"])
	msg0 := testassert.MustType[map[string]any](msgs[0])
	content0 := testassert.MustType[[]any](msg0["content"])
	require.Len(t, content0, 2)
	toolResult := testassert.MustType[map[string]any](content0[0])
	nestedContent := testassert.MustType[[]any](toolResult["content"])
	require.Len(t, nestedContent, 0)
}

func TestFilterThinkingBlocksForRetry_PreservesNonEmptyTextBlocks(t *testing.T) {
	// Non-empty text blocks should pass through unchanged
	input := []byte(`{
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	// Fast path: no thinking content, no empty content, no empty text blocks → unchanged
	require.Equal(t, input, out)
}

func TestFilterSignatureSensitiveBlocksForRetry_DowngradesTools(t *testing.T) {
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"assistant","content":[
				{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}},
				{"type":"tool_result","tool_use_id":"t1","content":"ok","is_error":false}
			]}
		]
	}`)

	out := FilterSignatureSensitiveBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking)

	msgs, ok := req["messages"].([]any)
	require.True(t, ok)
	msg0, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	content, ok := msg0["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	content0, ok := content[0].(map[string]any)
	require.True(t, ok)
	content1, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", content0["type"])
	require.Equal(t, "text", content1["type"])
	require.Contains(t, content0["text"], "tool_use")
	require.Contains(t, content1["text"], "tool_result")
}

// ============ Group 6b: context_management.edits 清理测试 ============

// removeThinkingDependentContextStrategies — 边界用例

// FilterThinkingBlocksForRetry — 包含 context_management 的场景

func TestFilterThinkingBlocksForRetry_RemovesClearThinkingStrategy_FastPath(t *testing.T) {
	// 快速路径：messages 中无 thinking 块，仅有顶层 thinking 字段
	// 这条路径曾因提前 return 跳过 removeThinkingDependentContextStrategies 而存在 bug
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Hello"}]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking, "顶层 thinking 应被移除")

	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	_, hasEdits := cm["edits"]
	require.False(t, hasEdits, "fast path 下 clear_thinking_20251015 应被移除，edits 键应被删除")
}

func TestFilterThinkingBlocksForRetry_RemovesClearThinkingStrategy_WithThinkingBlocks(t *testing.T) {
	// 完整路径：messages 中有 thinking 块（非 fast path）
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"keep_this"}]},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"some thought","signature":"sig"},
				{"type":"text","text":"Answer"}
			]}
		]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking, "顶层 thinking 应被移除")

	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	edits, ok := cm["edits"].([]any)
	require.True(t, ok)
	require.Len(t, edits, 1, "仅移除 clear_thinking_20251015，保留 keep_this")
	edit0, ok := edits[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "keep_this", edit0["type"])
}

func TestFilterThinkingBlocksForRetry_NoContextManagement_Unaffected(t *testing.T) {
	// 无 context_management 时不应报错，且 thinking 正常被移除
	input := []byte(`{
		"thinking":{"type":"enabled"},
		"messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]}]
	}`)

	out := FilterThinkingBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking)
	_, hasCM := req["context_management"]
	require.False(t, hasCM)
}

// FilterSignatureSensitiveBlocksForRetry — 包含 context_management 的场景

func TestFilterSignatureSensitiveBlocksForRetry_RemovesClearThinkingStrategy(t *testing.T) {
	input := []byte(`{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"thought","signature":"sig"}
			]}
		]
	}`)

	out := FilterSignatureSensitiveBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	_, hasThinking := req["thinking"]
	require.False(t, hasThinking, "顶层 thinking 应被移除")

	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	if rawEdits, hasEdits := cm["edits"]; hasEdits {
		edits, ok := rawEdits.([]any)
		require.True(t, ok)
		for _, e := range edits {
			em, ok := e.(map[string]any)
			require.True(t, ok)
			require.NotEqual(t, "clear_thinking_20251015", em["type"], "clear_thinking_20251015 应被移除")
		}
	}
}

func TestFilterSignatureSensitiveBlocksForRetry_PreservesNonThinkingStrategies(t *testing.T) {
	input := []byte(`{
		"thinking":{"type":"enabled"},
		"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"other_edit"}]},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"t","signature":"s"}
			]}
		]
	}`)

	out := FilterSignatureSensitiveBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))

	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	edits, ok := cm["edits"].([]any)
	require.True(t, ok)
	require.Len(t, edits, 1, "仅移除 clear_thinking_20251015，保留 other_edit")
	edit0, ok := edits[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "other_edit", edit0["type"])
}

func TestFilterSignatureSensitiveBlocksForRetry_NoThinkingField_ContextManagementUntouched(t *testing.T) {
	// 没有顶层 thinking 字段时，context_management 不应被修改
	input := []byte(`{
		"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"t","signature":"s"}
			]}
		]
	}`)

	out := FilterSignatureSensitiveBlocksForRetry(input)

	var req map[string]any
	require.NoError(t, json.Unmarshal(out, &req))
	cm, ok := req["context_management"].(map[string]any)
	require.True(t, ok)
	edits, ok := cm["edits"].([]any)
	require.True(t, ok)
	require.Len(t, edits, 1, "无顶层 thinking 时 context_management 不应被修改")
}

// ============ Group 7: ParseGatewayRequest 补充单元测试 ============

// Task 7.3 — Gemini 协议分支测试
// 已有测试覆盖：
// - TestParseGatewayRequest_GeminiSystemInstruction: 正常 systemInstruction+contents
// - TestParseGatewayRequest_GeminiNoContents: 缺失 contents
// - TestParseGatewayRequest_GeminiContents: 正常 contents（无 systemInstruction）
// 因此跳过。

// ============ Task 7.5: Benchmark 测试 ============

func TestDefaultEffortForThinkingEnabled(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  *string
	}{
		{name: "glm", model: "glm-5.1", want: thinkingStringPointer("high")},
		{name: "kimi", model: "kimi-k2.6", want: thinkingStringPointer("high")},
		{name: "moonshot", model: "moonshot-v1-8k", want: thinkingStringPointer("high")},
		{name: "minimax lowercase", model: "minimax-m3", want: thinkingStringPointer("high")},
		{name: "minimax mixed case", model: "MiniMax-M3", want: thinkingStringPointer("high")},
		{name: "qwen thinking", model: "qwen3-235b-a22b-thinking-2507", want: thinkingStringPointer("high")},
		{name: "deepseek excluded", model: "deepseek-v4-pro", want: nil},
		{name: "claude strict", model: "claude-opus-4.6", want: nil},
		{name: "unknown", model: "gpt-5.5", want: nil},
		{name: "empty", model: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultEffortForThinkingEnabled(tt.model)
			if tt.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, *tt.want, *got)
		})
	}
}

func TestNormalizeChineseLLMThinking(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		input       string
		wantChanged bool
		wantType    string
	}{
		{
			name:        "minimax enabled becomes adaptive",
			model:       "MiniMax-M3",
			input:       `{"thinking":{"type":"enabled","budget_tokens":8192},"messages":[]}`,
			wantChanged: true,
			wantType:    "adaptive",
		},
		{
			name:     "minimax adaptive unchanged",
			model:    "MiniMax-M2.7",
			input:    `{"thinking":{"type":"adaptive"},"messages":[]}`,
			wantType: "adaptive",
		},
		{
			name:     "kimi unchanged",
			model:    "kimi-k2.6",
			input:    `{"thinking":{"type":"enabled"},"messages":[]}`,
			wantType: "enabled",
		},
		{
			name:  "invalid json unchanged",
			model: "MiniMax-M3",
			input: `{not json`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := NormalizeChineseLLMThinking([]byte(tt.input), tt.model)
			require.Equal(t, tt.wantChanged, changed)
			if !tt.wantChanged {
				require.Equal(t, tt.input, string(got))
			}
			if tt.wantType != "" {
				require.Equal(t, tt.wantType, gjson.GetBytes(got, "thinking.type").String())
			}
		})
	}
}

func TestApplyThinkingEnabledFallback(t *testing.T) {
	explicit := "medium"
	tests := []struct {
		name        string
		effort      *string
		body        string
		model       string
		want        *string
		wantSamePtr bool
	}{
		{name: "explicit effort unchanged", effort: &explicit, body: `{"thinking":{"type":"enabled"}}`, model: "glm-5.1", wantSamePtr: true},
		{name: "glm thinking enabled defaults high", body: `{"thinking":{"type":"enabled"}}`, model: "glm-5.1", want: thinkingStringPointer("high")},
		{name: "minimax adaptive defaults high", body: `{"thinking":{"type":"adaptive"}}`, model: "MiniMax-M3", want: thinkingStringPointer("high")},
		{name: "deepseek excluded", body: `{"thinking":{"type":"enabled"}}`, model: "deepseek-v4-pro", want: nil},
		{name: "thinking disabled no fallback", body: `{"thinking":{"type":"disabled"}}`, model: "glm-5.1", want: nil},
		{name: "unknown model no fallback", body: `{"thinking":{"type":"enabled"}}`, model: "gpt-5.5", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyThinkingEnabledFallback(tt.effort, []byte(tt.body), tt.model)
			if tt.wantSamePtr {
				require.Same(t, tt.effort, got)
				return
			}
			if tt.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, *tt.want, *got)
		})
	}
}

func TestNormalizeGLMOpenAIReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		input         string
		wantApplied   bool
		wantPath      string
		wantValue     string
		wantUnchanged bool
	}{
		{
			name:        "flat xhigh maps to max",
			model:       "glm-5.2",
			input:       `{"model":"glm-5.2","reasoning_effort":"xhigh","messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning_effort",
			wantValue:   "max",
		},
		{
			name:        "flat x-high maps to max",
			model:       "GLM-5.2",
			input:       `{"model":"glm-5.2","reasoning_effort":"x-high","messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning_effort",
			wantValue:   "max",
		},
		{
			name:        "flat ultracode maps to max",
			model:       "glm-5.2",
			input:       `{"model":"glm-5.2","reasoning_effort":"ultracode","messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning_effort",
			wantValue:   "max",
		},
		{
			name:        "flat medium maps to high",
			model:       "glm-5.2",
			input:       `{"model":"glm-5.2","reasoning_effort":"medium","messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning_effort",
			wantValue:   "high",
		},
		{
			name:        "glm 5.2 low maps to high",
			model:       "glm-5.2",
			input:       `{"model":"glm-5.2","reasoning_effort":"low","messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning_effort",
			wantValue:   "high",
		},
		{
			name:        "nested high case-normalizes",
			model:       "glm-5.2",
			input:       `{"model":"glm-5.2","reasoning":{"effort":"HIGH"},"messages":[]}`,
			wantApplied: true,
			wantPath:    "reasoning.effort",
			wantValue:   "high",
		},
		{
			name:          "glm 5.3 native low unchanged",
			model:         "glm-5.3",
			input:         `{"model":"glm-5.3","reasoning_effort":"low","messages":[]}`,
			wantApplied:   false,
			wantUnchanged: true,
		},
		{
			name:          "native max unchanged",
			model:         "glm-5.2",
			input:         `{"model":"glm-5.2","reasoning_effort":"max","messages":[]}`,
			wantApplied:   false,
			wantUnchanged: true,
		},
		{
			name:          "non glm unchanged",
			model:         "deepseek-v4-pro",
			input:         `{"model":"deepseek-v4-pro","reasoning_effort":"xhigh","messages":[]}`,
			wantApplied:   false,
			wantUnchanged: true,
		},
		{
			name:          "missing effort unchanged",
			model:         "glm-5.2",
			input:         `{"model":"glm-5.2","messages":[]}`,
			wantApplied:   false,
			wantUnchanged: true,
		},
		{
			name:          "unknown effort unchanged",
			model:         "glm-5.2",
			input:         `{"model":"glm-5.2","reasoning_effort":"banana","messages":[]}`,
			wantApplied:   false,
			wantUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, applied := NormalizeGLMOpenAIReasoningEffort([]byte(tt.input), tt.model)
			require.Equal(t, tt.wantApplied, applied)
			if tt.wantUnchanged {
				require.Equal(t, tt.input, string(got))
				return
			}
			require.Equal(t, tt.wantValue, gjson.GetBytes(got, tt.wantPath).String())
		})
	}
}

// 原平台默认档位断言的独立值。
func thinkingStringPointer(value string) *string { return &value }
