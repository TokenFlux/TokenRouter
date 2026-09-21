package forward

import (
	json "encoding/json"
	testing "testing"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	require "github.com/stretchr/testify/require"
)

func anthropicAssistantMsg(t *testing.T, blocks string) *protocolanthropic.AnthropicRequest {
	t.Helper()
	return &protocolanthropic.AnthropicRequest{
		Model:     "deepseek-v4-flash",
		MaxTokens: 256,
		Messages: []protocolanthropic.AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"what's the weather?"`)},
			{Role: "assistant", Content: json.RawMessage(blocks)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}]`)},
		},
	}
}

const anthropicThinkingToolTurn = `[
	{"type":"thinking","thinking":"user wants weather, call the tool"},
	{"type":"text","text":"checking"},
	{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
]`

func TestAnthropicToChatCompletionsRequest_ThinkingBecomesReasoningContentOnToolTurn(t *testing.T) {
	out, err := AnthropicToChatCompletionsRequest(anthropicAssistantMsg(t, anthropicThinkingToolTurn))
	require.NoError(t, err)

	var assistant *protocolopenai.ChatMessage
	for i := range out.Messages {
		if out.Messages[i].Role == "assistant" {
			assistant = &out.Messages[i]
			break
		}
	}
	require.NotNil(t, assistant, "assistant message must survive the bridge")
	require.Equal(t, "user wants weather, call the tool", assistant.ReasoningContent,
		"产生工具调用的 thinking 必须作为 reasoning_content 回传，否则 DeepSeek 400")
	require.Len(t, assistant.ToolCalls, 1)
	require.Equal(t, `"checking"`, string(assistant.Content), "text/tool_use 处理保持不变")
}

// 上游线格式才是上游看到的东西：字段没序列化出去，等于没修。
func TestAnthropicToChatCompletionsRequest_ReasoningContentSerializesOnWire(t *testing.T) {
	out, err := AnthropicToChatCompletionsRequest(anthropicAssistantMsg(t, anthropicThinkingToolTurn))
	require.NoError(t, err)

	payload, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"reasoning_content":"user wants weather, call the tool"`)
}

// 兄弟不变式：Responses→Chat 桥(buildChatMessagesFromItems 的 pendingReasoning)
// 早就把 reasoning 挂到带 tool_calls 的 assistant 消息上了。等价历史下两条桥必须一致。
func TestAnthropicChatBridge_MatchesResponsesChatBridgeReasoningPlacement(t *testing.T) {
	responsesReq := &protocolopenai.ResponsesRequest{
		Model: "deepseek-v4-flash",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"what's the weather?"}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"call the tool"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		]`),
	}
	viaResponses, err := protocolbridge.ResponsesToChatCompletionsRequest(responsesReq)
	require.NoError(t, err)

	viaAnthropic, err := AnthropicToChatCompletionsRequest(anthropicAssistantMsg(t, `[
		{"type":"thinking","thinking":"call the tool"},
		{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
	]`))
	require.NoError(t, err)

	reasoningOnToolCallMessage := func(msgs []protocolopenai.ChatMessage) string {
		for _, m := range msgs {
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				return m.ReasoningContent
			}
		}
		return ""
	}
	require.Equal(t, "call the tool", reasoningOnToolCallMessage(viaResponses.Messages),
		"前置条件：兄弟桥本来就带 reasoning_content")
	require.Equal(t, reasoningOnToolCallMessage(viaResponses.Messages),
		reasoningOnToolCallMessage(viaAnthropic.Messages),
		"两条桥对等价历史必须产出同样的 reasoning_content 位置")
}

// 作用域守卫：不带工具调用的纯文本轮次维持现状(与兄弟桥一致 —— reasoning 只随
// 工具调用回传)，避免把 reasoning_content 撒到不需要它的上游请求上。
func TestAnthropicToChatCompletionsRequest_ThinkingWithoutToolCallsStaysDropped(t *testing.T) {
	req := &protocolanthropic.AnthropicRequest{
		Model:     "deepseek-v4-flash",
		MaxTokens: 100,
		Messages: []protocolanthropic.AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(
				`[{"type":"thinking","thinking":"secret thoughts"},{"type":"text","text":"answer"}]`)},
		},
	}

	out, err := AnthropicToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	require.Empty(t, out.Messages[0].ReasoningContent)
	require.Equal(t, `"answer"`, string(out.Messages[0].Content))

	payload, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "reasoning_content")
}
