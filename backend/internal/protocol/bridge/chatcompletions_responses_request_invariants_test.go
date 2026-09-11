package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// assertChatInvariants 校验 DeepSeek / OpenAI Chat Completions 的消息不变量；
// 这些不变量一旦被破坏，上游通常会返回 400。这里用它验证 Codex 请求形状。
func assertChatInvariants(t *testing.T, messages []ChatMessage) {
	t.Helper()
	for i, m := range messages {
		// 每个 assistant tool_calls 后面都必须按顺序紧跟对应 tool message。
		if len(m.ToolCalls) > 0 {
			for j, tc := range m.ToolCalls {
				k := i + 1 + j
				require.Lessf(t, k, len(messages), "tool_call %s has no following tool message", tc.ID)
				require.Equalf(t, "tool", messages[k].Role, "tool_call %s not followed by a tool message", tc.ID)
				require.Equalf(t, tc.ID, messages[k].ToolCallID, "tool reply order mismatch for %s", tc.ID)
			}
		}
		// 不允许连续两个 assistant message。
		if i > 0 && m.Role == "assistant" && messages[i-1].Role == "assistant" {
			t.Fatalf("consecutive assistant messages at %d", i)
		}
		// 不允许孤儿 tool reply。
		if m.Role == "tool" {
			require.NotEmptyf(t, m.ToolCallID, "tool message without tool_call_id at %d", i)
		}
	}
}

func convertGolden(t *testing.T, input string) []ChatMessage {
	t.Helper()
	msgs, err := responsesInputToChatMessages("You are a helpful assistant.", json.RawMessage(input))
	require.NoError(t, err)
	return msgs
}

// 单个工具调用回合，覆盖最初触发“no response”/400 的 Codex 形状。
func TestGolden_SingleToolCall(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"latest sha?"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"need to run curl"}]},
		{"type":"function_call","call_id":"call_a","name":"exec_command","arguments":"{\"cmd\":\"curl x\"}"},
		{"type":"function_call_output","call_id":"call_a","output":"deadbeef"}
	]`)
	assertChatInvariants(t, msgs)
	// reasoning_content 必须挂在 assistant tool-call message 上。
	var asst *ChatMessage
	for i := range msgs {
		if len(msgs[i].ToolCalls) > 0 {
			asst = &msgs[i]
		}
	}
	require.NotNil(t, asst)
	require.Equal(t, "need to run curl", asst.ReasoningContent)
}

// 并行工具调用，模拟 Codex 同时运行多个命令。
func TestGolden_ParallelToolCalls(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"features?"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"inspect repo"}]},
		{"type":"function_call","call_id":"c0","name":"exec_command","arguments":"{\"cmd\":\"git log\"}"},
		{"type":"function_call","call_id":"c1","name":"exec_command","arguments":"{\"cmd\":\"git tag\"}"},
		{"type":"function_call_output","call_id":"c0","output":"log"},
		{"type":"function_call_output","call_id":"c1","output":"tags"}
	]`)
	assertChatInvariants(t, msgs)
	// 并行调用必须共享同一个 assistant message。
	var toolMsgs int
	for _, m := range msgs {
		if len(m.ToolCalls) == 2 {
			require.Equal(t, "c0", m.ToolCalls[0].ID)
			require.Equal(t, "c1", m.ToolCalls[1].ID)
		}
		if m.Role == "tool" {
			toolMsgs++
		}
	}
	require.Equal(t, 2, toolMsgs)
}

// 未知 item type（例如联网查询产生的 web_search_call）即使夹在 function_call
// 和 output 之间，也不能破坏 tool/reply 邻接关系。
func TestGolden_UnknownItemBetweenToolCallAndOutput(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"search"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"let me search"}]},
		{"type":"function_call","call_id":"c0","name":"exec_command","arguments":"{}"},
		{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"x"}},
		{"type":"function_call_output","call_id":"c0","output":"result"}
	]`)
	assertChatInvariants(t, msgs)
}

// 中间已有 tool reply 的顺序工具调用必须保留为不同 assistant message。
func TestRequest_SequentialToolCallsStaySeparate(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"function_call","call_id":"c1","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"r1"},
		{"type":"function_call","call_id":"c2","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"c2","output":"r2"}
	]`)
	assertChatInvariants(t, msgs)
	assistants := 0
	for _, m := range msgs {
		if len(m.ToolCalls) == 1 {
			assistants++
		}
	}
	require.Equal(t, 2, assistants)
}

// Codex 有时会在 function_call 和 output 之间插入通知消息；这类中间消息必须
// 移到 tool reply 之后，保证 assistant tool_calls 后面紧跟对应回复。
func TestGolden_MessageBetweenToolCallAndOutput(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"do it"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"run cmd"}]},
		{"type":"function_call","call_id":"A","name":"exec","arguments":"{}"},
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"Approved command prefix saved"}]},
		{"type":"function_call_output","call_id":"A","output":"ok"}
	]`)
	assertChatInvariants(t, msgs)
	// assistant tool_calls message 后面必须立刻跟着对应 tool reply。
	for i, m := range msgs {
		if len(m.ToolCalls) > 0 {
			require.Equal(t, "tool", msgs[i+1].Role)
			require.Equal(t, "A", msgs[i+1].ToolCallID)
		}
	}
}

// 并行工具调用里某个 sibling 输出缺失时（例如执行中断或重连），必须丢弃未回答
// 的 tool_call，保证保留下来的 assistant tool_calls 全部有回复。
func TestGolden_PartialParallelDropsUnansweredCall(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"r"}]},
		{"type":"function_call","call_id":"A","name":"exec","arguments":"{}"},
		{"type":"function_call","call_id":"B","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"A","output":"oa"}
	]`)
	assertChatInvariants(t, msgs)
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			require.NotEqual(t, "B", tc.ID, "unanswered tool_call B should have been dropped")
		}
	}
}

// 历史末尾悬空的 tool_call（尚无 output）必须被整体丢弃。
func TestGolden_DanglingToolCallDropped(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"r"}]},
		{"type":"function_call","call_id":"A","name":"exec","arguments":"{}"}
	]`)
	assertChatInvariants(t, msgs)
	for _, m := range msgs {
		require.Empty(t, m.ToolCalls, "dangling unanswered tool_call should have been dropped")
	}
}

// normalizeChatMessages 会丢弃没有对应 assistant tool_call 的孤儿 tool reply。
func TestNormalize_DropsOrphanToolReply(t *testing.T) {
	msgs := convertGolden(t, `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"function_call_output","call_id":"ghost","output":"orphan"}
	]`)
	for _, m := range msgs {
		require.NotEqualf(t, "tool", m.Role, "orphan tool reply should have been dropped")
	}
}
