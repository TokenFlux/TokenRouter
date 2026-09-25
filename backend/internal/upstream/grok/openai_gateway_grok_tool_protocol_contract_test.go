//go:build unit

package grok_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	groktestkit "github.com/TokenFlux/TokenRouter/internal/upstream/grok/testkit"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPatchGrokResponsesBodyWithClientToolsLowersCodexProtocol(t *testing.T) {
	t.Parallel()

	body := groktestkit.ClientToolsRequest(false)
	patched, mapping, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.True(t, mapping.CustomTools["apply_patch"])
	require.True(t, mapping.ToolSearch)
	require.Equal(t, "collaboration", mapping.NamespaceTools["collaboration__send_message"].Namespace)
	require.Equal(t, "send_message", mapping.NamespaceTools["collaboration__send_message"].Name)

	tools := gjson.GetBytes(patched, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "apply_patch", tools[0].Get("name").String())
	require.Equal(t, "string", tools[0].Get("parameters.properties.input.type").String())
	require.False(t, tools[0].Get("format").Exists())
	require.Equal(t, "function", tools[1].Get("type").String())
	require.Equal(t, "tool_search", tools[1].Get("name").String())
	require.Equal(t, "function", tools[2].Get("type").String())
	require.Equal(t, "collaboration__send_message", tools[2].Get("name").String())
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="namespace")`).Exists())
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="tool_search")`).Exists())

	require.Equal(t, "function", gjson.GetBytes(patched, "tool_choice.type").String())
	require.Equal(t, "apply_patch", gjson.GetBytes(patched, "tool_choice.name").String())
	require.Equal(t, "function_call", gjson.GetBytes(patched, "input.0.type").String())
	require.JSONEq(t, `{"input":"*** Begin Patch"}`, gjson.GetBytes(patched, "input.0.arguments").String())
	require.False(t, gjson.GetBytes(patched, "input.0.input").Exists())
	require.Equal(t, "function_call_output", gjson.GetBytes(patched, "input.1.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(patched, "input.2.type").String())
	require.Equal(t, "tool_search", gjson.GetBytes(patched, "input.2.name").String())
	require.JSONEq(t, `{"query":"github"}`, gjson.GetBytes(patched, "input.2.arguments").String())
	require.False(t, gjson.GetBytes(patched, "input.2.execution").Exists())
	require.Equal(t, "function_call_output", gjson.GetBytes(patched, "input.3.type").String())
	require.JSONEq(t, `{"groups":["github"]}`, gjson.GetBytes(patched, "input.3.output").String())
	require.Equal(t, "function_call", gjson.GetBytes(patched, "input.4.type").String())
	require.Equal(t, "collaboration__send_message", gjson.GetBytes(patched, "input.4.name").String())
	require.False(t, gjson.GetBytes(patched, "input.4.namespace").Exists())
}

func TestPatchGrokResponsesBodyWithClientToolsLowersDiscoveredToolsOutput(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"grok-4.5",
		"tools":[{"type":"tool_search"}],
		"input":[
			{"type":"tool_search_call","id":"tsc_fixture","call_id":"call_fixture","arguments":{"query":"subagent"},"execution":"client","status":"completed"},
			{"type":"tool_search_output","id":"tso_fixture","call_id":"call_fixture","execution":"client","status":"completed","tools":[
				{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"load_workspace_dependencies","parameters":{"type":"object","properties":{},"additionalProperties":false}}]},
				{"type":"namespace","name":"multi_agent_v1","tools":[
					{"type":"function","name":"spawn_agent","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}},
					{"type":"function","name":"wait_agent","parameters":{"type":"object","properties":{"timeout_ms":{"type":"integer"}},"additionalProperties":false}}
				]}
			]}
		]
	}`)

	patched, mapping, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, mapping.ToolSearch)
	require.Equal(t, bridge.ResponsesNamespaceName{Namespace: "multi_agent_v1", Name: "spawn_agent"}, mapping.NamespaceTools["multi_agent_v1__spawn_agent"])
	require.Equal(t, bridge.ResponsesNamespaceName{Namespace: "multi_agent_v1", Name: "wait_agent"}, mapping.NamespaceTools["multi_agent_v1__wait_agent"])
	output := gjson.GetBytes(patched, "input.1.output").String()
	require.JSONEq(t, `[
		{"type":"namespace","name":"codex_app","tools":[{"type":"function","name":"load_workspace_dependencies","parameters":{"type":"object","properties":{},"additionalProperties":false}}]},
		{"type":"namespace","name":"multi_agent_v1","tools":[
			{"type":"function","name":"spawn_agent","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}},
			{"type":"function","name":"wait_agent","parameters":{"type":"object","properties":{"timeout_ms":{"type":"integer"}},"additionalProperties":false}}
		]}
	]`, output)

	require.JSONEq(t, `{
		"model":"grok-4.5",
		"tools":[
			{"type":"function","name":"tool_search","description":"Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.","parameters":{"type":"object","properties":{"query":{"type":"string","description":"Search query for tools or connectors to load."},"limit":{"type":"integer","description":"Maximum number of tool groups to return."}},"required":["query"]}},
			{"type":"function","name":"codex_app__load_workspace_dependencies","parameters":{"type":"object","properties":{},"additionalProperties":false}},
			{"type":"function","name":"multi_agent_v1__spawn_agent","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}},
			{"type":"function","name":"multi_agent_v1__wait_agent","parameters":{"type":"object","properties":{"timeout_ms":{"type":"integer"}},"additionalProperties":false}}
		],
		"input":[
			{"type":"function_call","call_id":"call_fixture","name":"tool_search","arguments":"{\"query\":\"subagent\"}"},
			{"type":"function_call_output","call_id":"call_fixture","output":`+string(mustMarshalJSONForTest(t, output))+`}
		]
	}`, string(patched))
}

func TestPatchGrokResponsesBodyWithClientToolsRewritesEveryToolChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		choice   string
		wantName string
		wantType string
		wantNoNS bool
	}{
		{
			name:     "custom",
			choice:   `{"type":"custom","name":"apply_patch"}`,
			wantName: "apply_patch",
			wantType: "function",
		},
		{
			name:     "tool search",
			choice:   `{"type":"tool_search"}`,
			wantName: "tool_search",
			wantType: "function",
		},
		{
			name:     "namespace function",
			choice:   `{"type":"function","namespace":"collaboration","name":"send_message"}`,
			wantName: "collaboration__send_message",
			wantType: "function",
			wantNoNS: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(fmt.Sprintf(`{
				"model":"grok","input":"hello",
				"tools":[
					{"type":"custom","name":"apply_patch"},
					{"type":"tool_search"},
					{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"send_message","parameters":{"type":"object"}}]}
				],
				"tool_choice":%s
			}`, tt.choice))

			patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
			require.NoError(t, err)
			require.Equal(t, tt.wantType, gjson.GetBytes(patched, "tool_choice.type").String())
			require.Equal(t, tt.wantName, gjson.GetBytes(patched, "tool_choice.name").String())
			if tt.wantNoNS {
				require.False(t, gjson.GetBytes(patched, "tool_choice.namespace").Exists())
			}
		})
	}
}

func TestPatchGrokResponsesBodyWithClientToolsRejectsTrailingJSONDocument(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"grok","input":"hello","tools":[{"type":"custom","name":"apply_patch"}]} {"ignored":true}`)
	patched, mapping, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")

	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "invalid json")
	require.Nil(t, patched)
	require.Empty(t, mapping.CustomTools)
}

func mustMarshalJSONForTest(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
