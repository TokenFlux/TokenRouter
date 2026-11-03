package qoder

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

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Stream fixtures assemble client responses from explicit event sequences
// containing split arguments, index drift, and text-encoded calls. Separate
// usage cases check cached/uncached categories and last total interpretation,
// while the scanner fixture verifies a cancelled consumer releases blocked
// result delivery.

// XML, JSON, and DSML text-call fixtures represent complete invocation formats
// used by the non-streaming protocol assembly assertions.
const (
	qoderXMLToolCallFixture       = `<tool_call>Read<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>`
	qoderJSONShellToolCallFixture = `<tool_call>{"name":"shell","arguments":{"command":"pwd","description":"Print working directory"}}</tool_call>`

	qoderDSMLToolCallFixture = `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="Bash">
<｜｜DSML｜｜parameter name="command" string="true">ls -la</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="description" string="true">List root files</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`
)

// qoderCachedUsageEventForTest retains a shared cache-bearing usage fixture.
// Tests copy nested details before changing values so other cases keep the same
// observation.
var qoderCachedUsageEventForTest = SSEEvent{
	Type:             "usage",
	PromptTokens:     66637,
	CompletionTokens: 6,
	TotalTokens:      66643,
	UsageDetails: UsageDetails{
		PromptTokensDetails:     &PromptTokensDetails{CachedTokens: 66612, CacheableTokens: 19},
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: 0},
	},
	HasUsage: true,
}

// TestQoderUsageSerializersPreserveAvailableCacheWrites supplies independent
// ordinary, cached, and cache-write counts. All output protocols must retain
// the reported write bucket without treating cacheable prompt capacity as writes.
func TestQoderUsageSerializersPreserveAvailableCacheWrites(t *testing.T) {
	usage := QoderUsageFromEvents(nil)
	usage.InputTokens = 20
	usage.CacheReadInputTokens = 60
	usage.CacheCreationInputTokens = 20
	usage.OutputTokens = 10
	require.Equal(t, 110, QoderTotalTokensFromEvents(nil, usage))
	completed := QoderEventsWithUsage([]SSEEvent{{HasUsage: true}}, usage)
	require.Equal(t, 100, completed[0].PromptTokens)
	require.Equal(t, 110, completed[0].TotalTokens)
	chat := QoderOpenAIUsage(usage, 0)
	encoded, err := json.Marshal(chat)
	require.NoError(t, err)
	require.Equal(t, int64(100), gjson.GetBytes(encoded, "prompt_tokens").Int())
	require.Equal(t, int64(110), gjson.GetBytes(encoded, "total_tokens").Int())
	require.Equal(t, int64(60), gjson.GetBytes(encoded, "prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(20), gjson.GetBytes(encoded, "prompt_tokens_details.cache_creation_tokens").Int())
	responses := QoderResponsesUsage(usage)
	require.Equal(t, 100, responses.InputTokens)
	require.Equal(t, 110, responses.TotalTokens)
	require.Equal(t, 20, responses.CacheCreationInputTokens)
	messages := QoderAnthropicUsage(usage)
	require.Equal(t, 20, messages["input_tokens"])
	require.Equal(t, 60, messages["cache_read_input_tokens"])
	require.Equal(t, 20, messages["cache_creation_input_tokens"])
}

// qoderNoIndexNamedParallelToolCallEventsForTest returns three complete named
// invocations without wire IDs or positions. The fixtures use them to check
// implicit slot separation after each completed argument object.
func qoderNoIndexNamedParallelToolCallEventsForTest() []SSEEvent {
	return []SSEEvent{
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`},
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"ls -la","description":"List files in current directory"}`},
		{Type: "tool_call_delta", ToolName: "glob", Arguments: `{"pattern":"**/*.md"}`},
		{IsDone: true},
	}
}

// qoderRepeatedIndexNamedParallelToolCallEventsForTest returns three complete
// named calls reusing upstream index zero. Response fixtures check separate
// ordered output invocations with distinct arguments instead of concatenated
// complete JSON objects.
func qoderRepeatedIndexNamedParallelToolCallEventsForTest() []SSEEvent {
	return []SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"pwd","description":"Print working directory"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "glob", Arguments: `{"pattern":"docs/*.md"}`},
		{IsDone: true},
	}
}

// TestResolveQoderModelUsesOpus46AliasForUltimate resolves the current global
// Opus alias and two unmatched model names. It checks ultimate/system mapping
// plus passthrough spelling for the unmatched names.
func TestResolveQoderModelUsesOpus46AliasForUltimate(t *testing.T) {
	info := ResolveQoderModelForSite(SiteGlobal, "claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "system", info.Source)

	legacy := ResolveQoderModelForSite(SiteGlobal, "claude-opus-4-5")
	require.Equal(t, "claude-opus-4-5", legacy.Key)

	codex := ResolveQoderModelForSite(SiteGlobal, "gpt-5-codex")
	require.Equal(t, "gpt-5-codex", codex.Key)
}

// TestResolveQoderModelUsesQwen38MaxAlias resolves the public Qwen alias for
// both sites. It checks the shared qmodel_38max route and system source in each
// result.
func TestResolveQoderModelUsesQwen38MaxAlias(t *testing.T) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		info := ResolveQoderModelForSite(site, "qwen3.8-max")
		require.Equal(t, "qmodel_38max", info.Key)
		require.Equal(t, "system", info.Source)
	}
}

// TestResolveQoderModelUsesKimiK3Alias resolves the global public Kimi model
// alias. It checks the kmodel_latest route and system source used when preparing
// inference metadata.
func TestResolveQoderModelUsesKimiK3Alias(t *testing.T) {
	info := ResolveQoderModelForSite(SiteGlobal, "kimi-k3")
	require.Equal(t, "kmodel_latest", info.Key)
	require.Equal(t, "system", info.Source)
}

// TestResolveQoderModelUsesGLM52RouteKey resolves the global GLM public alias.
// It checks the expected gm51model route and system source retained for payload
// model configuration.
func TestResolveQoderModelUsesGLM52RouteKey(t *testing.T) {
	info := ResolveQoderModelForSite(SiteGlobal, "glm-5.2")
	require.Equal(t, "gm51model", info.Key)
	require.Equal(t, "system", info.Source)
}

// TestResolveQoderModelUsesGLM53RouteKey resolves the public GLM alias at both
// sites. It checks gmodel and system source so their shared route identity stays
// consistent.
func TestResolveQoderModelUsesGLM53RouteKey(t *testing.T) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		info := ResolveQoderModelForSite(site, "glm-5.3")
		require.Equal(t, "gmodel", info.Key)
		require.Equal(t, "system", info.Source)
	}
}

// TestResolveQoderModelDoesNotTranslateRemovedCompatibilityAliases resolves a
// set of unmatched historical names and raw routes. It checks their supplied
// spelling remains the key with system source.
func TestResolveQoderModelDoesNotTranslateRemovedCompatibilityAliases(t *testing.T) {
	for _, model := range []string{"ultimate", "qwen3.8-max-preview", "qmodel_preview", "qwen3.5-plus", "glm-5", "glm-5.1", "kimi-k2.6"} {
		info := ResolveQoderModelForSite(SiteGlobal, model)
		require.Equal(t, model, info.Key)
		require.Equal(t, "system", info.Source)
	}
}

// TestQoderGatewayAssemblesResponsesKeepsNoIndexNamedParallelFunctionCalls
// assembles three complete named calls without wire indices. It checks three
// ordered Responses invocations with separate names and valid argument strings.
func TestQoderGatewayAssemblesResponsesKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := BuildQoderResponsesResponse("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
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

// TestQoderGatewayAssemblesResponsesKeepsRepeatedIndexNamedParallelFunctionCalls
// assembles complete calls reusing wire index zero. It checks three ordered
// Responses invocations and separate arguments for each named call.
func TestQoderGatewayAssemblesResponsesKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := BuildQoderResponsesResponse("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
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

// TestQoderGatewayUsageSplitsCachedPromptTokens supplies prompt totals with
// cached-token detail and completion usage. It checks uncached input, cache
// reads, output, and zero cache creation independently.
func TestQoderGatewayUsageSplitsCachedPromptTokens(t *testing.T) {
	usage := QoderUsageFromEvents([]SSEEvent{qoderCachedUsageEventForTest})
	require.Equal(t, 25, usage.InputTokens)
	require.Equal(t, 66612, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.OutputTokens)
	require.Equal(t, 0, usage.CacheCreationInputTokens)
}

// TestQoderGatewayUsageClampsCachedPromptTokens supplies cache reads larger than
// the prompt count. It checks uncached input clamps to zero while cache-read
// evidence retains the larger reported amount.
func TestQoderGatewayUsageClampsCachedPromptTokens(t *testing.T) {
	event := qoderCachedUsageEventForTest
	// The usage fixture retains a shared detail pointer. Copy that object before
	// changing cached tokens so this clamp scenario cannot alter observations used
	// by other tests.
	promptDetails := *event.UsageDetails.PromptTokensDetails
	event.UsageDetails.PromptTokensDetails = &promptDetails
	event.UsageDetails.PromptTokensDetails.CachedTokens = 70000
	usage := QoderUsageFromEvents([]SSEEvent{event})
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 70000, usage.CacheReadInputTokens)
}

// TestQoderGatewayUsageKeepsOldBehaviorWhenDetailsMissing supplies primary token
// counts with no prompt details. It checks full prompt input, zero cache reads,
// and retained output usage.
func TestQoderGatewayUsageKeepsOldBehaviorWhenDetailsMissing(t *testing.T) {
	event := SSEEvent{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true}
	usage := QoderUsageFromEvents([]SSEEvent{event})
	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 0, usage.CacheReadInputTokens)
	require.Equal(t, 34, usage.OutputTokens)
}

// TestQoderGatewayAssemblesNonStreamingChatCompletion combines reasoning, split
// visible text, and usage events. It checks Hello content and exact prompt,
// completion, and total token fields.
func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
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

// TestQoderGatewayAssemblesNonStreamingChatCompletionWithToolCalls supplies a
// function ID/name followed by split arguments. It checks merged JSON,
// tool_calls finish reason, and null visible content.
func TestQoderGatewayAssemblesNonStreamingChatCompletionWithToolCalls(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.True(t, gjson.GetBytes(body, "choices.0.message.content").Exists())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionMapsToolNameToDeclaredOpenAITool
// supplies upstream Bash with a declared lowercase function tool. It checks the
// declared name, tool finish reason, and retained command arguments.
func TestQoderGatewayAssemblesNonStreamingChatCompletionMapsToolNameToDeclaredOpenAITool(t *testing.T) {
	events := []SSEEvent{
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

	body, err := BuildQoderOpenAICompletion("auto", events, QoderDeclaredToolNameMapper(tools))
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionMergesIndexDriftForSameCall
// changes wire position while retaining one call ID. It checks one assembled
// invocation with all argument fragments associated with that identity.
func TestQoderGatewayAssemblesNonStreamingChatCompletionMergesIndexDriftForSameCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(1), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsParallelCallIndexes
// interleaves argument fragments for two identified upstream positions. It
// checks both call names and IDs alongside their independent completed JSON
// objects.
func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsParallelCallIndexes(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"path":"a"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.JSONEq(t, `{"path":"b"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionParsesXMLTextToolCall
// supplies one complete XML invocation as visible text. It checks Read
// arguments, tool finish reason, null content, and removal of marker text.
func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesXMLTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionParsesJSONTextToolCall
// supplies shell invocation JSON inside a text marker. It checks Bash
// normalization and separate command/description arguments with null visible
// content.
func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesJSONTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.NotContains(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String(), "{")
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionParsesDSMLTextToolCall
// supplies a named DSML invocation and parameters. It checks Bash argument
// conversion and absence of DSML tags in response output.
func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesDSMLTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"ls -la","description":"List root files"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionParsesSplitXMLTextToolCall
// splits the opening marker and argument tags across events. It checks one
// complete invocation with marker fragments absent from visible output.
func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesSplitXMLTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: "<tool_"},
		{Type: "text_delta", Text: "call>Re"},
		{Type: "text_delta", Text: "ad<arg_value><arg_key>file_path</arg_key>"},
		{Type: "text_delta", Text: "<arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>"},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

// TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsMixedXMLToolText
// places readable text before and after an XML invocation. It checks retained
// surrounding text and a separately parsed Read call.
func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsMixedXMLToolText(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: "I will inspect it.\n"},
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{Type: "text_delta", Text: "\nWaiting for result."},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "I will inspect it.\n\nWaiting for result.", gjson.GetBytes(body, "choices.0.message.content").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithThinking supplies
// reasoning followed by text and usage. It checks ordered thinking/text blocks
// and retained input/output token counts in Messages JSON.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithThinking(t *testing.T) {
	events := []SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
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

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithToolUse supplies one
// identified call with split JSON arguments. It checks tool_use stop reason,
// name/ID, and the decoded input object.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithToolUse(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "content.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.cmd").String())
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageRejectsMalformedToolArguments
// ends after an incomplete argument object. It checks a malformed-arguments
// error and nil response bytes instead of accepting a tool block.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageRejectsMalformedToolArguments(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("auto", events)

	require.Error(t, err)
	require.Nil(t, body)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageSkipsTypeOnlyPlaceholder
// supplies a default function type with no invocation identity or arguments. It
// checks end_turn and empty-text content with no tool-use block.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageSkipsTypeOnlyPlaceholder(t *testing.T) {
	events := []SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "end_turn", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, "text", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.False(t, gjson.GetBytes(body, `content.#(type=="tool_use")`).Exists(), string(body))
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsNoIndexNamedParallelToolCalls
// assembles three named calls without wire positions. It checks ordered tool-use
// blocks and independent decoded argument objects for each invocation.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
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

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsRepeatedIndexNamedParallelToolCalls
// reuses index zero for three complete named invocations. It checks separate
// tool blocks, distinct names, and independently decoded command/pattern
// arguments.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {
	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
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

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesXMLTextToolCall
// supplies a complete Read invocation encoded as XML text. It checks tool-use
// content and the decoded file-path argument with marker text removed.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesXMLTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "/workspace/campus-navigation/README.md", gjson.GetBytes(body, "content.0.input.file_path").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesJSONTextToolCall
// supplies shell JSON inside text-call markup. It checks Bash name,
// command/description input, and separation of function identity from the
// serialized payload.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesJSONTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "Print working directory", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), `"name":"{\"name\"`)
}

// TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesDSMLTextToolCall
// supplies DSML command and description parameters. It checks Bash tool-use
// content and decoded parameter strings with DSML markers removed.
func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesDSMLTextToolCall(t *testing.T) {
	events := []SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "ls -la", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "List root files", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

// TestQoderGatewayReadsWrappedSSE reads reasoning, text, usage, and wrapped DONE
// frames from one response. It checks their event order, retained content,
// counts, and terminal observation.
func TestQoderGatewayReadsWrappedSSE(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":3,\\\"completion_tokens\\\":4,\\\"total_tokens\\\":7}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEventsContext(context.Background(), resp)
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

// TestQoderGatewayScannerStopsWhenResultSendIsCanceled stops consumption after
// the first scanner batch and cancels its context. It checks the producer
// goroutine exits while later results cannot drain normally.
func TestQoderGatewayScannerStopsWhenResultSendIsCanceled(t *testing.T) {
	line := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat(line, 10)))}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan QoderEventResult, 1)
	done := make(chan struct{})

	// Leave later scanner batches buffered after observing the first result.
	// Cancellation then needs to release the producer's send independently of
	// ordinary channel draining.
	go func() {
		ScanQoderEvents(ctx, resp, results)
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

// TestQoderGatewayReadsWrappedSSEUpstreamError reads a wrapped signature
// rejection from a response body. It checks empty collected events and typed
// status/code/message information in the returned error.
func TestQoderGatewayReadsWrappedSSEUpstreamError(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEventsContext(context.Background(), resp)
	require.Error(t, err)
	require.Empty(t, events)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 403, apiErr.StatusCode)
	require.Equal(t, "101", apiErr.Code)
	require.Equal(t, "Signature invalid", apiErr.Message)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", apiErr.Error())
}

// TestQoderGatewayOpenAIUsageDerivesTotalFromPromptWhenUpstreamTotalMissing
// clears total from a cache-bearing usage observation. It checks reconstructed
// prompt/completion/total counts and retained cache-read detail in Chat output.
func TestQoderGatewayOpenAIUsageDerivesTotalFromPromptWhenUpstreamTotalMissing(t *testing.T) {
	event := qoderCachedUsageEventForTest
	event.TotalTokens = 0
	body, err := BuildQoderOpenAICompletion("auto", []SSEEvent{
		{Type: "text_delta", Text: "OK"},
		event,
		{IsDone: true},
	})
	require.NoError(t, err)

	require.Equal(t, int64(66637), gjson.GetBytes(body, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.GetBytes(body, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.GetBytes(body, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.GetBytes(body, "usage.prompt_tokens_details.cached_tokens").Int())
}
