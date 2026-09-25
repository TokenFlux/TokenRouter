//go:build unit

package grok_test

import (
	"encoding/json"
	"testing"

	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPatchGrokResponsesBodyPreservesGrokShellFunctionOutputImages(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"grok-4.6",
		"input":[
			{"type":"function_call","call_id":"call_read","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_read","content":"Read image file: /tmp/example.png","images":[{"type":"image","url":"data:image/png;base64,QUE="}]}
		]
	}`)

	patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "function_call_output", gjson.GetBytes(patched, "input.1.type").String())
	require.Equal(t, "Read image file: /tmp/example.png", gjson.GetBytes(patched, "input.1.output").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.2.type").String())
	require.Equal(t, "input_image", gjson.GetBytes(patched, "input.2.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QUE=", gjson.GetBytes(patched, "input.2.content.1.image_url").String())
}

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

	patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "Read image file: /tmp/example.png", gjson.GetBytes(patched, "input.1.output").String())
	require.Equal(t, "input_image", gjson.GetBytes(patched, "input.2.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QUE=", gjson.GetBytes(patched, "input.2.content.1.image_url").String())
}

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

	patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.Len(t, gjson.GetBytes(patched, "tools").Array(), 3)
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="namespace")`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(type=="function")`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(type=="shell")`).Exists())
	require.Equal(t, "functions__inner", gjson.GetBytes(patched, "tools.0.name").String())
	require.Equal(t, "functions__inner", gjson.GetBytes(patched, "tool_choice.name").String())
	require.False(t, gjson.GetBytes(patched, "tool_choice.namespace").Exists())
}

func TestSanitizeGrokResponsesToolsRemovesDeferredFlagsWithToolSearch(t *testing.T) {
	body := []byte(`{"tools":[{"type":"tool_search"},{"type":"function","name":"shell","defer_loading":true},{"type":"function","name":"apply_patch"}]}`)

	patched, err := (grok.BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, `tools.#(type=="tool_search")`).Exists())
	require.False(t, gjson.GetBytes(patched, `tools.#(name=="shell").defer_loading`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(name=="apply_patch")`).Exists())
}

func TestSanitizeGrokResponsesToolsSimplifiesInvalidRootUnion(t *testing.T) {
	body := []byte(`{"tools":[
		{"type":"function","name":"mcp__codex_app__automation_update","strict":true,"parameters":{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},{"type":"null"}]}},
		{"type":"function","name":"object_only","strict":true,"parameters":{"type":"object","anyOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"type":"object","properties":{"b":{"type":"integer"}}}]}}
	]}`)

	patched, err := (grok.BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools(body)
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

	patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.6")
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

func TestSanitizeGrokResponsesToolsKeepsToolChoiceOnlyWithSupportedTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           string
		wantTools      bool
		wantToolChoice bool
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
			patched, err := (grok.BodyCodec{NewID: uuid.NewString}).SanitizeGrokResponsesTools([]byte(tt.body))
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

	patched, _, err := (grok.BodyCodec{NewID: uuid.NewString}).PatchGrokResponsesBodyWithClientTools(body, "grok-4.5")
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

func TestBuildGrokCompactRequestBodyUsesResponsesCompactionTurn(t *testing.T) {
	body := []byte(`{"model":"grok-4.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"shell"}],"stream":true}`)

	patched, err := (grok.BodyCodec{NewID: uuid.NewString}).BuildGrokCompactRequestBody(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "stream").Bool())
	require.False(t, gjson.GetBytes(patched, "store").Bool())
	require.Equal(t, "none", gjson.GetBytes(patched, "tool_choice").String())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(patched, "include.0").String())
	require.Equal(t, "hello", gjson.GetBytes(patched, "input.0.content.0.text").String())
	prompt := gjson.GetBytes(patched, "input.1.content.0.text").String()
	require.Contains(t, prompt, "1. Primary Request and Intent")
	require.Contains(t, prompt, "9. Optional Next Step")
	require.Contains(t, prompt, "Respond with ONLY the <summary>...</summary> block")
	require.NotContains(t, prompt, "<summary_request>")
}

func TestConvertGrokResponseToOpenAICompact(t *testing.T) {
	body := []byte(`{
		"id":"resp_grok_1",
		"object":"response",
		"status":"completed",
		"model":"grok-4.5",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"grok-encrypted-state"},
			{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"summary text"}]}
		],
		"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}
	}`)

	converted, err := (grok.BodyCodec{NewID: uuid.NewString}).ConvertGrokResponseToOpenAICompact(body)
	require.NoError(t, err)
	require.Equal(t, "resp_grok_1", gjson.GetBytes(converted, "id").String())
	require.Len(t, gjson.GetBytes(converted, "output").Array(), 1)
	require.Equal(t, "compaction", gjson.GetBytes(converted, "output.0.type").String())
	require.Equal(t, "grok-encrypted-state", gjson.GetBytes(converted, "output.0.encrypted_content").String())
	require.Equal(t, "summary text", gjson.GetBytes(converted, "output.0.summary.0.text").String())
	require.Equal(t, int64(14), gjson.GetBytes(converted, "usage.total_tokens").Int())
}

func TestConvertGrokResponseToOpenAICompactRequiresEncryptedContent(t *testing.T) {
	_, err := (grok.BodyCodec{NewID: uuid.NewString}).ConvertGrokResponseToOpenAICompact([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"summary"}]}]}`))
	require.ErrorContains(t, err, "reasoning.encrypted_content")
}

func TestGrokMediaGenerationGateCoversImagesAndVideo(t *testing.T) {
	tests := []struct {
		name     string
		endpoint grok.GrokMediaEndpoint
		want     bool
	}{
		{name: "image generation", endpoint: grok.GrokMediaEndpointImagesGenerations, want: true},
		{name: "image edit", endpoint: grok.GrokMediaEndpointImagesEdits, want: true},
		{name: "video generation", endpoint: grok.GrokMediaEndpointVideosGenerations, want: true},
		{name: "video edit", endpoint: grok.GrokMediaEndpointVideosEdits, want: true},
		{name: "video extension", endpoint: grok.GrokMediaEndpointVideosExtensions, want: true},
		{name: "video status", endpoint: grok.GrokMediaEndpointVideoStatus, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.endpoint.IsGenerationRequest())
		})
	}
}
