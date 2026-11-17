package grok

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	groktestkit "github.com/TokenFlux/TokenRouter/internal/upstream/grok/testkit"
)

// Grok patching needs replay images and client-tool identity to survive
// adaptation while unsupported request controls disappear. These fixtures
// inspect extracted image messages, namespace/custom/search mappings, promoted
// tool precedence and schema repair, then distinguish known reasoning capability
// limits from unknown vendor-qualified model identities.

// TestPatchGrokResponsesBodyPreservesGrokShellFunctionOutputImages supplies
// shell-style output text with a separate image array. Patching must retain
// function output text and append a user image message containing the same
// encoded image URL.
func TestPatchGrokResponsesBodyPreservesGrokShellFunctionOutputImages(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"grok-4.6",
		"input":[
			{"type":"function_call","call_id":"call_read","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_read","content":"Read image file: /tmp/example.png","images":[{"type":"image","url":"data:image/png;base64,QUE="}]}
		]
	}`)

	patched, _, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "function_call_output", gjson.GetBytes(patched, "input.1.type").String())
	require.Equal(t, "Read image file: /tmp/example.png", gjson.GetBytes(patched, "input.1.output").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.2.type").String())
	require.Equal(t, "input_image", gjson.GetBytes(patched, "input.2.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QUE=", gjson.GetBytes(patched, "input.2.content.1.image_url").String())
}

// TestPatchGrokResponsesBodyPreservesGrok105StructuredFunctionOutputImages
// supplies structured text and image parts inside a tool output. Patching must
// extract the text into function output and retain the image URL in the appended
// input_image part.
func TestPatchGrokResponsesBodyPreservesGrok105StructuredFunctionOutputImages(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"grok-4.6",
		"input":[
			{"type":"function_call","call_id":"call_read","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_read","output":[
				{"type":"input_text","text":"Read image file: /tmp/example.png"},
				{"type":"input_image","detail":"auto","image_url":"data:image/png;base64,QUE="}
			]}
		]
	}`)

	patched, _, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "Read image file: /tmp/example.png", gjson.GetBytes(patched, "input.1.output").String())
	require.Equal(t, "input_image", gjson.GetBytes(patched, "input.2.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QUE=", gjson.GetBytes(patched, "input.2.content.1.image_url").String())
}

// TestPatchGrokResponsesBodyFlattensNamespaceTools combines a default-scope
// function with flat function and shell tools. Patching keeps its declaration
// and choice bare, records default-scope restoration, and applies the selected
// upstream model while retaining all three supported tools.
func TestPatchGrokResponsesBodyFlattensNamespaceTools(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"tools": [
			{"type": "namespace", "name": "functions", "tools": [{"type": "function", "name": "inner"}]},
			{"type": "function", "name": "kept_fn", "parameters": {"type": "object"}},
			{"type": "shell", "name": "kept_shell"}
		],
		"tool_choice": {"type": "function", "namespace": "functions", "name": "inner"}
	}`)

	patched, mapping, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.Len(t, gjson.GetBytes(patched, "tools").Array(), 3)
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="namespace")`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(type=="function")`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(type=="shell")`).Exists())
	require.Equal(t, "inner", gjson.GetBytes(patched, "tools.0.name").String())
	require.Equal(t, "inner", gjson.GetBytes(patched, "tool_choice.name").String())
	require.False(t, gjson.GetBytes(patched, "tool_choice.namespace").Exists())
	require.Equal(t, bridge.ResponsesNamespaceName{Name: "inner"}, mapping.NamespaceTools["inner"])
}

// TestSanitizeGrokResponsesToolsRemovesDeferredFlagsWithToolSearch supplies an
// unsupported raw tool_search type and a deferred function. Sanitization must
// remove the raw search tool and the shell defer_loading field, and retain the
// apply_patch declaration.
func TestSanitizeGrokResponsesToolsRemovesDeferredFlagsWithToolSearch(t *testing.T) {
	body := []byte(`{"tools":[{"type":"tool_search"},{"type":"function","name":"shell","defer_loading":true},{"type":"function","name":"apply_patch"}]}`)

	patched, err := (BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="tool_search")`).Exists())
	require.False(t, gjson.GetBytes(patched, `tools.#(name=="shell").defer_loading`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(name=="apply_patch")`).Exists())
}

// TestSanitizeGrokResponsesToolsSimplifiesInvalidRootUnion compares a mixed
// object/null union with an object-only union. The mixed schema must become
// permissive object parameters with strict=false. The object-only fixture must
// still have an anyOf field and strict=true.
func TestSanitizeGrokResponsesToolsSimplifiesInvalidRootUnion(t *testing.T) {
	body := []byte(`{"tools":[
		{"type":"function","name":"mcp__codex_app__automation_update","strict":true,"parameters":{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},{"type":"null"}]}},
		{"type":"function","name":"object_only","strict":true,"parameters":{"type":"object","anyOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"type":"object","properties":{"b":{"type":"integer"}}}]}}
	]}`)

	patched, err := (BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools(body)
	require.NoError(t, err)
	require.True(t, json.Valid(patched))

	mixed := gjson.GetBytes(patched, `tools.#(name=="mcp__codex_app__automation_update")`)
	require.Equal(t, "object", mixed.Get("parameters.type").String())
	require.True(t, mixed.Get("parameters.properties").IsObject())
	require.True(t, mixed.Get("parameters.additionalProperties").Bool())
	require.False(t, mixed.Get("parameters.oneOf").Exists())
	require.Equal(t, gjson.False, mixed.Get("strict").Type)

	objectOnly := gjson.GetBytes(patched, `tools.#(name=="object_only")`)
	require.True(t, objectOnly.Get("parameters.anyOf").Exists())
	require.Equal(t, gjson.True, objectOnly.Get("strict").Type)
}

// TestPatchGrokResponsesBodySimplifiesTypedInvalidRootUnion nests a
// reference/null union inside a namespace tool with metadata. Patching must
// flatten its name, remove metadata, replace the schema and definitions with
// permissive object parameters, and disable strict.
func TestPatchGrokResponsesBodySimplifiesTypedInvalidRootUnion(t *testing.T) {
	body := []byte(`{
		"model":"grok-4.6",
		"metadata":{"session_id":"abc"},
		"tools":[{
			"type":"namespace",
			"name":"mcp__codex_app",
			"tools":[{
				"type":"function",
				"name":"automation_update",
				"strict":true,
				"parameters":{
					"type":"object",
					"oneOf":[{"$ref":"#/$defs/update"},{"type":"null"}],
					"$defs":{"update":{"type":"object","properties":{"id":{"type":"string"}}}}
				}
			}]
		}]
	}`)

	patched, _, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, gjson.GetBytes(patched, "metadata").Exists())

	tool := gjson.GetBytes(patched, `tools.#(name=="mcp__codex_app__automation_update")`)
	require.Equal(t, "object", tool.Get("parameters.type").String())
	require.True(t, tool.Get("parameters.properties").IsObject())
	require.True(t, tool.Get("parameters.additionalProperties").Bool())
	require.False(t, tool.Get("parameters.oneOf").Exists())
	require.False(t, tool.Get("parameters.$defs").Exists())
	require.Equal(t, gjson.False, tool.Get("strict").Type)
}

// TestSanitizeGrokResponsesToolsKeepsToolChoiceOnlyWithSupportedTools compares
// absent, empty, unsupported, supported and malformed declaration sets. It
// asserts tools-field presence independently from tool_choice presence,
// including retained empty/malformed fields after their orphan controls
// disappear.
func TestSanitizeGrokResponsesToolsKeepsToolChoiceOnlyWithSupportedTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // scenario display label
		body string // supplied request JSON
		// wantTools and wantToolChoice describe independent field-presence assertions.
		// Empty or malformed tools can remain in the request while their orphan tool
		// choice has already been removed.
		wantTools      bool // expected tools-field presence
		wantToolChoice bool // expected choice-field presence
	}{
		{
			name: "missing tools with string tool choice",
			body: `{"input":"hello","tool_choice":"auto"}`,
		},
		{
			name: "missing tools with object tool choice",
			body: `{"input":"hello","tool_choice":{"type":"function","name":"lookup"}}`,
		},
		{
			name:      "empty tools",
			body:      `{"input":"hello","tools":[],"tool_choice":"auto"}`,
			wantTools: true,
		},
		{
			name: "all tools unsupported",
			body: `{"input":"hello","tools":[{"type":"namespace","name":"client_tools"}],"tool_choice":"auto"}`,
		},
		{
			name:           "supported tool",
			body:           `{"input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":"auto"}`,
			wantTools:      true,
			wantToolChoice: true,
		},
		{
			name:      "malformed non-array tools drop orphan controls",
			body:      `{"input":"hello","tools":{"type":"function","name":"lookup"},"tool_choice":"auto"}`,
			wantTools: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := (BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools([]byte(tt.body))
			require.NoError(t, err)
			require.True(t, json.Valid(patched))
			require.Equal(t, tt.wantTools, gjson.GetBytes(patched, "tools").Exists())
			require.Equal(t, tt.wantToolChoice, gjson.GetBytes(patched, "tool_choice").Exists())
			if tt.wantToolChoice {
				require.Equal(t, "auto", gjson.GetBytes(patched, "tool_choice").String())
			}
		})
	}
}

// TestPatchGrokResponsesBodyPromotesCodexAdditionalTools combines top-level
// declarations with duplicated and new carried tools. Patching must keep earlier
// definitions first, lower the custom tool, remove carrier items and retain
// ordinary developer/user messages and automatic choice.
func TestPatchGrokResponsesBodyPromotesCodexAdditionalTools(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"tools": [
			{"type": "function", "name": "existing", "description": "top-level wins"},
			{"type": "web_search"}
		],
		"tool_choice": "auto",
		"input": [
			{
				"type": "additional_tools",
				"role": "developer",
				"tools": [
					{"type": "function", "name": "existing", "description": "duplicate carrier definition"},
					{"type": "function", "name": "wait"},
					{"type": "web_search"},
					{"type": "shell"},
					{"type": "custom", "name": "apply_patch"},
					{"type": "namespace", "name": "collaboration"}
				]
			},
			{
				"type": "message",
				"role": "developer",
				"content": [{"type": "input_text", "text": "system prompt"}]
			},
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "hello"}]
			}
		]
	}`)

	patched, _, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.5", gjson.GetBytes(patched, "model").String())
	require.Equal(t, 2, len(gjson.GetBytes(patched, "input").Array()))
	require.False(t, gjson.GetBytes(patched, `input.#(type=="additional_tools")`).Exists())
	tools := gjson.GetBytes(patched, "tools").Array()
	require.Len(t, tools, 5)
	require.Equal(t, "existing", tools[0].Get("name").String())
	require.Equal(t, "top-level wins", tools[0].Get("description").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "wait", tools[2].Get("name").String())
	require.Equal(t, "shell", tools[3].Get("type").String())
	require.Equal(t, "function", tools[4].Get("type").String())
	require.Equal(t, "apply_patch", tools[4].Get("name").String())
	require.Equal(t, "string", tools[4].Get("parameters.properties.input.type").String())
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="namespace")`).Exists())
	require.Equal(t, "auto", gjson.GetBytes(patched, "tool_choice").String())
	require.Equal(t, "developer", gjson.GetBytes(patched, "input.0.role").String())
	require.Equal(t, "system prompt", gjson.GetBytes(patched, "input.0.content.0.text").String())
	require.Equal(t, "user", gjson.GetBytes(patched, "input.1.role").String())
	require.Equal(t, "hello", gjson.GetBytes(patched, "input.1.content.0.text").String())
}

// TestPatchGrokResponsesBodyWithClientToolsReplaysUndeclaredHistory supplies
// retained MCP history after its declaration disappears. Grok preparation keeps
// the call and result paired without adding that historical tool to callable state.
func TestPatchGrokResponsesBodyWithClientToolsReplaysUndeclaredHistory(t *testing.T) {
	body := []byte(`{"model":"grok","input":[{"type":"function_call","namespace":"mcp__cua_proxy","name":"js","call_id":"old","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"ok"}],"tools":[{"type":"tool_search"}]}`)
	patched, mapping, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "mcp__cua_proxy__js", gjson.GetBytes(patched, "input.0.name").String())
	require.False(t, gjson.GetBytes(patched, "input.0.namespace").Exists())
	require.Equal(t, "old", gjson.GetBytes(patched, "input.1.call_id").String())
	require.Equal(t, int64(1), gjson.GetBytes(patched, "tools.#").Int())
	require.True(t, mapping.ToolSearch)
	require.NotContains(t, mapping.NamespaceTools, "mcp__cua_proxy__js")
}

// TestPatchGrokResponsesBodyWithClientToolsLowersCodexProtocol uses the shared
// custom, search and namespace replay fixture. It asserts matching restoration
// maps, function declarations and choice, serialized call arguments/outputs,
// qualified names and removal of client-only protocol fields.
func TestPatchGrokResponsesBodyWithClientToolsLowersCodexProtocol(t *testing.T) {
	t.Parallel()

	body := groktestkit.ClientToolsRequest(false)
	patched, mapping, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
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

// TestPatchGrokResponsesBodyWithClientToolsLowersDiscoveredToolsOutput supplies
// namespaced declarations discovered by a prior client search. Patching must add
// flattened functions and restoration mappings while retaining the discovered
// namespace payload as serialized function output with matching call IDs.
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

	patched, mapping, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
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

// TestPatchGrokResponsesBodyWithClientToolsRewritesEveryToolChoice compares
// custom, search and namespace function selections. Each choice must become the
// matching function name/type, and the namespace selection must lose its
// separate namespace field.
func TestPatchGrokResponsesBodyWithClientToolsRewritesEveryToolChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string // scenario display label
		choice string // client choice JSON
		// wantName and wantType identify the rewritten function choice together. The
		// cases compare each client's custom, search or namespace selection with the
		// function shape forwarded upstream.
		wantName string // expected function identity
		wantType string // expected choice type
		// wantNoNS requires the namespace field to disappear for the namespaced
		// fixture; other cases assert the rewritten name and type.
		wantNoNS bool // namespace-field absence expected
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

			patched, _, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
			require.NoError(t, err)
			require.Equal(t, tt.wantType, gjson.GetBytes(patched, "tool_choice.type").String())
			require.Equal(t, tt.wantName, gjson.GetBytes(patched, "tool_choice.name").String())
			if tt.wantNoNS {
				require.False(t, gjson.GetBytes(patched, "tool_choice.namespace").Exists())
			}
		})
	}
}

// TestPatchGrokResponsesBodyWithClientToolsRejectsTrailingJSONDocument appends
// another object after an otherwise usable request. Preparation must report
// invalid JSON, return no patched body and leave the custom-tool mapping empty.
func TestPatchGrokResponsesBodyWithClientToolsRejectsTrailingJSONDocument(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"grok","input":"hello","tools":[{"type":"custom","name":"apply_patch"}]} {"ignored":true}`)
	patched, mapping, err := (BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")

	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "invalid json")
	require.Nil(t, patched)
	require.Empty(t, mapping.CustomTools)
}

// mustMarshalJSONForTest encodes a string as JSON for nested expected-response
// text. It marks the call as a test helper and stops the test on encoding
// failure; callers use the bytes to preserve string escaping in JSONEq fixtures.
func mustMarshalJSONForTest(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

// TestQualifiedGrokReasoningPreservesModel compares known and unknown
// vendor-qualified 4.6 model spellings through the offline catalog. Capability
// reports must match known identities while Responses/Chat normalization retains
// the full model and explicit high/xhigh effort values.
func TestQualifiedGrokReasoningPreservesModel(t *testing.T) {
	codec := BodyCodec{Attributes: func(model string) modelcatalog.Attributes {
		return testkit.OfflineView().Resolve(modelrules.Target{Platform: "grok", Model: model}, modelcatalog.Attributes{}).Attributes
	}}
	for _, model := range []string{"grok-4.6", "xai/grok-4.6", "x-ai/grok-4.6", "grok/grok-4.6", "X-AI/GROK-4.6"} {
		t.Run(model, func(t *testing.T) {
			known := model == "grok-4.6" || model == "xai/grok-4.6"
			require.Equal(t, known, codec.GrokSupportsReasoningEffort(model))
			require.Equal(t, known, codec.GrokSupportsXHighReasoningEffort(model))
			responses, err := codec.PatchGrokResponsesBody([]byte(`{"model":"client-alias","input":"hello","reasoning":{"effort":"xhigh"}}`), model)
			require.NoError(t, err)
			require.Equal(t, model, gjson.GetBytes(responses, "model").String())
			require.Equal(t, "xhigh", gjson.GetBytes(responses, "reasoning.effort").String())

			for _, effort := range []string{"high", "xhigh"} {
				body := []byte(`{"model":"` + model + `","reasoning_effort":"` + effort + `"}`)
				chat, err := codec.NormalizeGrokChatReasoningEffort(body, model)
				require.NoError(t, err)
				require.Equal(t, model, gjson.GetBytes(chat, "model").String())
				require.Equal(t, effort, gjson.GetBytes(chat, "reasoning_effort").String())
			}
			require.Equal(t, model, NormalizeModelID(model))
			require.Equal(t, model, ResolveGrokTextResponsesModelID(model))
		})
	}
}

// TestQualifiedGrokReasoningKeepsCapabilityBounds compares an explicit high-only
// capability with catalog and unknown qualified identities. Configured bounds
// clamp xhigh while catalog-supported and unknown effort values remain intact;
// complete model IDs and absent effort retain their supplied representation.
func TestQualifiedGrokReasoningKeepsCapabilityBounds(t *testing.T) {
	// Keep a configured upper bound independent of changing catalog capabilities
	// so snapshot refreshes cannot remove the coverage of effort clamping.
	high := "high"
	limited := []modelcatalog.ReasoningOption{{Type: "effort", Values: []*string{&high}}}
	codec := BodyCodec{Attributes: func(model string) modelcatalog.Attributes {
		if model == "xai/high-only-model" {
			return modelcatalog.Attributes{ReasoningOptions: &limited}
		}
		return testkit.OfflineView().Resolve(modelrules.Target{Platform: "grok", Model: model}, modelcatalog.Attributes{}).Attributes
	}}
	for _, tc := range []struct {
		model string // qualified model fixture
		want  string // expected normalized effort
	}{
		{model: "xai/high-only-model", want: "high"},
		{model: "xai/grok-4.5", want: "xhigh"},
		{model: "x-ai/grok-composer-2.5-fast", want: "xhigh"},
		{model: "xai/grok-4.6-high", want: "xhigh"},
		{model: "unknown/grok-4.6", want: "xhigh"},
		{model: "xai/custom/grok-4.6", want: "xhigh"},
		{model: "xai/gpt-5.6-sol", want: "xhigh"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			responses, err := codec.PatchGrokResponsesBody([]byte(`{"input":"hello","reasoning":{"effort":"xhigh"}}`), tc.model)
			require.NoError(t, err)
			require.Equal(t, tc.model, gjson.GetBytes(responses, "model").String())
			require.Equal(t, tc.want, gjson.GetBytes(responses, "reasoning.effort").String())
			chat, err := codec.NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"xhigh"}`), tc.model)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(chat, "reasoning_effort").String())
		})
	}
	for _, model := range []string{"xai/grok-4.6", "xai/grok-4.6-xhigh"} {
		body, err := codec.PatchGrokResponsesBody([]byte(`{"input":"hello"}`), model)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "reasoning.effort").Exists())
		require.Equal(t, model, gjson.GetBytes(body, "model").String())
	}
}
