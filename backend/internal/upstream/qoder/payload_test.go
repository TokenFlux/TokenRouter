package qoder

import (
	"encoding/json"
	"maps"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// Payload fixtures compare protocol history with Qoder's scalar content,
// structured text, and call/result fields. Site input limits and thinking
// controls are checked at every mirrored configuration path, while Responses
// histories exercise retained matches and removal of incomplete tool pairs.

// TestQoderHighestContextFlowsThroughAllProtocols parses Qwen input through
// Chat, Responses, and Messages. It checks the same route and million-token
// runtime configuration in every resulting global payload.
func TestQoderHighestContextFlowsThroughAllProtocols(t *testing.T) {
	tests := []struct {
		name  string                                    // fixture case label
		body  []byte                                    // client request bytes
		parse func([]byte) (QoderPayloadRequest, error) // protocol parsing callback
	}{
		{
			name:  "Chat Completions",
			body:  []byte(`{"model":"qwen3.8-max","messages":[{"role":"user","content":"hello"}]}`),
			parse: ParseQoderChatCompletionsPayload,
		},
		{
			name:  "Responses",
			body:  []byte(`{"model":"qwen3.8-max","input":"hello"}`),
			parse: ParseQoderResponsesPayload,
		},
		{
			name:  "Anthropic Messages",
			body:  []byte(`{"model":"qwen3.8-max","max_tokens":1024,"messages":[{"role":"user","content":"hello"}]}`),
			parse: ParseQoderAnthropicMessagesPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := tt.parse(tt.body)
			require.NoError(t, err)
			request.Site = SiteGlobal
			request.Capabilities = observedPayloadModel(t, SiteGlobal, request.Model)
			payload, modelKey, buildErr := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
			require.NoError(t, buildErr)

			require.Equal(t, "qmodel_38max", modelKey)
			assertQoderContextCapabilityForTest(t, payload, 1000000, true)
		})
	}
}

// TestQoderDesktopPayloadUsesCanonicalContent reproduces the current desktop
// request shape for Cantus. User text stays in content, chat prompts remain
// strings, and observed display metadata replaces obsolete fixed model hints.
func TestQoderDesktopPayloadUsesCanonicalContent(t *testing.T) {
	request, err := ParseQoderChatCompletionsPayload([]byte(`{"model":"cmodel","messages":[{"role":"user","content":"hello"}]}`))
	require.NoError(t, err)
	request.Capabilities = observedPayloadModel(t, SiteGlobal, request.Model)
	payload, model, buildErr := BuildQoderPayloadWithOptions(request, "fixture", request.Messages, true, true)
	require.NoError(t, buildErr)
	require.Equal(t, "cmodel", model)
	config := qoderFixtureValue[map[string]any](t, payload["model_config"])
	require.Equal(t, "Cantus", config["display_name"])
	require.Equal(t, true, config["is_vl"])
	require.Equal(t, "", payload["aliyun_user_type"])
	chat := qoderFixtureValue[map[string]any](t, payload["chat_context"])
	require.Equal(t, "hello", chat["text"])
	require.Equal(t, "hello", qoderFixtureValue[map[string]any](t, chat["extra"])["originalContent"])
	message := qoderFixtureValue[map[string]any](t, qoderFixtureValue[[]any](t, payload["messages"])[0])
	require.NotContains(t, message, "contents")
	require.NotContains(t, message, "response_meta")
	require.Equal(t, "hello", qoderFixtureValue[map[string]any](t, qoderFixtureValue[[]any](t, message["content"])[0])["text"])
	require.Equal(t, true, qoderFixtureValue[map[string]any](t, payload["parameters"])["enable_thinking"])
}

// TestQoderPayloadCacheMarkerDoesNotMutateInput attaches an automatic cache
// marker to outgoing text. The original text block remains unchanged for
// conversation fingerprinting and subsequent attempt preparation.
func TestQoderPayloadCacheMarkerDoesNotMutateInput(t *testing.T) {
	block := map[string]any{"type": "text", "text": "hello"}
	message := QoderPayloadMessageFromMessage(QoderMessage{Role: "user", Text: "hello", TextContent: []map[string]any{block}})
	AddQoderEphemeralCacheControl([]any{message})
	require.NotContains(t, block, "cache_control")
	require.Contains(t, qoderFixtureValue[map[string]any](t, qoderFixtureValue[[]any](t, message["content"])[0]), "cache_control")
}

// TestQoderContextCapabilityUsesSiteAndRouteFallback compares observed selectable
// routes with unknown and retired names. Only observed capabilities introduce
// runtime context fields; a missing observation keeps the execution default.
func TestQoderContextCapabilityUsesSiteAndRouteFallback(t *testing.T) {
	for _, test := range []struct {
		model      string // request model identifier
		limit      int    // expected input tokens
		selectable bool   // expected context override
	}{{"auto", 1000000, true}, {"kimi-k2.8-preview", 1000000, true}, {"minimax-m3.1-flash", 1000000, true}, {"custom-route", 200000, false}, {"kimi-k2.7-code", 200000, false}} {
		request, err := ParseQoderChatCompletionsPayload([]byte(`{"model":"` + test.model + `","messages":[{"role":"user","content":"hello"}]}`))
		require.NoError(t, err)
		request.Capabilities = observedPayloadModel(t, SiteGlobal, test.model)
		payload, _, buildErr := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
		require.NoError(t, buildErr)
		assertQoderContextCapabilityForTest(t, payload, test.limit, test.selectable)
	}
}

// TestQoderContextCapabilityMergesWithThinkingOverride enables thinking on a
// runtime-context route. It checks the mapped max effort and million-token limit
// coexist in parameters and ideModelConfigOverride.
func TestQoderContextCapabilityMergesWithThinkingOverride(t *testing.T) {
	request, err := ParseQoderChatCompletionsPayload([]byte(`{"model":"deepseek-v4-pro","reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`))
	require.NoError(t, err)
	request.Capabilities = observedPayloadModel(t, SiteGlobal, request.Model)
	payload, _, buildErr := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
	require.NoError(t, buildErr)
	assertQoderContextCapabilityForTest(t, payload, 1000000, true)

	parameters := requireQoderPayloadMapForTest(t, payload["parameters"], "parameters")
	require.Equal(t, "high", parameters["reasoning_effort"])
	chatContext := requireQoderPayloadMapForTest(t, payload["chat_context"], "chat_context")
	extra := requireQoderPayloadMapForTest(t, chatContext["extra"], "chat_context.extra")
	runtimeOverride := requireQoderPayloadMapForTest(t, extra["ideModelConfigOverride"], "chat_context.extra.ideModelConfigOverride")
	require.Equal(t, "high", runtimeOverride["reasoning_effort"])
	require.Equal(t, 1000000, runtimeOverride["max_input_tokens"])
}

// assertQoderContextCapabilityForTest checks model input limit and optional
// runtime configuration locations. Runtime-enabled fixtures need both parameter
// and IDE overrides; fixed-route fixtures require both to remain absent.
func assertQoderContextCapabilityForTest(t *testing.T, payload map[string]any, wantTokens int, wantRuntime bool) {
	t.Helper()
	modelConfig := requireQoderPayloadMapForTest(t, payload["model_config"], "model_config")
	require.EqualValues(t, wantTokens, modelConfig["max_input_tokens"])

	parameters := requireQoderPayloadMapForTest(t, payload["parameters"], "parameters")
	contextLength, hasContextLength := parameters["context_length"]
	chatContext := requireQoderPayloadMapForTest(t, payload["chat_context"], "chat_context")
	extra := requireQoderPayloadMapForTest(t, chatContext["extra"], "chat_context.extra")
	runtimeOverride, hasRuntimeOverride := extra["ideModelConfigOverride"].(map[string]any)
	if wantRuntime {
		require.True(t, hasContextLength)
		require.EqualValues(t, wantTokens, contextLength)
		require.True(t, hasRuntimeOverride)
		require.EqualValues(t, wantTokens, runtimeOverride["max_input_tokens"])
		return
	}

	require.False(t, hasContextLength)
	require.False(t, hasRuntimeOverride)
}

// requireQoderPayloadMapForTest requires a decoded object and returns it for
// payload assertions. Unexpected values fail with the supplied path, locating
// the incorrect nested representation in the fixture.
func requireQoderPayloadMapForTest(t *testing.T, value any, path string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "%s 应为 JSON 对象", path)
	return result
}

// TestBuildQoderPayloadFromChatCompletions converts control, user, assistant,
// and tool messages with a declared tool. It checks output budget,
// model/context/business fields, user text representation, and retained tool
// content.
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

	payload, modelKey, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
	require.NoError(t, err)
	require.Equal(t, "auto", modelKey)
	require.Equal(t, true, payload["stream"])
	require.Equal(t, "", payload["aliyun_user_type"])
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	chatContext, _ := payload["chat_context"].(map[string]any)
	chatText, _ := chatContext["text"].(string)
	require.Equal(t, 123, parameters["max_tokens"])
	require.Equal(t, "auto", modelConfig["key"])
	require.Equal(t, 200000, modelConfig["max_input_tokens"])
	require.Equal(t, "hello", chatText)
	business, ok := payload["business"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "1.1.86-hotfix.5.3", business["version"])

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
	require.NotContains(t, secondMsg, "contents")
	userContents, ok := secondMsg["content"].([]any)
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

// TestBuildQoderPayloadUsesCNModelAndClientVersion converts the CN-only Qwen
// Flash alias into a payload. It checks the CN route key and business client
// version emitted for that site.
func TestBuildQoderPayloadUsesCNModelAndClientVersion(t *testing.T) {
	body := []byte(`{
		"model":"qwen3.8-flash",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	request, err := ParseQoderChatCompletionsPayload(body)
	require.NoError(t, err)
	request.Site = SiteCN
	request.Capabilities = observedPayloadModel(t, SiteCN, request.Model)
	payload, modelKey, buildErr := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
	require.NoError(t, buildErr)

	require.NoError(t, err)
	require.Equal(t, "qfmodel", modelKey)
	business, ok := payload["business"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "1.1.86-hotfix.5.3", business["version"])
}

// TestBuildQoderPayloadUserSystemReplacesBuiltInSystem supplies one user-defined
// system message and one user turn. It requires exactly one system message
// containing the supplied text in the outgoing history.
func TestBuildQoderPayloadUserSystemReplacesBuiltInSystem(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"custom system"},
			{"role":"user","content":"hello"}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
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

// TestBuildQoderPayloadFromChatCompletionsPreservesToolHistory converts modern
// assistant/tool history with object arguments. It checks serialized call
// arguments and both Gateway result-ID fields plus the tool name.
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

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
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

// TestBuildQoderPayloadFromChatCompletionsPreservesLegacyFunctionHistory
// converts legacy function_call and function-role output. It checks
// function-name call identity, argument JSON, and matching result IDs and name.
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

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
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

// TestBuildQoderPayloadFromChatCompletionsMergesParallelToolHistory converts two
// assistant calls and their separately identified results. It checks both call
// IDs, enriched result names, and absence of reasoning text in assistant
// contents.
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

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 4)
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Empty(t, assistant["content"], "DeepSeek reasoning_content must not be replayed as visible assistant text before tool_calls")
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

// TestBuildQoderPayloadAddsCacheControlToLastEligibleTextBlock mixes text, tool
// results, and omitted thinking history. It checks retained earlier cache
// control, an ephemeral marker on final text, and absent markers on non-text
// contents.
func TestBuildQoderPayloadAddsCacheControlToLastEligibleTextBlock(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral"}},{"type":"tool_use","id":"ignored","name":"bash","input":{}},{"type":"thinking","thinking":"ignore"}]},
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"ignore"},{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"},{"type":"text","text":"last"}]}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	messages := qoderFixtureValue[[]any](t, payload["messages"])
	firstContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[0])["content"])
	require.Equal(t, "ephemeral", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, firstContents[0])["cache_control"])["type"])
	lastContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[len(messages)-1])["content"])
	lastText := qoderFixtureValue[map[string]any](t, lastContents[len(lastContents)-1])
	require.Equal(t, "last", lastText["text"])
	require.Equal(t, "ephemeral", qoderFixtureValue[map[string]any](t, lastText["cache_control"])["type"])
	for _, rawMessage := range messages {
		blocks, _ := qoderFixtureValue[map[string]any](t, rawMessage)["content"].([]any)
		for _, rawBlock := range blocks {
			block := qoderFixtureValue[map[string]any](t, rawBlock)
			if block["type"] != "text" {
				require.NotContains(t, block, "cache_control")
			}
		}
	}
}

// TestBuildQoderPayloadFromAnthropicMessages converts text-block system and
// mixed user/tool-result content. It checks alias, output budget, joined system
// text, user contents, and result identity.
func TestBuildQoderPayloadFromAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"max_tokens":456,
		"system":[{"type":"text","text":"system one"},{"type":"text","text":"system two"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"tool result"}]}]}
		]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", modelKey)
	require.Equal(t, 456, qoderFixtureValue[map[string]any](t, payload["parameters"])["max_tokens"])
	require.Equal(t, "claude-opus-4-6", qoderFixtureValue[map[string]any](t, payload["model_config"])["key"])

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "system one\nsystem two", qoderFixtureValue[map[string]any](t, messages[0])["content"])
	require.NotContains(t, qoderFixtureValue[map[string]any](t, messages[1]), "contents")
	userContents := qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[1])["content"])
	require.Equal(t, "hello", qoderFixtureValue[map[string]any](t, userContents[0])["text"])
	toolResult := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "t1", toolResult["tool_call_id"])
	require.Equal(t, "tool result", toolResult["content"])
}

// TestBuildQoderPayloadFromAnthropicMessagesPreservesToolUseHistory converts an
// assistant tool-use block followed by its result. It checks call
// ID/name/argument representation and the matching result fields and text.
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

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
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

// TestBuildQoderPayloadFromAnthropicMessagesIgnoresThinkingToolUseHistory mixes
// assistant thinking and tool use before a result. It checks empty visible
// assistant text with retained call identity, arguments, and result association.
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

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
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

// TestBuildQoderPayloadFromAnthropicMessagesDoesNotInventMissingToolResultID
// supplies a result block without tool_use_id. It checks that conversion keeps
// user role and creates neither call metadata nor an invented result ID.
func TestBuildQoderPayloadFromAnthropicMessagesDoesNotInventMissingToolResultID(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"tool_result","content":"file.txt"}]}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 1)
	tool := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "user", tool["role"])
	require.NotContains(t, tool, "tool_calls")
	require.NotContains(t, tool, "tool_call_id")
}

// TestBuildQoderPayloadFromAnthropicMessagesConvertsTools supplies a named
// Messages tool with description and input schema. It checks Qoder function
// type, nested name/schema, and removal of the Messages input_schema field.
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

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
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

// TestQoderResponsesPayloadPreservesControlRoleInputAsSystemPrompt combines
// instructions with system/developer input items. It checks all control text in
// system and one remaining user history message.
func TestQoderResponsesPayloadPreservesControlRoleInputAsSystemPrompt(t *testing.T) {
	request, err := ParseQoderResponsesPayload([]byte(`{
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

// TestQoderResponsesPayloadSkipsReasoningAndUnknownOutputItems mixes user text,
// reasoning, function call, search output, and matched result. It checks only
// user, assistant call, and tool result survive normalization.
func TestQoderResponsesPayloadSkipsReasoningAndUnknownOutputItems(t *testing.T) {
	request, err := ParseQoderResponsesPayload([]byte(`{
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
	require.Len(t, QoderAnySlice(request.Messages[1].Raw["tool_calls"]), 1)
	require.Equal(t, "tool", request.Messages[2].Role)
	require.Equal(t, "call_a", request.Messages[2].ToolCallID)
	require.Equal(t, "deadbeef", request.Messages[2].Text)
}

// TestQoderResponsesPayloadDropsUnansweredParallelFunctionCall supplies two
// parallel calls but a result for only one. It checks the retained call ID and
// its immediately associated result in normalized history.
func TestQoderResponsesPayloadDropsUnansweredParallelFunctionCall(t *testing.T) {
	request, err := ParseQoderResponsesPayload([]byte(`{
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
	toolCalls := QoderAnySlice(request.Messages[1].Raw["tool_calls"])
	require.Len(t, toolCalls, 1)
	toolCall := qoderFixtureValue[map[string]any](t, toolCalls[0])
	require.Equal(t, "call_a", toolCall["id"])
	require.Equal(t, "tool", request.Messages[2].Role)
	require.Equal(t, "call_a", request.Messages[2].ToolCallID)
}

// TestQoderResponsesRejectsOrphanOutput requires an owning call in the current
// request. Accepting an orphan would silently discard a client tool result
// or send it without its corresponding assistant turn.
func TestQoderResponsesRejectsOrphanOutput(t *testing.T) {
	_, err := ParseQoderResponsesPayload([]byte(`{"model":"auto","input":[{"type":"function_call_output","call_id":"missing","output":"result"}]}`))
	var invalid *RequestError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "missing_tool_call", invalid.Code)
}

// TestQoderChatSystemTextPreservesDeveloperMessages supplies system, developer,
// and user turns. It checks both control texts and their newline-joined order
// while the user text stays outside system.
func TestQoderChatSystemTextPreservesDeveloperMessages(t *testing.T) {
	messages := []protocolopenai.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := QoderChatSystemText(messages)

	require.Contains(t, systemText, "You are helpful")
	require.Contains(t, systemText, "Be concise")
	require.Equal(t, "You are helpful\nBe concise", systemText)
}

// TestQoderChatSystemTextOnlySystemMessages supplies a system turn followed by
// ordinary user text. It checks the resulting prompt equals the system text with
// no user contribution.
func TestQoderChatSystemTextOnlySystemMessages(t *testing.T) {
	messages := []protocolopenai.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := QoderChatSystemText(messages)

	require.Equal(t, "You are helpful", systemText)
}

// TestQoderChatSystemTextOnlyDeveloperMessages supplies developer instructions
// followed by ordinary user text. It checks that developer text alone becomes
// the Qoder system prompt.
func TestQoderChatSystemTextOnlyDeveloperMessages(t *testing.T) {
	messages := []protocolopenai.ChatMessage{
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := QoderChatSystemText(messages)

	require.Equal(t, "Be concise", systemText)
}

// TestQoderChatSystemTextEmptyWhenNoSystemOrDeveloper supplies only user and
// assistant conversation turns. It checks an empty system prompt so visible
// history cannot become control text.
func TestQoderChatSystemTextEmptyWhenNoSystemOrDeveloper(t *testing.T) {
	messages := []protocolopenai.ChatMessage{
		{Role: "user", Content: []byte(`"Hello"`)},
		{Role: "assistant", Content: []byte(`"Hi"`)},
	}

	systemText := QoderChatSystemText(messages)

	require.Equal(t, "", systemText)
}

// TestQoderChatSystemTextMultipleDeveloperMessages supplies system instructions
// and two developer turns before user input. It checks every control message
// contributes text to the constructed system prompt.
func TestQoderChatSystemTextMultipleDeveloperMessages(t *testing.T) {
	messages := []protocolopenai.ChatMessage{
		{Role: "system", Content: []byte(`"You are helpful"`)},
		{Role: "developer", Content: []byte(`"Be concise"`)},
		{Role: "developer", Content: []byte(`"Use examples"`)},
		{Role: "user", Content: []byte(`"Hello"`)},
	}

	systemText := QoderChatSystemText(messages)

	require.Contains(t, systemText, "You are helpful")
	require.Contains(t, systemText, "Be concise")
	require.Contains(t, systemText, "Use examples")
}

// TestQoderChatCompletionsRespectsMaxCompletionTokens supplies positive
// completion and legacy output limits together. It checks that the parsed budget
// uses the completion-specific value selected by protocol precedence.
func TestQoderChatCompletionsRespectsMaxCompletionTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_completion_tokens": 1000,
		"max_tokens": 2000
	}`)

	req, err := ParseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 1000, req.MaxTokens, "max_completion_tokens should take precedence")
}

// TestQoderChatCompletionsFallsBackToMaxTokens supplies a positive legacy limit
// with no completion-specific limit. It checks that parsed output budget retains
// the provided max_tokens value.
func TestQoderChatCompletionsFallsBackToMaxTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 2000
	}`)

	req, err := ParseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 2000, req.MaxTokens, "should use max_tokens when max_completion_tokens absent")
}

// TestQoderChatCompletionsUsesDefaultWhenBothAbsent parses user input with both
// Chat output-limit fields omitted. It checks the resulting budget selects
// QoderDefaultMaxTokens for payload construction.
func TestQoderChatCompletionsUsesDefaultWhenBothAbsent(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	req, err := ParseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, QoderDefaultMaxTokens, req.MaxTokens, "should use default when both absent")
}

// TestQoderChatCompletionsIgnoresZeroMaxTokens combines a positive completion
// limit with zero legacy max_tokens. It checks that the positive completion
// budget remains selected during Chat parsing.
func TestQoderChatCompletionsIgnoresZeroMaxTokens(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_completion_tokens": 1000,
		"max_tokens": 0
	}`)

	req, err := ParseQoderChatCompletionsPayload(body)

	require.NoError(t, err)
	require.Equal(t, 1000, req.MaxTokens, "should ignore max_tokens when it's 0")
}

// assertQoderThinkingPayload checks both reasoning switches and every optional
// effort configuration copy. It requires the expected effort text when enabled
// for that fixture, or absence of all four effort paths.
func assertQoderThinkingPayload(t *testing.T, payload map[string]any, enabled bool, effort string, hasEffort bool) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(raw, "model_config.is_reasoning").Exists())
	require.Equal(t, enabled, gjson.GetBytes(raw, "model_config.is_reasoning").Bool())
	require.True(t, gjson.GetBytes(raw, "chat_context.extra.modelConfig.is_reasoning").Exists())
	require.Equal(t, enabled, gjson.GetBytes(raw, "chat_context.extra.modelConfig.is_reasoning").Bool())

	paths := []string{
		"parameters.reasoning_effort",
		"model_config.reasoning_effort",
		"chat_context.extra.modelConfig.reasoning_effort",
		"chat_context.extra.ideModelConfigOverride.reasoning_effort",
	}
	for _, path := range paths {
		if hasEffort {
			require.Equal(t, effort, gjson.GetBytes(raw, path).String(), path)
			continue
		}
		require.False(t, gjson.GetBytes(raw, path).Exists(), path)
	}
}

// TestQoderThinkingDirectiveFromBody covers effort normalization, explicit
// disable, budgets, switches, and invalid labels. It checks the normalized
// directive selected by each control combination and precedence case.
func TestQoderThinkingDirectiveFromBody(t *testing.T) {
	tests := []struct {
		name        string                 // fixture case label
		body        string                 // client request bytes
		effortPaths []string               // ordered effort selectors
		want        QoderThinkingDirective // expected thinking directive
	}{
		{name: "missing stays unspecified", body: `{}`, effortPaths: []string{"reasoning.effort"}},
		{name: "minimal normalizes low", body: `{"reasoning":{"effort":"minimal"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "low"}},
		{name: "low stays low", body: `{"reasoning_effort":"LOW"}`, effortPaths: []string{"reasoning_effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "low"}},
		{name: "medium stays medium", body: `{"reasoning":{"effort":"medium"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "medium"}},
		{name: "high stays high", body: `{"reasoning":{"effort":"high"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "high"}},
		{name: "very high aliases map xhigh", body: `{"reasoning":{"effort":"very_high"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "xhigh"}},
		{name: "positive budget maps max", body: `{"thinking":{"budget_tokens":1}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "max"}},
		{name: "zero budget stays disabled", body: `{"thinking":{"budget_tokens":0}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "enabled uses model default", body: `{"thinking":{"type":"enabled"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true}},
		{name: "adaptive uses model default", body: `{"thinking":{"type":"adaptive"}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true}},
		{name: "explicit effort beats budget", body: `{"reasoning":{"effort":"low"},"thinking":{"budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "low"}},
		{name: "disabled beats effort and budget", body: `{"reasoning":{"effort":"max"},"thinking":{"type":"disabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true}},
		{name: "none effort beats enabled", body: `{"reasoning":{"effort":"none"},"thinking":{"type":"enabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true}},
		{name: "invalid effort falls back to budget", body: `{"reasoning":{"effort":"banana"},"thinking":{"budget_tokens":8}}`, effortPaths: []string{"reasoning.effort"}, want: QoderThinkingDirective{Specified: true, Enabled: true, Effort: "max"}},
		{name: "invalid effort alone stays disabled", body: `{"reasoning":{"effort":"banana"}}`, effortPaths: []string{"reasoning.effort"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, QoderThinkingDirectiveFromBody([]byte(tt.body), tt.effortPaths...))
		})
	}
}

// TestQoderThinkingParsersUseProtocolNativeFields parses native control fields
// from Chat, Responses, and Messages. It checks effort normalization and
// confirms an unknown Messages effort reaches Qoder's disabled directive.
func TestQoderThinkingParsersUseProtocolNativeFields(t *testing.T) {
	chat, err := ParseQoderChatCompletionsPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, QoderThinkingDirective{Specified: true, Enabled: true, Effort: "medium"}, chat.Thinking)

	responses, err := ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning":{"effort":"xhigh"},
		"input":"hello"
	}`))
	require.NoError(t, err)
	require.Equal(t, QoderThinkingDirective{Specified: true, Enabled: true, Effort: "xhigh"}, responses.Thinking)

	// The fixture supplies both effort and budget. Check the effort first so native
	// Messages control precedence remains observable after tool-definition bridge
	// conversion.
	messages, err := ParseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"low"},
		"thinking":{"type":"enabled","budget_tokens":32768},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, QoderThinkingDirective{Specified: true, Enabled: true, Effort: "low"}, messages.Thinking)

	// Unknown effort is handled by Qoder's control normalizer. This request
	// verifies the shared tool converter does not reject it before that handling
	// runs.
	messages, err = ParseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"ultra"},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, QoderThinkingDirective{}, messages.Thinking)
}

// TestBuildQoderAnthropicThinkingPayloadDefaultsToGlobalSite builds Messages
// input without a site argument. It checks the global DeepSeek route and enabled
// max effort across payload configuration copies.
func TestBuildQoderAnthropicThinkingPayloadDefaultsToGlobalSite(t *testing.T) {
	// The constructor without a site applies global thinking capabilities.
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"thinking":{"type":"enabled","budget_tokens":1},
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "deepseek-v4-pro", modelKey)
	assertQoderThinkingPayload(t, payload, false, "", false)
}

// TestBuildQoderThinkingPayloadBySiteAndModelCapability applies current model
// metadata to Chat input on both sites. Defaults, disable intent, effort
// promotion, and unsupported raw routes retain consistent mirrored fields.
func TestBuildQoderThinkingPayloadBySiteAndModelCapability(t *testing.T) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		for _, test := range []struct {
			model   string         // current public alias
			extra   map[string]any // native reasoning controls
			enabled bool           // expected thinking switch
			effort  string         // expected encoded effort
		}{{"qwen3.8-max", nil, true, "medium"}, {"qwen3.8-max", map[string]any{"reasoning_effort": "high"}, true, "xhigh"}, {"qwen4-max", map[string]any{"reasoning_effort": "low"}, true, "low"}, {"qwen4-max", map[string]any{"thinking": map[string]any{"type": "disabled"}}, false, "none"}, {"deepseek-v4-pro", map[string]any{"reasoning_effort": "low"}, true, "high"}, {"glm-5.3", map[string]any{"reasoning_effort": "medium"}, true, "high"}, {"minimax-m3.1-flash", nil, true, "xhigh"}} {
			body := map[string]any{"model": test.model, "messages": []map[string]string{{"role": "user", "content": "hello"}}}
			maps.Copy(body, test.extra)
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			request, err := ParseQoderChatCompletionsPayload(raw)
			require.NoError(t, err)
			request.Site = site
			request.Capabilities = observedPayloadModel(t, site, test.model)
			payload, _, buildErr := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
			require.NoError(t, buildErr)
			assertQoderThinkingPayload(t, payload, test.enabled, test.effort, true)
		}
	}
}

// observedPayloadModel resolves sanitized desktop metadata for payload tests.
// Unknown or retired names have no observation and therefore exercise the
// execution fallback instead of reintroducing historical service-key aliases.
func observedPayloadModel(t *testing.T, site Site, model string) *modelrules.Resolution {
	t.Helper()
	raw, err := os.ReadFile("testdata/models_" + string(site) + ".json")
	require.NoError(t, err)
	models, err := ParseModelCatalog(raw)
	require.NoError(t, err)
	index := qodermodel.NewCatalog(string(site), models)
	entry, found := index.Lookup(model)
	if !found {
		return nil
	}
	resolution := modelrules.View{}.Resolve(modelrules.Target{Platform: "qoder", Site: string(site), Model: entry.Key, ProviderModels: index}, modelcatalog.Attributes{})
	return &resolution
}

// TestBuildQoderThinkingPayloadKeepsGlobalOnlyModelIsolated requests max effort
// on a global Kimi route lacking adjustable controls. It checks disabled
// switches and absence of effort paths.
func TestBuildQoderThinkingPayloadKeepsGlobalOnlyModelIsolated(t *testing.T) {
	body := []byte(`{
		"model":"kimi-k3",
		"reasoning_effort":"max",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", SiteGlobal)
	require.NoError(t, err)
	assertQoderThinkingPayload(t, payload, false, "", false)
}

// TestEffectiveToolPolicyAcrossTextProtocols exercises each public request shape.
// A mandatory choice must fail before an upstream request and optional tools
// are removed from both the native payload and the session tool fingerprint.
func TestEffectiveToolPolicyAcrossTextProtocols(t *testing.T) {
	res := modelrules.View{}.Resolve(modelrules.Target{Platform: "qoder", Model: "route"}, modelcatalog.Attributes{ToolCall: new(false)})
	executor := NewExecutor(ExecuteOptions{})
	for _, tc := range []struct {
		wire  protocol.ProtocolID
		parse func([]byte) (QoderPayloadRequest, error)
		body  string
		named string
	}{
		{protocol.ProtocolOpenAIChatCompletions, ParseQoderChatCompletionsPayload, `{"model":"route","messages":[{"role":"user","content":"test"}],"tools":[{"type":"function","function":{"name":"demo"}}]}`, `{"type":"function","function":{"name":"demo"}}`},
		{protocol.ProtocolOpenAIResponses, ParseQoderResponsesPayload, `{"model":"route","input":"test","tools":[{"type":"function","name":"demo"}]}`, `{"type":"function","name":"demo"}`},
		{protocol.ProtocolAnthropicMessages, ParseQoderAnthropicMessagesPayload, `{"model":"route","max_tokens":16,"messages":[{"role":"user","content":"test"}],"tools":[{"name":"demo","input_schema":{"type":"object"}}]}`, `{"type":"tool","name":"demo"}`},
	} {
		t.Run(string(tc.wire), func(t *testing.T) {
			for _, choice := range []string{"", tc.named, `"required"`, `"auto"`, `"none"`} {
				nativeChoice := choice
				if tc.wire == protocol.ProtocolAnthropicMessages && choice != tc.named && choice != "" {
					if choice == `"required"` {
						nativeChoice = `{"type":"any"}`
					} else {
						nativeChoice = `{"type":` + choice + `}`
					}
				}
				var body map[string]any
				require.NoError(t, json.Unmarshal([]byte(tc.body), &body))
				if nativeChoice != "" {
					body["tool_choice"] = json.RawMessage(nativeChoice)
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				req, err := tc.parse(raw)
				require.NoError(t, err)
				req.Capabilities = &res
				req.ExplicitSession = "fixture"
				payload, _, plan, err := executor.PrepareConversation(&Target{ProviderID: 1}, tc.wire, req)
				if choice == tc.named || choice == `"required"` {
					var invalid *RequestError
					require.ErrorAs(t, err, &invalid)
					require.Equal(t, "unsupported_tool_choice", invalid.Code)
					require.Nil(t, plan)
				} else {
					require.NoError(t, err)
					require.Empty(t, payload["tools"])
					require.NotContains(t, qoderFixtureValue[map[string]any](t, payload["parameters"]), "tool_choice")
					require.Equal(t, QoderFingerprintAny([]any{}), plan.ToolsFingerprint)
				}
			}
		})
	}
}

// TestConditionalToolsUseNativeEffort verifies conditional tool policy after
// effort projection. A missing intermediate level must use its supported higher
// level both for the wire parameters and for deciding whether tools are allowed.
func TestConditionalToolsUseNativeEffort(t *testing.T) {
	model := qodermodel.Model{Key: "route", Reasoning: new(true), DefaultThinking: true, DefaultEffort: "low", Efforts: []string{"low", "high"}}
	index := qodermodel.NewCatalog("fixture", []qodermodel.Model{model})
	res := modelrules.View{}.Resolve(modelrules.Target{Model: model.Key, ProviderModels: index}, modelcatalog.Attributes{ToolCall: new(true), Conditions: new(modelcatalog.ParameterConditions{"tool_call": {Efforts: []string{"high"}}})})
	request := QoderPayloadRequest{Model: model.Key, Capabilities: &res, Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "demo"}}}}
	disabled, _, err := BuildQoderPayloadWithOptions(request, "", nil, true, true)
	require.NoError(t, err)
	require.Empty(t, disabled["tools"])
	request.Thinking = QoderThinkingDirective{Specified: true, Enabled: true, Effort: "medium"}
	enabled, _, err := BuildQoderPayloadWithOptions(request, "", nil, true, true)
	require.NoError(t, err)
	require.Len(t, enabled["tools"], 1)
	require.Equal(t, "high", qoderFixtureValue[map[string]any](t, enabled["parameters"])["reasoning_effort"])
}

// TestAllowedFunctionSubsetPreservesRequiredIntent checks flat and nested tool
// selectors. Native requests should contain only the named functions, and an
// explicit required subset must be rejected when the group disables tool calls.
func TestAllowedFunctionSubsetPreservesRequiredIntent(t *testing.T) {
	for _, tc := range []struct {
		parse func([]byte) (QoderPayloadRequest, error)
		body  string
	}{
		{ParseQoderChatCompletionsPayload, `{
			"model":"x", "messages":[{"role":"user","content":"test"}],
			"tools":[{"type":"function","function":{"name":"first"}},{"type":"function","function":{"name":"second"}}],
			"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"second"}}]}}
		}`},
		{ParseQoderResponsesPayload, `{
			"model":"x", "input":"test",
			"tools":[{"type":"function","name":"first"},{"type":"function","name":"second"}],
			"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"second"}]}
		}`},
	} {
		request, err := tc.parse([]byte(tc.body))
		require.NoError(t, err)
		out, _, err := BuildQoderPayloadWithOptions(request, "", nil, true, true)
		require.NoError(t, err)
		tools := QoderAnySlice(out["tools"])
		require.Len(t, tools, 1)
		require.Equal(t, "second", QoderDeclaredToolName(tools[0]))
		require.Equal(t, "required", qoderFixtureValue[map[string]any](t, out["parameters"])["tool_choice"])
		request.Capabilities = &modelrules.Resolution{Attributes: modelcatalog.Attributes{ToolCall: new(false)}}
		_, _, err = BuildQoderPayloadWithOptions(request, "", nil, true, true)
		require.True(t, IsRequestError(err))
	}
}
