package qoder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const qoderXMLToolCallFixture = `<tool_call>Read<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>`

const qoderJSONShellToolCallFixture = `<tool_call>{"name":"shell","arguments":{"command":"pwd","description":"Print working directory"}}</tool_call>`

const qoderDSMLToolCallFixture = `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="Bash">
<｜｜DSML｜｜parameter name="command" string="true">ls -la</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="description" string="true">List root files</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

func qoderNoIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`},
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"ls -la","description":"List files in current directory"}`},
		{Type: "tool_call_delta", ToolName: "glob", Arguments: `{"pattern":"**/*.md"}`},
		{IsDone: true},
	}
}

func qoderRepeatedIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"pwd","description":"Print working directory"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "glob", Arguments: `{"pattern":"docs/*.md"}`},
		{IsDone: true},
	}
}

var qoderCachedUsageEventForTest = qoder.SSEEvent{
	Type:             "usage",
	PromptTokens:     66637,
	CompletionTokens: 6,
	TotalTokens:      66643,
	UsageDetails: qoder.UsageDetails{
		PromptTokensDetails:     &qoder.PromptTokensDetails{CachedTokens: 66612, CacheableTokens: 19},
		CompletionTokensDetails: &qoder.CompletionTokensDetails{ReasoningTokens: 0},
	},
	HasUsage: true,
}

func TestBuildQoderPayloadFromChatCompletions(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":123,
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"assistant","content":"hi"},
			{"role":"tool","tool_call_id":"call_1","content":"tool output"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
	}`)

	payload, modelKey, err := qoder.BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "auto", modelKey)
	require.Equal(t, true, payload["stream"])
	require.Equal(t, "personal_standard", payload["aliyun_user_type"])
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	chatContext, _ := payload["chat_context"].(map[string]any)
	chatText, _ := chatContext["text"].(map[string]any)
	require.Equal(t, 123, parameters["max_tokens"])
	require.Equal(t, "auto", modelConfig["key"])
	require.Equal(t, 180000, modelConfig["max_input_tokens"])
	require.Equal(t, "hello", chatText["text"])
	business, ok := payload["business"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "1.24.2", business["version"])

	messagesRaw, ok := payload["messages"].([]any)
	require.True(t, ok)
	messages := messagesRaw
	require.Len(t, messages, 4)
	firstMsg, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "system", firstMsg["role"])
	require.Equal(t, "be terse", firstMsg["content"])
	secondMsg, ok := messages[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", secondMsg["role"])
	require.Equal(t, "", secondMsg["content"])
	userContents, ok := secondMsg["contents"].([]any)
	require.True(t, ok)
	firstContent, ok := userContents[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "hello", firstContent["text"])
	lastMsg, ok := messages[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool", lastMsg["role"])
	require.Equal(t, "tool output", lastMsg["content"])
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
}

func TestBuildQoderPayloadUsesCNModelAndClientVersion(t *testing.T) {
	body := []byte(`{
		"model":"qwen3.6-flash",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, modelKey, err := qoder.BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", qoder.SiteCN)

	require.NoError(t, err)
	require.Equal(t, "q36fmodel", modelKey)
	business, ok := payload["business"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "1.24.2", business["version"])
}

func TestBuildQoderPayloadUserSystemReplacesBuiltInSystem(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"custom system"},
			{"role":"user","content":"hello"}
		]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	systemMessages := 0
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		require.True(t, ok)
		if msg["role"] == "system" {
			systemMessages++
			require.Equal(t, "custom system", msg["content"])
		}
	}
	require.Equal(t, 1, systemMessages)
}

func TestBuildQoderPayloadFromChatCompletionsPreservesToolHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"run ls"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"ls"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"file.txt"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "call_1", toolCall["id"])
	require.Equal(t, "function", toolCall["type"])
	function := qoderFixtureValue[map[string]any](t, toolCall["function"])
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, qoderFixtureValue[string](t, function["arguments"]))

	tool := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
}

func TestBuildQoderPayloadFromChatCompletionsPreservesLegacyFunctionHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"weather"},
			{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}},
			{"role":"function","name":"get_weather","content":"sunny"}
		],
		"functions":[{"name":"get_weather","parameters":{"type":"object"}}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "get_weather", toolCall["id"])
	function := qoderFixtureValue[map[string]any](t, toolCall["function"])
	require.Equal(t, "get_weather", function["name"])
	require.JSONEq(t, `{"city":"Tokyo"}`, qoderFixtureValue[string](t, function["arguments"]))

	tool := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "get_weather", tool["tool_call_id"])
	require.Equal(t, "get_weather", tool["tool_call_call_id"])
	require.Equal(t, "get_weather", tool["name"])
}

func TestBuildQoderPayloadFromChatCompletionsMergesParallelToolHistory(t *testing.T) {
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[
			{"role":"user","content":"run both commands"},
			{"role":"assistant","reasoning_content":"Need two shell checks.","content":"","tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf a\\n\"}"}},
				{"id":"call_b","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf b\\n\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_a","content":"a\n"},
			{"role":"tool","tool_call_id":"call_b","content":"b\n"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 4)
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Empty(t, assistant["contents"], "DeepSeek reasoning_content must not be replayed as visible assistant text before tool_calls")
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 2)
	require.Equal(t, "call_a", qoderFixtureValue[map[string]any](t, toolCalls[0])["id"])
	require.Equal(t, "call_b", qoderFixtureValue[map[string]any](t, toolCalls[1])["id"])

	firstTool := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", firstTool["role"])
	require.Equal(t, "call_a", firstTool["tool_call_id"])
	require.Equal(t, "call_a", firstTool["tool_call_call_id"])
	require.Equal(t, "bash", firstTool["name"])
	secondTool := qoderFixtureValue[map[string]any](t, messages[3])
	require.Equal(t, "tool", secondTool["role"])
	require.Equal(t, "call_b", secondTool["tool_call_id"])
	require.Equal(t, "call_b", secondTool["tool_call_call_id"])
	require.Equal(t, "bash", secondTool["name"])
}

func TestBuildQoderPayloadAddsCacheControlToLastEligibleTextBlock(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral"}},{"type":"tool_use","id":"ignored","name":"bash","input":{}},{"type":"thinking","thinking":"ignore"}]},
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"ignore"},{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"},{"type":"text","text":"last"}]}
		]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	messages := qoderFixtureValue[[]any](t, payload["messages"])
	firstContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[0])["contents"])
	require.Equal(t, "ephemeral", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, firstContents[0])["cache_control"])["type"])
	lastContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[len(messages)-1])["contents"])
	lastText := qoderFixtureValue[map[string]any](t, lastContents[len(lastContents)-1])
	require.Equal(t, "last", lastText["text"])
	require.Equal(t, "ephemeral", qoderFixtureValue[map[string]any](t, lastText["cache_control"])["type"])
	for _, rawMessage := range messages {
		for _, rawBlock := range qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, rawMessage)["contents"]) {
			block := qoderFixtureValue[map[string]any](t, rawBlock)
			if block["type"] != "text" {
				require.NotContains(t, block, "cache_control")
			}
		}
	}
}

func TestBuildQoderPayloadFromAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"max_tokens":456,
		"system":[{"type":"text","text":"system one"},{"type":"text","text":"system two"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"tool result"}]}]}
		]
	}`)

	payload, modelKey, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "ultimate", modelKey)
	require.Equal(t, 456, qoderFixtureValue[map[string]any](t, payload["parameters"])["max_tokens"])
	require.Equal(t, "ultimate", qoderFixtureValue[map[string]any](t, payload["model_config"])["key"])

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "system one\nsystem two", qoderFixtureValue[map[string]any](t, messages[0])["content"])
	require.Equal(t, "", qoderFixtureValue[map[string]any](t, messages[1])["content"])
	userContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[1])["contents"])
	require.Equal(t, "hello", qoderFixtureValue[map[string]any](t, userContents[0])["text"])
	toolResult := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "t1", toolResult["tool_call_id"])
	require.Equal(t, "tool result", toolResult["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesPreservesToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"file.txt"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	assistant := qoderFixtureValue[map[string]any](t, messages[0])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "call_1", toolCall["id"])
	function := qoderFixtureValue[map[string]any](t, toolCall["function"])
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, qoderFixtureValue[string](t, function["arguments"]))

	tool := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	require.Equal(t, "file.txt", tool["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesIgnoresThinkingToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"messages":[
			{"role":"user","content":"inspect files"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"need to inspect","signature":"sig"},
				{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"README.md"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"contents"}
			]}
		],
		"tools":[{"name":"Read","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 3)
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Equal(t, "", assistant["content"])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "toolu_1", toolCall["id"])
	function := qoderFixtureValue[map[string]any](t, toolCall["function"])
	require.Equal(t, "Read", function["name"])
	require.JSONEq(t, `{"file_path":"README.md"}`, qoderFixtureValue[string](t, function["arguments"]))
	toolResult := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "toolu_1", toolResult["tool_call_id"])
	require.Equal(t, "toolu_1", toolResult["tool_call_call_id"])
	require.Equal(t, "Read", toolResult["name"])
	require.Equal(t, "contents", toolResult["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesDoesNotInventMissingToolResultID(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"tool_result","content":"file.txt"}]}
		]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 1)
	tool := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "user", tool["role"])
	require.NotContains(t, tool, "tool_calls")
	require.NotContains(t, tool, "tool_call_id")
}

func TestBuildQoderPayloadFromAnthropicMessagesConvertsTools(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"bash",
			"description":"run command",
			"input_schema":{
				"type":"object",
				"properties":{"cmd":{"type":"string"}},
				"required":["cmd"]
			}
		}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	tools := qoderFixtureValue[[]any](t, payload["tools"])
	require.Len(t, tools, 1)
	tool := qoderFixtureValue[map[string]any](t, tools[0])
	require.Equal(t, "function", tool["type"])
	require.NotContains(t, tool, "input_schema")
	function := qoderFixtureValue[map[string]any](t, tool["function"])
	require.Equal(t, "bash", function["name"])
	require.Equal(t, "run command", function["description"])
	parameters := qoderFixtureValue[map[string]any](t, function["parameters"])
	require.Equal(t, "object", parameters["type"])
	require.Contains(t, parameters, "properties")
}

func TestResolveQoderModelUsesOpus46AliasForUltimate(t *testing.T) {

	info := qoder.ResolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "system", info.Source)

	legacy := qoder.ResolveQoderModel("claude-opus-4-5")
	require.Equal(t, "claude-opus-4-5", legacy.Key)

	codex := qoder.ResolveQoderModel("gpt-5-codex")
	require.Equal(t, "gpt-5-codex", codex.Key)
}

func TestResolveQoderModelUsesQwen38MaxAlias(t *testing.T) {

	for _, site := range []qoder.Site{qoder.SiteGlobal, qoder.SiteCN} {
		info := qoder.ResolveQoderModelForSite(site, "qwen3.8-max")
		require.Equal(t, "qmodel_38max", info.Key)
		require.Equal(t, "system", info.Source)
		require.Equal(t, "Qwen3.8-Max", info.DisplayName)
	}
}

func TestResolveQoderModelUsesKimiK3Alias(t *testing.T) {

	info := qoder.ResolveQoderModel("kimi-k3")
	require.Equal(t, "kmodel_latest", info.Key)
	require.Equal(t, "system", info.Source)
	require.Equal(t, "Kimi-K3", info.DisplayName)
}

func TestResolveQoderModelUsesGLM52RouteKey(t *testing.T) {

	info := qoder.ResolveQoderModel("glm-5.2")
	require.Equal(t, "gm51model", info.Key)
	require.Equal(t, "system", info.Source)
	require.Equal(t, "GLM-5.2", info.DisplayName)
}

func TestResolveQoderModelUsesGLM53RouteKey(t *testing.T) {

	for _, site := range []qoder.Site{qoder.SiteGlobal, qoder.SiteCN} {
		info := qoder.ResolveQoderModelForSite(site, "glm-5.3")
		require.Equal(t, "gmodel", info.Key)
		require.Equal(t, "system", info.Source)
		require.Equal(t, "GLM-5.3", info.DisplayName)
	}
}

func TestResolveQoderModelDoesNotTranslateRemovedCompatibilityAliases(t *testing.T) {

	for _, model := range []string{"ultimate", "qwen3.8-max-preview", "qmodel_preview", "qwen3.5-plus", "glm-5", "glm-5.1", "kimi-k2.6"} {
		info := qoder.ResolveQoderModel(model)
		require.Equal(t, model, info.Key)
		require.Equal(t, "system", info.Source)
		require.Empty(t, info.DisplayName)
	}
}

func TestQoderResponsesPayloadPreservesControlRoleInputAsSystemPrompt(t *testing.T) {
	request, err := qoder.ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"instructions":"top-level instructions",
		"input":[
			{"type":"message","role":"system","content":[{"type":"input_text","text":"system item"}]},
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer item"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))

	require.NoError(t, err)
	require.Contains(t, request.System, "top-level instructions")
	require.Contains(t, request.System, "system item")
	require.Contains(t, request.System, "developer item")
	require.Len(t, request.Messages, 1)
	require.Equal(t, "user", request.Messages[0].Role)
	require.Equal(t, "hello", request.Messages[0].Text)
}

func TestQoderResponsesPayloadSkipsReasoningAndUnknownOutputItems(t *testing.T) {
	request, err := qoder.ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest sha?"}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"need to run curl"}]},
			{"type":"function_call","call_id":"call_a","name":"exec_command","arguments":"{\"cmd\":\"curl x\"}"},
			{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"x"}},
			{"type":"function_call_output","call_id":"call_a","output":"deadbeef"}
		]
	}`))

	require.NoError(t, err)
	require.Len(t, request.Messages, 3)
	require.Equal(t, "user", request.Messages[0].Role)
	require.Equal(t, "latest sha?", request.Messages[0].Text)
	require.Equal(t, "assistant", request.Messages[1].Role)
	require.Len(t, qoder.QoderAnySlice(request.Messages[1].Raw["tool_calls"]), 1)
	require.Equal(t, "tool", request.Messages[2].Role)
	require.Equal(t, "call_a", request.Messages[2].ToolCallID)
	require.Equal(t, "deadbeef", request.Messages[2].Text)
}

func TestQoderResponsesPayloadDropsUnansweredParallelFunctionCall(t *testing.T) {
	request, err := qoder.ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"run commands"}]},
			{"type":"function_call","call_id":"call_a","name":"bash","arguments":"{\"command\":\"printf A\"}"},
			{"type":"function_call","call_id":"call_b","name":"bash","arguments":"{\"command\":\"printf B\"}"},
			{"type":"function_call_output","call_id":"call_a","output":"A\n"}
		]
	}`))

	require.NoError(t, err)
	require.Len(t, request.Messages, 3)
	toolCalls := qoder.QoderAnySlice(request.Messages[1].Raw["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "call_a", toolCall["id"])
	require.Equal(t, "tool", request.Messages[2].Role)
	require.Equal(t, "call_a", request.Messages[2].ToolCallID)
}

func TestQoderResponsesPayloadDropsOrphanFunctionCallOutput(t *testing.T) {
	request, err := qoder.ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"function_call_output","call_id":"ghost","output":"stale output"}
		]
	}`))

	require.NoError(t, err)
	require.Len(t, request.Messages, 1)
	require.Equal(t, "user", request.Messages[0].Role)
	require.Equal(t, "hello", request.Messages[0].Text)
}

func TestQoderGatewayAssemblesResponsesKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := qoder.BuildQoderResponsesResponse("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	functionCalls := gjson.GetBytes(body, `output.#(type=="function_call")#`).Array()
	require.Len(t, functionCalls, 3, string(body))
	require.Equal(t, "Bash", functionCalls[0].Get("name").String())
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, functionCalls[0].Get("arguments").String())
	require.Equal(t, "Bash", functionCalls[1].Get("name").String())
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, functionCalls[1].Get("arguments").String())
	require.Equal(t, "glob", functionCalls[2].Get("name").String())
	require.JSONEq(t, `{"pattern":"**/*.md"}`, functionCalls[2].Get("arguments").String())
	for _, call := range functionCalls {
		require.NotContains(t, call.Get("arguments").String(), `}{`)
	}
}

func TestQoderGatewayAssemblesResponsesKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := qoder.BuildQoderResponsesResponse("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	functionCalls := gjson.GetBytes(body, `output.#(type=="function_call")#`).Array()
	require.Len(t, functionCalls, 3, string(body))
	require.Equal(t, "Bash", functionCalls[0].Get("name").String())
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, functionCalls[0].Get("arguments").String())
	require.Equal(t, "Bash", functionCalls[1].Get("name").String())
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, functionCalls[1].Get("arguments").String())
	require.Equal(t, "glob", functionCalls[2].Get("name").String())
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, functionCalls[2].Get("arguments").String())
	for _, call := range functionCalls {
		require.NotContains(t, call.Get("arguments").String(), `}{`)
	}
}

func TestQoderGatewayUsageSplitsCachedPromptTokens(t *testing.T) {
	usage := qoder.QoderUsageFromEvents([]qoder.SSEEvent{qoderCachedUsageEventForTest})
	require.Equal(t, 25, usage.InputTokens)
	require.Equal(t, 66612, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.OutputTokens)
	require.Equal(t, 0, usage.CacheCreationInputTokens)
}

func TestQoderGatewayUsageClampsCachedPromptTokens(t *testing.T) {
	event := qoderCachedUsageEventForTest
	promptDetails := *event.UsageDetails.PromptTokensDetails
	event.UsageDetails.PromptTokensDetails = &promptDetails
	event.UsageDetails.PromptTokensDetails.CachedTokens = 70000
	usage := qoder.QoderUsageFromEvents([]qoder.SSEEvent{event})
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 70000, usage.CacheReadInputTokens)
}

func TestQoderGatewayUsageKeepsOldBehaviorWhenDetailsMissing(t *testing.T) {
	event := qoder.SSEEvent{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true}
	usage := qoder.QoderUsageFromEvents([]qoder.SSEEvent{event})
	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 0, usage.CacheReadInputTokens)
	require.Equal(t, 34, usage.OutputTokens)
}

func TestQoderConversationStoreExpiresState(t *testing.T) {
	store := qoder.NewQoderConversationStore(5 * time.Millisecond)
	messages := []qoder.QoderMessage{{Role: "user", Text: "hello"}}

	plan := store.Plan("key", "", nil, messages)
	require.NotNil(t, plan)
	plan.Commit()

	time.Sleep(10 * time.Millisecond)

	next := store.Plan("key", "", nil, messages)
	require.False(t, next.Reused)
	require.True(t, next.IncludeSystem)
	require.Len(t, next.MessagesToSend, 1)
}

func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	choices := qoderFixtureValue[[]any](t, decoded["choices"])
	message := qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, choices[0])["message"])
	require.Equal(t, "Hello", message["content"])
	usage := qoderFixtureValue[map[string]any](t, decoded["usage"])
	require.Equal(t, float64(12), usage["prompt_tokens"])
	require.Equal(t, float64(34), usage["completion_tokens"])
	require.Equal(t, float64(46), usage["total_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionWithToolCalls(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.True(t, gjson.GetBytes(body, "choices.0.message.content").Exists())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionMapsToolNameToDeclaredOpenAITool(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}
	tools := []any{map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":       "bash",
			"parameters": map[string]any{"type": "object"},
		},
	}}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events, qoder.QoderDeclaredToolNameMapper(tools))
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionMergesIndexDriftForSameCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(1), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsParallelCallIndexes(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"path":"a"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.JSONEq(t, `{"path":"b"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesJSONTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.NotContains(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String(), "{")
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesDSMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"ls -la","description":"List root files"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesSplitXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "<tool_"},
		{Type: "text_delta", Text: "call>Re"},
		{Type: "text_delta", Text: "ad<arg_value><arg_key>file_path</arg_key>"},
		{Type: "text_delta", Text: "<arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>"},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsMixedXMLToolText(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "I will inspect it.\n"},
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{Type: "text_delta", Text: "\nWaiting for result."},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "I will inspect it.\n\nWaiting for result.", gjson.GetBytes(body, "choices.0.message.content").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithThinking(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	content := qoderFixtureValue[[]any](t, decoded["content"])
	require.Len(t, content, 2)
	thinkingBlock := qoderFixtureValue[map[string]any](t, content[0])
	require.Equal(t, "thinking", thinkingBlock["type"])
	require.Equal(t, "hidden thought", thinkingBlock["thinking"])
	textBlock := qoderFixtureValue[map[string]any](t, content[1])
	require.Equal(t, "Hi", textBlock["text"])
	usage := qoderFixtureValue[map[string]any](t, decoded["usage"])
	require.Equal(t, float64(12), usage["input_tokens"])
	require.Equal(t, float64(34), usage["output_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithToolUse(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "content.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.cmd").String())
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageRejectsMalformedToolArguments(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("auto", events)

	require.Error(t, err)
	require.Nil(t, body)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageSkipsTypeOnlyPlaceholder(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "end_turn", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, "text", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.False(t, gjson.GetBytes(body, `content.#(type=="tool_use")`).Exists(), string(body))
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, int64(3), gjson.GetBytes(body, "content.#").Int(), string(body))
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String(), string(body))
	require.Equal(t, "pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm", gjson.GetBytes(body, "content.0.input.command").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.1.name").String(), string(body))
	require.Equal(t, "ls -la", gjson.GetBytes(body, "content.1.input.command").String(), string(body))
	require.Equal(t, "glob", gjson.GetBytes(body, "content.2.name").String(), string(body))
	require.Equal(t, "**/*.md", gjson.GetBytes(body, "content.2.input.pattern").String(), string(body))
	require.False(t, gjson.GetBytes(body, "content.0.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.1.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.2.input.raw").Exists(), string(body))
	require.NotContains(t, string(body), `}{`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {
	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, int64(3), gjson.GetBytes(body, "content.#").Int(), string(body))
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String(), string(body))
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.command").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.1.name").String(), string(body))
	require.Equal(t, "printf OPENCODE_PARALLEL_OK", gjson.GetBytes(body, "content.1.input.command").String(), string(body))
	require.Equal(t, "glob", gjson.GetBytes(body, "content.2.name").String(), string(body))
	require.Equal(t, "docs/*.md", gjson.GetBytes(body, "content.2.input.pattern").String(), string(body))
	require.False(t, gjson.GetBytes(body, "content.0.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.1.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.2.input.raw").Exists(), string(body))
	require.NotContains(t, string(body), `}{`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "/workspace/campus-navigation/README.md", gjson.GetBytes(body, "content.0.input.file_path").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesJSONTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "Print working directory", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), `"name":"{\"name\"`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesDSMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "ls -la", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "List root files", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

func TestQoderGatewayReadsWrappedSSE(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":3,\\\"completion_tokens\\\":4,\\\"total_tokens\\\":7}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	events, err := qoder.ReadQoderSSEEvents(resp)
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Equal(t, "reasoning_delta", events[0].Type)
	require.Equal(t, "hidden thought", events[0].Text)
	require.Equal(t, "text_delta", events[1].Type)
	require.Equal(t, "Hi", events[1].Text)
	require.True(t, events[2].HasUsage)
	require.Equal(t, 3, events[2].PromptTokens)
	require.Equal(t, 4, events[2].CompletionTokens)
	require.True(t, events[3].IsDone)
}

func TestQoderGatewayScannerStopsWhenResultSendIsCanceled(t *testing.T) {
	line := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat(line, 10)))}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan qoder.QoderEventResult, 1)
	done := make(chan struct{})

	go func() {
		qoder.ScanQoderEvents(ctx, resp, results)
		close(done)
	}()

	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("scanner did not emit first event")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scanner goroutine did not exit after context cancellation")
	}
}

func TestQoderGatewayReadsWrappedSSEUpstreamError(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := qoder.ReadQoderSSEEvents(resp)
	require.Error(t, err)
	require.Empty(t, events)
	var apiErr *qoder.APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 403, apiErr.StatusCode)
	require.Equal(t, "101", apiErr.Code)
	require.Equal(t, "Signature invalid", apiErr.Message)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", apiErr.Error())
}

// qoderFixtureValue 明确验证解码夹具的类型，保留原字段断言失败语义。
func qoderFixtureValue[T any](t *testing.T, raw any) T {
	t.Helper()
	value, ok := raw.(T)
	require.True(t, ok, "unexpected decoded fixture type: %T", raw)
	return value
}
