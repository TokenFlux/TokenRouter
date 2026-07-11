package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// assertAnthropicPairing 校验 Anthropic Messages 工具配对不变量，避免上游返回 400。
func assertAnthropicPairing(t *testing.T, messages []AnthropicMessage) {
	t.Helper()
	for i, m := range messages {
		blocks := parseContentBlocks(m.Content)

		// 不允许连续两条消息角色相同。
		if i > 0 {
			require.NotEqualf(t, messages[i-1].Role, m.Role, "consecutive %s messages at %d", m.Role, i)
		}

		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				// tool_result 必须在前一条消息里有对应 tool_use。
				require.Positivef(t, i, "tool_result %s has no previous message", b.ToolUseID)
				prev := parseContentBlocks(messages[i-1].Content)
				require.Truef(t, hasToolUse(prev, b.ToolUseID),
					"tool_result %s has no corresponding tool_use in previous message", b.ToolUseID)
			case "tool_use":
				// tool_use 必须在后一条消息里有对应 tool_result。
				require.Lessf(t, i+1, len(messages), "tool_use %s has no following message", b.ID)
				next := parseContentBlocks(messages[i+1].Content)
				require.Truef(t, hasToolResult(next, b.ID),
					"tool_use %s is not answered in the next message", b.ID)
			}
		}
	}
}

func hasToolUse(blocks []AnthropicContentBlock, id string) bool {
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID == id {
			return true
		}
	}
	return false
}

func hasToolResult(blocks []AnthropicContentBlock, toolUseID string) bool {
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == toolUseID {
			return true
		}
	}
	return false
}

func convertAnthropic(t *testing.T, input string) []AnthropicMessage {
	t.Helper()
	_, messages, err := convertResponsesInputToAnthropic("", json.RawMessage(input))
	require.NoError(t, err)
	assertAnthropicPairing(t, messages)
	return messages
}

// 测试使用 call_ 前缀 id，因为 fromResponsesCallIDToAnthropic 会原样透传这类 id，
// 与 Codex 真实的 call_00_... id 一致；裸 id 会被改写成 toolu_<id>。

// function_call 和 output 之间插入的 developer/审批消息必须移出 tool_use→tool_result 邻接关系，
// 这是线上触发 “tool_result 必须在前一条消息有对应 tool_use” 400 的典型形态。
func TestAnthropicPairing_DeveloperMessageBetween(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"do it"}]},
		{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{}"},
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"Approved command prefix saved"}]},
		{"type":"function_call_output","call_id":"call_A","output":"ok"}
	]`)
	// assistant tool_use 消息后必须紧跟对应 tool_result。
	for i, m := range msgs {
		if hasToolUse(parseContentBlocks(m.Content), "call_A") {
			require.Equal(t, "user", msgs[i+1].Role)
			require.True(t, hasToolResult(parseContentBlocks(msgs[i+1].Content), "call_A"))
		}
	}
}

// 并行工具调用的两个输出都到达时，应保持为一条 assistant 消息包含两个 tool_use，
// 下一条 user 消息包含两个结果。
func TestAnthropicPairing_ParallelBothAnswered(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"features?"}]},
		{"type":"function_call","call_id":"call_c0","name":"exec","arguments":"{}"},
		{"type":"function_call","call_id":"call_c1","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_c0","output":"log"},
		{"type":"function_call_output","call_id":"call_c1","output":"tags"}
	]`)
	var sawGrouped bool
	for _, m := range msgs {
		blocks := parseContentBlocks(m.Content)
		if hasToolUse(blocks, "call_c0") && hasToolUse(blocks, "call_c1") {
			sawGrouped = true
		}
	}
	require.True(t, sawGrouped, "parallel tool_use blocks should share one assistant message")
}

// 并行调用中某个 sibling 一直没有输出时，必须丢弃未回答调用，确保剩余 tool_use 都有结果。
func TestAnthropicPairing_ParallelOneUnanswered(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{}"},
		{"type":"function_call","call_id":"call_B","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_A","output":"oa"}
	]`)
	for _, m := range msgs {
		require.Falsef(t, hasToolUse(parseContentBlocks(m.Content), "call_B"),
			"unanswered tool_use call_B should have been dropped")
	}
}

// 没有对应 tool_use 的孤儿 tool_result 必须丢弃。
func TestAnthropicPairing_OrphanToolResultDropped(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"function_call_output","call_id":"call_ghost","output":"orphan"}
	]`)
	for _, m := range msgs {
		require.Falsef(t, hasToolResult(parseContentBlocks(m.Content), "call_ghost"),
			"orphan tool_result should have been dropped")
	}
}

// 历史末尾尚无输出的悬空 tool_call 会丢弃只包含该调用的 assistant 消息。
func TestAnthropicPairing_DanglingCallDropped(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{}"}
	]`)
	for _, m := range msgs {
		require.Falsef(t, hasToolUse(parseContentBlocks(m.Content), "call_A"),
			"dangling tool_use call_A should have been dropped")
	}
}

// 基线：单个已回答调用应正确配对，并保留前后轮次。
func TestAnthropicPairing_SingleCall(t *testing.T) {
	msgs := convertAnthropic(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"latest sha?"}]},
		{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{\"cmd\":\"git rev-parse HEAD\"}"},
		{"type":"function_call_output","call_id":"call_A","output":"deadbeef"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is deadbeef."}]}
	]`)
	// 顺序应为 user、assistant(tool_use)、user(tool_result)、assistant(text)。
	require.GreaterOrEqual(t, len(msgs), 4)
	require.Equal(t, "user", msgs[0].Role)
	require.True(t, hasToolUse(parseContentBlocks(msgs[1].Content), "call_A"))
	require.True(t, hasToolResult(parseContentBlocks(msgs[2].Content), "call_A"))
}
