package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// issue #5528：/v1/messages 客户端(Claude Code 等)打到只会 Chat Completions 的
// OpenAI 兼容上游时，历史 assistant 消息里的 thinking 块被整块丢弃。DeepSeek 的
// thinking mode 要求产生工具调用的 reasoning_content 随该 assistant 消息回传，
// 于是「单轮正常、一进多轮工具对话必现 400」。

// 闭环不变式：thinking 块本来就是本桥出站时用上游 reasoning_content 生成的
// (chatMessageToAnthropicBlocks)，客户端只是原样回传。出站造、入站丢 = 自己丢自己的东西。
func TestAnthropicChatBridge_ReasoningSurvivesOutboundInboundRoundTrip(t *testing.T) {
	upstream := ChatMessage{
		Role:             "assistant",
		ReasoningContent: "step 1: need the weather tool",
		Content:          json.RawMessage(`"checking"`),
		ToolCalls: []ChatToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: ChatFunctionCall{Name: "get_weather", Arguments: `{"city":"SF"}`},
		}},
	}

	// 出站：Chat 响应 → Anthropic content blocks
	blocks := chatMessageToAnthropicBlocks(upstream)
	require.Equal(t, "thinking", blocks[0].Type)
	require.Equal(t, upstream.ReasoningContent, blocks[0].Thinking)

	// 客户端下一轮把同一组 blocks 原样回传
	raw, err := json.Marshal(blocks)
	require.NoError(t, err)

	// 入站：Anthropic content blocks → Chat 请求
	back, err := anthropicAssistantToChatMessages(raw)
	require.NoError(t, err)
	require.Len(t, back, 1)
	require.Equal(t, upstream.ReasoningContent, back[0].ReasoningContent,
		"出站生成的 thinking 必须能原样还原回 reasoning_content")
	require.Len(t, back[0].ToolCalls, 1)
}

func TestAnthropicThinkingToReasoningContent(t *testing.T) {
	blocksOf := func(t *testing.T, raw string) []AnthropicContentBlock {
		t.Helper()
		var blocks []AnthropicContentBlock
		require.NoError(t, json.Unmarshal([]byte(raw), &blocks))
		return blocks
	}

	cases := []struct {
		name         string
		raw          string
		hasToolCalls bool
		want         string
	}{
		{
			name:         "single_thinking_block",
			raw:          `[{"type":"thinking","thinking":"a"}]`,
			hasToolCalls: true,
			want:         "a",
		},
		{
			// 多个 thinking 块用 "\n" 连接，与 extractResponsesReasoningText 一致。
			name:         "multiple_blocks_join_with_newline",
			raw:          `[{"type":"thinking","thinking":"a"},{"type":"text","text":"x"},{"type":"thinking","thinking":"b"}]`,
			hasToolCalls: true,
			want:         "a\nb",
		},
		{
			// redacted_thinking 没有明文可回传。
			name:         "redacted_thinking_has_no_plaintext",
			raw:          `[{"type":"redacted_thinking","signature":"abc"}]`,
			hasToolCalls: true,
			want:         "",
		},
		{
			// 只带 signature 的 thinking 占位块(xAI/Codex 密文回放形态)同样无明文。
			name:         "signature_only_thinking",
			raw:          `[{"type":"thinking","thinking":"","signature":"gAAAAxxx"}]`,
			hasToolCalls: true,
			want:         "",
		},
		{
			name:         "no_tool_calls_returns_empty",
			raw:          `[{"type":"thinking","thinking":"a"}]`,
			hasToolCalls: false,
			want:         "",
		},
		{
			name:         "no_thinking_blocks",
			raw:          `[{"type":"text","text":"x"}]`,
			hasToolCalls: true,
			want:         "",
		},
		{
			name:         "empty_blocks",
			raw:          `[]`,
			hasToolCalls: true,
			want:         "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want,
				anthropicThinkingToReasoningContent(blocksOf(t, tc.raw), tc.hasToolCalls))
		})
	}
}

// 纯字符串形态的 assistant content 没有 blocks 可读，走早返回分支，不得 panic。
func TestAnthropicAssistantToChatMessages_PlainStringContentUnaffected(t *testing.T) {
	msgs, err := anthropicAssistantToChatMessages(json.RawMessage(`"just text"`))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].ReasoningContent)
	require.Equal(t, `"just text"`, string(msgs[0].Content))
}
