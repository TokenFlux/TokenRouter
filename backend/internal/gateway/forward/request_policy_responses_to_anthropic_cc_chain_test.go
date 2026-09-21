package forward

import (
	json "encoding/json"
	testing "testing"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	require "github.com/stretchr/testify/require"
)

// 这些测试覆盖 Anthropic 分组上 Chat Completions 客户端的真实生产链路：
// ForwardAsChatCompletions 会执行 ChatCompletionsToResponses →
// ResponsesToAnthropicRequest，再把 Anthropic 请求体转发给上游。这里验证配对修复在完整链路生效，
// 而不仅是 Codex 风格 Responses 输入的直接转换。
func ccChainToAnthropic(t *testing.T, ccReq *protocolopenai.ChatCompletionsRequest) []protocolanthropic.AnthropicMessage {
	t.Helper()
	respReq, err := ChatCompletionsToResponses(ccReq)
	require.NoError(t, err)
	anthReq, err := protocolbridge.ResponsesToAnthropicRequest(respReq)
	require.NoError(t, err)
	assertAnthropicPairing(t, anthReq.Messages)
	return anthReq.Messages
}

// 复现线上 400：
//
//	unexpected ...content.0: tool_use_id found in tool_result blocks:
//	call_00_TgfbRvKlnD7oK6Dg00sL1661. Each tool_result block must have a
//	corresponding tool_use block in the previous message.
//
// Chat Completions 客户端做滑动窗口上下文裁剪时，可能保留 tool 结果但丢掉声明该调用的
// assistant tool_calls 消息。这个孤儿 tool_result 没有匹配 tool_use，会触发上游 400；
// 修复逻辑会丢弃孤儿结果，让请求重新合法。
func TestCCChain_OrphanToolResultFromTrimmedHistory(t *testing.T) {
	orphanID := "call_00_TgfbRvKlnD7oK6Dg00sL1661"
	msgs := ccChainToAnthropic(t, &protocolopenai.ChatCompletionsRequest{
		Model: "deepseek-v4-pro",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"search the web for X"`)},
			// 声明 orphanID 的 assistant tool_calls 消息已被裁剪。
			{Role: "tool", ToolCallID: orphanID, Content: json.RawMessage(`"stale search results"`)},
			{Role: "assistant", Content: json.RawMessage(`"Here is what I found."`)},
			{Role: "user", Content: json.RawMessage(`"thanks, now do Y"`)},
		},
	})
	for _, m := range msgs {
		require.Falsef(t, hasToolResult(parseContentBlocks(m.Content), orphanID),
			"orphan tool_result %s should have been dropped", orphanID)
	}
}

// 并行 web_search 中某个 sibling 结果没有返回（工具失败或被跳过）。未回答 tool_use 会触发
// Anthropic 的 “tool_use 缺少 tool_result” 校验；修复逻辑会丢弃它。
func TestCCChain_ParallelToolOneResultMissing(t *testing.T) {
	msgs := ccChainToAnthropic(t, &protocolopenai.ChatCompletionsRequest{
		Model: "deepseek-v4-pro",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"search A and B"`)},
			{Role: "assistant", Content: json.RawMessage(`"searching both"`), ToolCalls: []protocolopenai.ChatToolCall{
				{ID: "call_a", Type: "function", Function: protocolopenai.ChatFunctionCall{Name: "web_search", Arguments: `{"q":"A"}`}},
				{ID: "call_b", Type: "function", Function: protocolopenai.ChatFunctionCall{Name: "web_search", Arguments: `{"q":"B"}`}},
			}},
			{Role: "tool", ToolCallID: "call_a", Content: json.RawMessage(`"result A"`)},
			// call_b 的结果缺失。
		},
	})
	for _, m := range msgs {
		require.Falsef(t, hasToolUse(parseContentBlocks(m.Content), "call_b"),
			"unanswered tool_use call_b should have been dropped")
	}
}

// 基线：结构良好的多轮工具历史（每轮 assistant 含文本和 tool_calls）应能完整转换并正确配对。
func TestCCChain_WellFormedMultiRound(t *testing.T) {
	msgs := ccChainToAnthropic(t, &protocolopenai.ChatCompletionsRequest{
		Model: "deepseek-v4-pro",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"do A then B"`)},
			{Role: "assistant", Content: json.RawMessage(`"running A"`), ToolCalls: []protocolopenai.ChatToolCall{
				{ID: "call_a", Type: "function", Function: protocolopenai.ChatFunctionCall{Name: "exec", Arguments: `{"cmd":"A"}`}},
			}},
			{Role: "tool", ToolCallID: "call_a", Content: json.RawMessage(`"A ok"`)},
			{Role: "assistant", Content: json.RawMessage(`"A done, running B"`), ToolCalls: []protocolopenai.ChatToolCall{
				{ID: "call_b", Type: "function", Function: protocolopenai.ChatFunctionCall{Name: "exec", Arguments: `{"cmd":"B"}`}},
			}},
			{Role: "tool", ToolCallID: "call_b", Content: json.RawMessage(`"B ok"`)},
			{Role: "assistant", Content: json.RawMessage(`"all done"`)},
		},
	})
	// 两个调用都应保留且保持配对；assertAnthropicPairing 已验证配对关系。
	var sawA, sawB bool
	for _, m := range msgs {
		blocks := parseContentBlocks(m.Content)
		sawA = sawA || hasToolUse(blocks, "call_a")
		sawB = sawB || hasToolUse(blocks, "call_b")
	}
	require.True(t, sawA && sawB, "both well-formed calls should be preserved")
}
