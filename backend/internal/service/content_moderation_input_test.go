package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Agent 工具循环只审核模型调用后返回的工具结果，不重复审核发起工具调用的历史用户消息。

func TestExtractContentModerationInput_AnthropicAgentToolLoopExtractsToolResult(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"调用一下天气工具"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"晴 25 度"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "晴 25 度", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

func TestExtractContentModerationInput_AnthropicFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q1", input.Text)
}

func TestExtractContentModerationInput_AnthropicMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_AnthropicStreamResendExtractsResend(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"原问题"},
			{"role":"assistant","content":"部分回答……"},
			{"role":"user","content":"重发"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "重发", input.Text)
}

func TestExtractContentModerationInput_OpenAIChatAgentToolLoopExtractsToolResult(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"system","content":"sys"},
			{"role":"user","content":"列出我的订单"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"orders","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"[]"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "[]", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

func TestExtractContentModerationInput_OpenAIChatMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_GeminiAgentToolLoopExtractsFunctionResponse(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"查询天气"}]},
			{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"temp":25}}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.JSONEq(t, `{"temp":25}`, input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

func TestExtractContentModerationInput_GeminiFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"你好"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "你好", input.Text)
}

func TestExtractContentModerationInput_GeminiMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"Q1"}]},
			{"role":"model","parts":[{"text":"A1"}]},
			{"role":"user","parts":[{"text":"Q2"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_ResponsesAgentToolLoopExtractsToolOutput(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"运行测试"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "all passed", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

func TestExtractContentModerationPromptExcerpt_ResponsesUsesCurrentToolOutput(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"检查这段高风险请求 sk-proj-1234567890abcdef"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我先看一下"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)
	excerpt := ExtractContentModerationPromptExcerpt(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "all passed", input.Text)
	require.Equal(t, "all passed", excerpt)
}

func TestExtractContentModerationInput_ResponsesLastUserMessageExtracted(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "latest", input.Text)
}

func TestExtractContentModerationInput_ResponsesLastIsAssistantSkipped(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_ChatCollectsParallelToolResultsAndDeduplicatesImages(t *testing.T) {
	body := []byte(`{
		"messages":[
			{"role":"user","content":"old request"},
			{"role":"assistant","tool_calls":[{"id":"one"},{"id":"two"}]},
			{"role":"tool","tool_call_id":"one","content":{"result":"first","image_url":"https://example.com/result.png"}},
			{"role":"tool","tool_call_id":"two","content":[{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"https://example.com/result.png"}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Contains(t, input.Text, `"result":"first"`)
	require.Contains(t, input.Text, `"text":"second"`)
	require.NotContains(t, input.Text, "old request")
	require.Equal(t, []string{"https://example.com/result.png"}, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

func TestExtractContentModerationInput_ResponsesCollectsAllSupportedToolOutputs(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_1","name":"run","arguments":"{}"},
			{"type":"function_call_output","output":{"one":1}},
			{"type":"custom_tool_call_output","output":"two"},
			{"type":"mcp_tool_call_output","output":"three"},
			{"type":"tool_search_output","output":"four"},
			{"type":"computer_call_output","output":"five"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Contains(t, input.Text, `"one":1`)
	for _, value := range []string{"two", "three", "four", "five"} {
		require.Contains(t, input.Text, value)
	}
	require.Equal(t, ContentModerationSourceTool, input.Source)
}
