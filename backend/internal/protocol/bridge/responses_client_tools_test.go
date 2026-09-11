package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAdaptResponsesClientTools_LowersDeclarationsHistoryChoiceAndNamespaces(t *testing.T) {
	req := map[string]any{
		"tools": []any{
			map[string]any{"type": "custom", "name": "exec", "format": map[string]any{"type": "grammar"}},
			map[string]any{"type": "tool_search"},
			map[string]any{"type": "namespace", "name": "team", "tools": []any{map[string]any{"type": "function", "name": "send"}}},
		},
		"tool_choice": map[string]any{"type": "custom", "name": "exec"},
		"input": []any{
			map[string]any{"type": "custom_tool_call", "id": "ctc_client", "call_id": "c1", "name": "exec", "input": "dir"},
			map[string]any{"type": "custom_tool_call_output", "id": "ctco_client", "call_id": "c1", "output": "ok"},
			map[string]any{"type": "tool_search_call", "id": "tsc_client", "call_id": "s1", "arguments": map[string]any{"query": "git"}},
			map[string]any{"type": "tool_search_output", "id": "tso_client", "call_id": "s1", "output": map[string]any{"groups": []string{"git"}}},
			map[string]any{"type": "function_call", "call_id": "n1", "namespace": "team", "name": "send", "arguments": "{}"},
		},
	}

	mapping, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, mapping.CustomTools["exec"])
	require.True(t, mapping.ToolSearch)
	require.Equal(t, ResponsesNamespaceName{Namespace: "team", Name: "send"}, mapping.NamespaceTools["team__send"])

	tools := requireResponsesClientToolValue[[]any](t, req["tools"])
	require.Len(t, tools, 3)
	exec := requireResponsesClientToolValue[map[string]any](t, tools[0])
	require.Equal(t, "function", exec["type"])
	parameters := requireResponsesClientToolValue[json.RawMessage](t, exec["parameters"])
	require.JSONEq(t, customToolInputSchema, string(parameters))
	search := requireResponsesClientToolValue[map[string]any](t, tools[1])
	require.Equal(t, toolSearchProxyName, search["name"])
	namespaceTool := requireResponsesClientToolValue[map[string]any](t, tools[2])
	require.Equal(t, "team__send", namespaceTool["name"])

	choice := requireResponsesClientToolValue[map[string]any](t, req["tool_choice"])
	require.Equal(t, "function", choice["type"])
	input := requireResponsesClientToolValue[[]any](t, req["input"])
	customCall := requireResponsesClientToolValue[map[string]any](t, input[0])
	require.Equal(t, "function_call", customCall["type"])
	require.Equal(t, "fc_client", customCall["id"])
	require.JSONEq(t, `{"input":"dir"}`, requireResponsesClientToolValue[string](t, customCall["arguments"]))
	customOutput := requireResponsesClientToolValue[map[string]any](t, input[1])
	require.Equal(t, "function_call_output", customOutput["type"])
	require.NotContains(t, customOutput, "id")
	searchCall := requireResponsesClientToolValue[map[string]any](t, input[2])
	require.Equal(t, "function_call", searchCall["type"])
	require.Equal(t, "fc_client", searchCall["id"])
	require.Equal(t, toolSearchProxyName, searchCall["name"])
	require.JSONEq(t, `{"query":"git"}`, requireResponsesClientToolValue[string](t, searchCall["arguments"]))
	searchOutput := requireResponsesClientToolValue[map[string]any](t, input[3])
	require.Equal(t, "function_call_output", searchOutput["type"])
	require.NotContains(t, searchOutput, "id")
	require.JSONEq(t, `{"groups":["git"]}`, requireResponsesClientToolValue[string](t, searchOutput["output"]))
	namespaceCall := requireResponsesClientToolValue[map[string]any](t, input[4])
	require.Equal(t, "team__send", namespaceCall["name"])
}

func TestAdaptResponsesClientTools_RemovesDeferredFlagsWhenToolSearchIsLowered(t *testing.T) {
	req := map[string]any{
		"tools": []any{
			map[string]any{"type": "tool_search"},
			map[string]any{"type": "function", "name": "shell", "defer_loading": true},
			map[string]any{"type": "function", "name": "apply_patch"},
		},
	}

	_, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	tools := requireResponsesClientToolValue[[]any](t, req["tools"])
	require.Equal(t, toolSearchProxyName, requireResponsesClientToolValue[map[string]any](t, tools[0])["name"])
	require.NotContains(t, requireResponsesClientToolValue[map[string]any](t, tools[1]), "defer_loading")
}

func TestStripResponsesDeferredToolFlags_PreservesFlagsWithBuiltInToolSearch(t *testing.T) {
	tools := []any{
		map[string]any{"type": "tool_search"},
		map[string]any{"type": "function", "name": "shell", "defer_loading": true},
	}

	require.False(t, stripResponsesDeferredToolFlags(tools))
	require.Equal(t, true, requireResponsesClientToolValue[map[string]any](t, tools[1])["defer_loading"])
}

func TestAdaptResponsesClientTools_LowersDiscoveredToolSearchOutput(t *testing.T) {
	requestJSON := `{
		"tools":[{"type":"tool_search"}],
		"input":[
			{"type":"tool_search_call","id":"tsc_client","call_id":"call_search","arguments":{"query":"codex app"},"execution":"client","status":"completed"},
			{"type":"tool_search_output","id":"tso_client","call_id":"call_search","execution":"client","status":"completed","tools":[
				{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"load_workspace_dependencies","description":"Load workspace dependencies","parameters":{"type":"object","properties":{},"additionalProperties":false}}]},
				{"type":"namespace","name":"multi_agent_v1","tools":[
					{"type":"function","name":"spawn_agent","description":"Spawn an agent","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}},
					{"type":"function","name":"wait_agent","description":"Wait for agents","parameters":{"type":"object","properties":{},"additionalProperties":false}}
				]}
			]}
		]
	}`

	type adaptedRequest struct {
		req     map[string]any
		mapping ResponsesClientToolMapping
	}
	adapt := func() adaptedRequest {
		var req map[string]any
		require.NoError(t, json.Unmarshal([]byte(requestJSON), &req))
		mapping, changed, err := AdaptResponsesClientTools(req)
		require.NoError(t, err)
		require.True(t, changed)
		return adaptedRequest{req: req, mapping: mapping}
	}

	first := adapt()
	second := adapt()
	firstInput := requireResponsesClientToolValue[[]any](t, first.req["input"])
	secondInput := requireResponsesClientToolValue[[]any](t, second.req["input"])

	tools := requireResponsesClientToolValue[[]any](t, first.req["tools"])
	require.Len(t, tools, 4)
	require.Equal(t, []string{
		"tool_search",
		"codex_app__load_workspace_dependencies",
		"multi_agent_v1__spawn_agent",
		"multi_agent_v1__wait_agent",
	}, responsesClientToolNames(t, tools))
	require.Equal(t, ResponsesNamespaceName{Namespace: "multi_agent_v1", Name: "spawn_agent"}, first.mapping.NamespaceTools["multi_agent_v1__spawn_agent"])
	require.Equal(t, ResponsesNamespaceName{Namespace: "multi_agent_v1", Name: "wait_agent"}, first.mapping.NamespaceTools["multi_agent_v1__wait_agent"])

	call := requireResponsesClientToolValue[map[string]any](t, firstInput[0])
	require.Equal(t, "function_call", call["type"])
	require.Equal(t, toolSearchProxyName, call["name"])
	require.JSONEq(t, `{"query":"codex app"}`, requireResponsesClientToolValue[string](t, call["arguments"]))
	require.NotContains(t, call, "execution")

	output := requireResponsesClientToolValue[map[string]any](t, firstInput[1])
	require.Equal(t, map[string]any{
		"type":    "function_call_output",
		"call_id": "call_search",
		"output":  output["output"],
	}, output)
	outputText := requireResponsesClientToolValue[string](t, output["output"])
	require.JSONEq(t, `[
		{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"load_workspace_dependencies","description":"Load workspace dependencies","parameters":{"type":"object","properties":{},"additionalProperties":false}}]},
		{"type":"namespace","name":"multi_agent_v1","tools":[
			{"type":"function","name":"spawn_agent","description":"Spawn an agent","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}},
			{"type":"function","name":"wait_agent","description":"Wait for agents","parameters":{"type":"object","properties":{},"additionalProperties":false}}
		]}
	]`, outputText)
	secondOutput := requireResponsesClientToolValue[map[string]any](t, secondInput[1])
	require.Equal(t, outputText, secondOutput["output"], "tool discovery output encoding must be deterministic")

	restored, changed, err := RestoreResponsesClientToolPayload(
		[]byte(`{"output":[{"type":"function_call","name":"multi_agent_v1__spawn_agent","call_id":"call_spawn","arguments":"{\"message\":\"work\"}"}]}`),
		first.mapping,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"output":[{"type":"function_call","name":"spawn_agent","namespace":"multi_agent_v1","call_id":"call_spawn","arguments":"{\"message\":\"work\"}"}]}`, string(restored))
}

func TestAdaptResponsesClientTools_LowersToolSearchDiscoveryPayloadWithoutOutput(t *testing.T) {
	req := map[string]any{
		"tools": []any{map[string]any{"type": "tool_search"}},
		"input": []any{map[string]any{
			"type":      "tool_search_output",
			"call_id":   "search_1",
			"tools":     []any{map[string]any{"name": "github", "type": "namespace"}},
			"status":    "completed",
			"execution": "client",
		}},
	}

	mapping, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, mapping.ToolSearch)

	item := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
	require.Equal(t, "function_call_output", item["type"])
	require.Equal(t, "search_1", item["call_id"])
	require.JSONEq(t, `[{"name":"github","type":"namespace"}]`, requireResponsesClientToolValue[string](t, item["output"]))
	require.NotContains(t, item, "tools")
	require.NotContains(t, item, "status")
	require.NotContains(t, item, "execution")
}

func TestAdaptResponsesClientTools_ToolSearchExplicitOutputTakesPriority(t *testing.T) {
	req := map[string]any{
		"tools": []any{map[string]any{"type": "tool_search"}},
		"input": []any{map[string]any{
			"type": "tool_search_output", "call_id": "search_1",
			"output": "authoritative", "tools": []any{map[string]any{"name": "ignored"}},
		}},
	}

	_, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	item := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
	require.Equal(t, "authoritative", item["output"])
	require.NotContains(t, item, "tools")
}

func TestAdaptResponsesClientTools_DoesNotInventMissingToolSearchOutput(t *testing.T) {
	req := map[string]any{
		"tools": []any{map[string]any{"type": "tool_search"}},
		"input": []any{map[string]any{
			"type": "tool_search_output", "call_id": "search_1",
			"status": "incomplete", "execution": "client",
		}},
	}

	_, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	item := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
	require.Equal(t, "function_call_output", item["type"])
	require.NotContains(t, item, "output")
	require.Equal(t, "incomplete", item["status"])
	require.Equal(t, "client", item["execution"])
}

func requireResponsesClientToolValue[T any](t *testing.T, value any) T {
	t.Helper()
	typed, ok := value.(T)
	require.True(t, ok, "unexpected value type %T", value)
	return typed
}

// responsesClientToolNames 提取测试工具声明名称，避免每个断言重复解包 map。
func responsesClientToolNames(t *testing.T, tools []any) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := requireResponsesClientToolValue[map[string]any](t, raw)
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestAdaptResponsesClientTools_RejectsAmbiguousNames(t *testing.T) {
	cases := []map[string]any{
		{"tools": []any{map[string]any{"type": "custom", "name": "same"}, map[string]any{"type": "function", "name": "same"}}},
		{"tools": []any{map[string]any{"type": "tool_search"}, map[string]any{"type": "function", "name": "tool_search"}}},
		{"tools": []any{map[string]any{"type": "function", "name": "team__send"}, map[string]any{"type": "namespace", "name": "team", "tools": []any{map[string]any{"type": "function", "name": "send"}}}}},
	}
	for _, req := range cases {
		_, _, err := AdaptResponsesClientTools(req)
		require.Error(t, err)
	}
}

func TestAdaptResponsesClientToolsWithInheritedMapping_LowersFollowupHistoryWithoutTools(t *testing.T) {
	req := map[string]any{
		"input": []any{
			map[string]any{
				"type": "custom_tool_call", "name": "exec",
				"call_id": "call_1", "input": "pwd",
			},
			map[string]any{
				"type": "custom_tool_call_output", "call_id": "call_1",
				"id": "ctco_client_output_1", "output": []any{
					map[string]any{"type": "input_text", "text": "ok"},
				},
			},
		},
	}
	inherited := ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}}

	mapping, changed, err := AdaptResponsesClientToolsWithInheritedMapping(req, inherited)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, inherited, mapping)
	items, ok := req["input"].([]any)
	require.True(t, ok)
	call, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", call["type"])
	arguments, ok := call["arguments"].(string)
	require.True(t, ok)
	require.JSONEq(t, `{"input":"pwd"}`, arguments)
	require.NotContains(t, call, "input")
	output, ok := items[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call_output", output["type"])
	require.NotContains(t, output, "id")
	require.Equal(t, []any{map[string]any{"type": "input_text", "text": "ok"}}, output["output"])
}

func TestAdaptResponsesClientTools_NormalizesCustomToolOutput(t *testing.T) {
	tests := []struct {
		name       string
		output     any
		wantOutput any
	}{
		{
			name: "supported content parts remain an array",
			output: []any{
				map[string]any{"type": "input_text", "text": "ok"},
				map[string]any{"type": "input_image", "image_url": "https://example.com/image.png"},
				map[string]any{"type": "input_file", "file_id": "file_123"},
			},
			wantOutput: []any{
				map[string]any{"type": "input_text", "text": "ok"},
				map[string]any{"type": "input_image", "image_url": "https://example.com/image.png"},
				map[string]any{"type": "input_file", "file_id": "file_123"},
			},
		},
		{name: "ordinary object is stringified", output: map[string]any{"ok": true}, wantOutput: `{"ok":true}`},
		{name: "arbitrary array is stringified", output: []any{"ok"}, wantOutput: `["ok"]`},
		{name: "empty array is stringified", output: []any{}, wantOutput: `[]`},
		{name: "mixed array is stringified", output: []any{map[string]any{"type": "input_text", "text": "ok"}, "bad"}, wantOutput: `[{"text":"ok","type":"input_text"},"bad"]`},
		{name: "unknown content type is stringified", output: []any{map[string]any{"type": "output_text", "text": "bad"}}, wantOutput: `[{"text":"bad","type":"output_text"}]`},
		{name: "whitespace-padded content type is stringified", output: []any{map[string]any{"type": " input_text ", "text": "bad"}}, wantOutput: `[{"text":"bad","type":" input_text "}]`},
		{name: "missing content type is stringified", output: []any{map[string]any{"text": "bad"}}, wantOutput: `[{"text":"bad"}]`},
		{name: "non-string content type is stringified", output: []any{map[string]any{"type": 1}}, wantOutput: `[{"type":1}]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := map[string]any{
				"tools": []any{map[string]any{"type": "custom", "name": "exec"}},
				"input": []any{map[string]any{
					"type": "custom_tool_call_output", "call_id": "call_1", "output": tc.output,
				}},
			}

			_, changed, err := AdaptResponsesClientTools(req)
			require.NoError(t, err)
			require.True(t, changed)
			item := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
			require.Equal(t, "function_call_output", item["type"])
			require.Equal(t, tc.wantOutput, item["output"])
		})
	}
}

func TestAdaptResponsesClientToolsWithInheritedMapping_PromotesOmittedToolsDiscoveryIntoEffectiveDeclarations(t *testing.T) {
	req := map[string]any{
		"input": []any{map[string]any{
			"type": "tool_search_output", "call_id": "call_search", "status": "completed", "execution": "client",
			"tools": []any{map[string]any{
				"type": "namespace", "name": "multi_agent_v1", "tools": []any{map[string]any{
					"type": "function", "name": "spawn_agent", "parameters": map[string]any{"type": "object"},
				}},
			}},
		}},
	}
	inherited := ResponsesClientToolMapping{
		ToolSearch: true,
		NamespaceTools: map[string]ResponsesNamespaceName{
			"codex_app__read_resource": {Namespace: "codex_app", Name: "read_resource"},
		},
	}
	lowered := []any{
		map[string]any{"type": "function", "name": "static_first", "parameters": map[string]any{"type": "object"}},
		map[string]any{"type": "function", "name": "tool_search", "parameters": json.RawMessage(toolSearchProxySchema)},
		map[string]any{"type": "function", "name": "codex_app__read_resource", "parameters": map[string]any{"type": "object"}},
	}

	mapping, changed, err := AdaptResponsesClientToolsWithInheritedMapping(req, inherited, lowered)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, mapping.ToolSearch)
	require.Equal(t, ResponsesNamespaceName{Namespace: "codex_app", Name: "read_resource"}, mapping.NamespaceTools["codex_app__read_resource"])
	require.Equal(t, ResponsesNamespaceName{Namespace: "multi_agent_v1", Name: "spawn_agent"}, mapping.NamespaceTools["multi_agent_v1__spawn_agent"])
	tools := requireResponsesClientToolValue[[]any](t, req["tools"])
	require.Equal(t, []string{
		"static_first", "tool_search", "codex_app__read_resource", "multi_agent_v1__spawn_agent",
	}, responsesClientToolNames(t, tools))
	output := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
	require.Equal(t, "function_call_output", output["type"])
	require.IsType(t, "", output["output"])
	require.NotContains(t, output, "tools")
	require.NotContains(t, output, "status")
	require.NotContains(t, output, "execution")
}

func TestAdaptResponsesClientToolsWithInheritedMapping_ExplicitToolsReplaceInheritedMapping(t *testing.T) {
	req := map[string]any{
		"tools": []any{},
		"input": []any{map[string]any{
			"type": "custom_tool_call", "name": "exec", "input": "pwd",
		}},
	}

	mapping, changed, err := AdaptResponsesClientToolsWithInheritedMapping(
		req,
		ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}},
	)

	require.NoError(t, err)
	require.False(t, changed)
	require.Empty(t, mapping)
	input, ok := req["input"].([]any)
	require.True(t, ok)
	firstInput, ok := input[0].(map[string]any)
	require.True(t, ok)
	inputType, ok := firstInput["type"].(string)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", inputType)
}

func TestAdaptResponsesClientToolsWithInheritedMapping_ExplicitToolResetDoesNotPromoteDiscovery(t *testing.T) {
	for _, reset := range []any{nil, []any{}} {
		req := map[string]any{
			"tools": reset,
			"input": []any{map[string]any{
				"type": "tool_search_output", "call_id": "call_reset", "status": "completed",
				"tools": []any{map[string]any{"type": "function", "name": "must_not_promote"}},
			}},
		}
		mapping, changed, err := AdaptResponsesClientToolsWithInheritedMapping(
			req,
			ResponsesClientToolMapping{ToolSearch: true},
			[]any{map[string]any{"type": "function", "name": "tool_search"}},
		)
		require.NoError(t, err)
		require.False(t, changed)
		require.Empty(t, mapping)
		item := requireResponsesClientToolValue[map[string]any](t, requireResponsesClientToolValue[[]any](t, req["input"])[0])
		require.Equal(t, "tool_search_output", item["type"])
	}
}

func TestRestoreResponsesClientToolPayload_RestoresClientAndNamespaceCalls(t *testing.T) {
	mapping := ResponsesClientToolMapping{
		CustomTools: map[string]bool{"exec": true}, ToolSearch: true,
		NamespaceTools: map[string]ResponsesNamespaceName{"team__send": {Namespace: "team", Name: "send"}},
	}
	payload := []byte(`{"id":"resp","output":[{"type":"function_call","id":"i1","call_id":"c1","name":"exec","arguments":"{\"input\":\"dir\"}","namespace":"ignore"},{"type":"function_call","id":"i2","call_id":"s1","name":"tool_search","arguments":"{\"query\":\"git\"}"},{"type":"function_call","id":"i3","call_id":"n1","name":"team__send","arguments":"{}"}]}`)

	restored, changed, err := RestoreResponsesClientToolPayload(payload, mapping)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"id":"resp","output":[{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"exec","input":"dir"},{"type":"tool_search_call","id":"i2","call_id":"s1","execution":"client","arguments":{"query":"git"}},{"type":"function_call","id":"i3","call_id":"n1","name":"send","namespace":"team","arguments":"{}"}]}`, string(restored))
}

func TestResponsesClientToolStreamRestorer_CustomToolBuffersWrapperAndSequences(t *testing.T) {
	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})
	added := restorer.Restore(ResponsesStreamEvent{Type: "response.output_item.added", SequenceNumber: 7, OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", ID: "i1", CallID: "c1", Name: "exec", Status: "in_progress"}})
	require.Len(t, added, 1)
	require.Equal(t, 7, added[0].SequenceNumber)
	require.Equal(t, "custom_tool_call", added[0].Item.Type)
	require.Empty(t, restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.delta", SequenceNumber: 8, ItemID: "i1", Delta: `{"input":"di`}))
	done := restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.done", SequenceNumber: 9, ItemID: "i1", CallID: "c1", Name: "exec", Arguments: `{"input":"dir"}`})
	require.Len(t, done, 2)
	require.Equal(t, 8, done[0].SequenceNumber)
	require.Equal(t, "response.custom_tool_call_input.delta", done[0].Type)
	require.Equal(t, "dir", done[0].Delta)
	require.Equal(t, 9, done[1].SequenceNumber)
	require.Equal(t, "response.custom_tool_call_input.done", done[1].Type)
	require.Equal(t, "dir", done[1].Input)
	closed := restorer.Restore(ResponsesStreamEvent{Type: "response.output_item.done", SequenceNumber: 10, OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", ID: "i1", CallID: "c1", Name: "exec", Arguments: `{"input":"dir"}`, Status: "completed"}})
	require.Equal(t, 10, closed[0].SequenceNumber)
	require.Equal(t, "custom_tool_call", closed[0].Item.Type)
	require.Equal(t, "dir", closed[0].Item.Input)
}

func TestResponsesClientToolStreamRestorer_ToolSearchAndFunction(t *testing.T) {
	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{ToolSearch: true})
	search := restorer.Restore(ResponsesStreamEvent{Type: "response.output_item.added", SequenceNumber: 0, OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", ID: "s1", CallID: "c1", Name: "tool_search", Status: "in_progress"}})
	require.Equal(t, "tool_search_call", search[0].Item.Type)
	require.Empty(t, restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.delta", SequenceNumber: 1, ItemID: "s1", Delta: `{"query":"git"}`}))
	require.Empty(t, restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.done", SequenceNumber: 2, ItemID: "s1", Arguments: `{"query":"git"}`}))
	closed := restorer.Restore(ResponsesStreamEvent{Type: "response.output_item.done", SequenceNumber: 3, OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", ID: "s1", CallID: "c1", Name: "tool_search", Status: "completed"}})
	require.Equal(t, 1, closed[0].SequenceNumber)
	require.Equal(t, "tool_search_call", closed[0].Item.Type)
	require.JSONEq(t, `{"query":"git"}`, string(toolSearchCallArgumentsJSON(closed[0].Item.Arguments)))

	function := restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.done", SequenceNumber: 4, ItemID: "plain", Name: "plain", Arguments: "{}"})
	require.Len(t, function, 1)
	require.Equal(t, "response.function_call_arguments.done", function[0].Type)
	require.Equal(t, 2, function[0].SequenceNumber)
}

func TestResponsesClientToolStreamRestorer_RestoresNamespaceLifecycle(t *testing.T) {
	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{
		NamespaceTools: map[string]ResponsesNamespaceName{
			"browser__open": {Namespace: "browser", Name: "open"},
		},
	})

	added, changed, err := restorer.RestoreEvent([]byte(`{"type":"response.output_item.added","sequence_number":4,"output_index":0,"item":{"type":"function_call","id":"i1","call_id":"c1","name":"browser__open","arguments":"","status":"in_progress"}}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, added, 1)
	require.Equal(t, "open", gjson.GetBytes(added[0], "item.name").String())
	require.Equal(t, "browser", gjson.GetBytes(added[0], "item.namespace").String())

	delta, changed, err := restorer.RestoreEvent([]byte(`{"type":"response.function_call_arguments.delta","sequence_number":5,"output_index":0,"item_id":"i1","name":"browser__open","delta":"{\"url\":"}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, delta, 1)
	require.Equal(t, "open", gjson.GetBytes(delta[0], "name").String())

	done, changed, err := restorer.RestoreEvent([]byte(`{"type":"response.function_call_arguments.done","sequence_number":6,"output_index":0,"item_id":"i1","name":"browser__open","arguments":"{}"}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, done, 1)
	require.Equal(t, "open", gjson.GetBytes(done[0], "name").String())
}

func TestResponsesClientToolStreamRestorer_RawEventsPreserveUnknownFieldsAndOutputFallback(t *testing.T) {
	restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})
	passthrough, changed, err := restorer.RestoreEvent([]byte(`{"type":"response.created","sequence_number":4,"response":{"id":"r"},"upstream_extension":{"keep":true}}`))
	require.NoError(t, err)
	require.False(t, changed)
	require.Len(t, passthrough, 1)
	require.Contains(t, string(passthrough[0]), `"upstream_extension":{"keep":true}`)

	restorer.Restore(ResponsesStreamEvent{Type: "response.output_item.added", SequenceNumber: 5, OutputIndex: 9, Item: &ResponsesOutput{Type: "function_call", ID: "item", CallID: "call", Name: "exec"}})
	// 部分上游会在后续参数分片中省略所有工具身份字段。
	require.Empty(t, restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.delta", SequenceNumber: 6, OutputIndex: 9, Delta: `{"input":"pwd"}`}))
	done := restorer.Restore(ResponsesStreamEvent{Type: "response.function_call_arguments.done", SequenceNumber: 7, OutputIndex: 9})
	require.Len(t, done, 2)
	require.Equal(t, "pwd", done[1].Input)
}

func TestResponsesClientToolStreamRestorer_RestoresAllTerminalEvents(t *testing.T) {
	for _, eventType := range []string{
		"response.completed",
		"response.done",
		"response.incomplete",
		"response.failed",
		"response.cancelled",
		"response.canceled",
	} {
		t.Run(eventType, func(t *testing.T) {
			restorer := NewResponsesClientToolStreamRestorer(ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}})
			payload := []byte(`{"type":"` + eventType + `","sequence_number":7,"response":{"id":"resp_tools","output":[{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec","arguments":"{\"input\":\"pwd\"}"}]}}`)

			restored, changed, err := restorer.RestoreEvent(payload)

			require.NoError(t, err)
			require.True(t, changed)
			require.Len(t, restored, 1)
			require.Equal(t, eventType, gjson.GetBytes(restored[0], "type").String())
			require.Equal(t, int64(7), gjson.GetBytes(restored[0], "sequence_number").Int())
			require.Equal(t, "custom_tool_call", gjson.GetBytes(restored[0], "response.output.0.type").String())
			require.Equal(t, "pwd", gjson.GetBytes(restored[0], "response.output.0.input").String())
			require.False(t, gjson.GetBytes(restored[0], "response.output.0.arguments").Exists())
		})
	}
}
