//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	grokRateLimitFallbackCooldown    = 2 * time.Minute
	grokRateLimitRepeatCooldown      = 10 * time.Minute
	grokRateLimitSustainedCooldown   = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown = time.Hour
	grokRateLimitBackoffQuietPeriod  = time.Hour
)

func TestPatchGrokResponsesBodySetsMappedModelAndDropsUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"prompt_cache_retention": "24h",
		"safety_identifier": "user-1",
		"reasoning": {"effort": "high"}
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "prompt_cache_retention").Exists())
	require.False(t, gjson.GetBytes(patched, "safety_identifier").Exists())
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning.effort").String())
}

func TestPatchGrokResponsesBodyDropsRedundantViewImageForCurrentInlineImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "top-level tools",
			body: `{
				"model":"grok-4.6",
				"input":[{"type":"message","role":"user","content":[
					{"type":"input_text","text":"What text is in this image?"},
					{"type":"input_image","image_url":"data:image/png;base64,AA=="}
				]}],
				"tools":[
					{"type":"function","name":"view_image","parameters":{"type":"object"}},
					{"type":"function","name":"shell_command","parameters":{"type":"object"}}
				]
			}`,
		},
		{
			name: "Responses Lite additional tools",
			body: `{
				"model":"grok-4.6",
				"input":[
					{"type":"additional_tools","role":"developer","tools":[
						{"type":"function","name":"view_image","parameters":{"type":"object"}},
						{"type":"function","name":"shell_command","parameters":{"type":"object"}}
					]},
					{"type":"message","role":"user","content":[
						{"type":"input_text","text":"What text is in this image?"},
						{"type":"input_image","image_url":"data:image/png;base64,AA=="}
					]}
				]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(patched, `tools.#(name=="view_image")`).Exists())
			require.Equal(t, "shell_command", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

func TestPatchGrokResponsesBodyKeepsNonRedundantViewImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "current turn has no inline image",
			body: `{"input":[{"role":"user","content":[{"type":"input_text","text":"Inspect a local image"}]}],"tools":[{"type":"function","name":"view_image"}]}`,
		},
		{
			name: "inline image is only historical",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]},{"role":"assistant","content":[{"type":"output_text","text":"Done"}]},{"role":"user","content":[{"type":"input_text","text":"Inspect another local image"}]}],"tools":[{"type":"function","name":"view_image"}]}`,
		},
		{
			name: "view image is explicitly selected",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"view_image"}],"tool_choice":{"type":"function","name":"view_image"}}`,
		},
		{
			name: "required with view image as the only tool",
			body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"view_image"}],"tool_choice":"required"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), "grok-4.6")
			require.NoError(t, err)
			require.Equal(t, "view_image", gjson.GetBytes(patched, "tools.0.name").String())
		})
	}
}

func TestPatchGrokResponsesBodyDropsViewImageOnlyToolMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],
		"tools":[{"type":"function","name":"view_image"}],
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.6")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists())
}

func TestPatchGrokResponsesBodyPreservesGrokShellFunctionOutputImages(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model":"grok-4.6",
		"input":[
			{"type":"function_call","call_id":"call_read","name":"read_file","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_read","content":"Read image file: /tmp/example.png","images":[{"type":"image","url":"data:image/png;base64,QUE="}]}
		]
	}`)

	patched, _, err := patchGrokResponsesBodyWithClientTools(body, "grok-4.6")
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

	patched, _, err := patchGrokResponsesBodyWithClientTools(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "Read image file: /tmp/example.png", gjson.GetBytes(patched, "input.1.output").String())
	require.Equal(t, "input_image", gjson.GetBytes(patched, "input.2.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QUE=", gjson.GetBytes(patched, "input.2.content.1.image_url").String())
}

func TestPatchGrokResponsesBodySanitizesComposerReasoningParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		upstreamModel string
		wantReasoning bool
	}{
		{name: "composer fast", upstreamModel: "grok-composer-2.5-fast"},
		{name: "composer shorthand", upstreamModel: "grok-composer"},
		{name: "composer legacy alias", upstreamModel: "composer-2.5"},
		{name: "provider-prefixed composer", upstreamModel: "xai/grok-composer-2.5-fast"},
		{name: "grok 4.5", upstreamModel: "grok-4.5", wantReasoning: true},
		{name: "grok 4.6", upstreamModel: "grok-4.6", wantReasoning: true},
		{name: "grok 4.6 latest", upstreamModel: "grok-4.6-latest", wantReasoning: true},
	}

	bodyTemplate := []byte(`{
		"model": "grok",
		"input": "hello",
		"reasoning": {"effort": "medium", "summary": "auto"},
		"reasoning_effort": "medium",
		"reasoningEffort": "medium"
	}`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(append([]byte(nil), bodyTemplate...), tt.upstreamModel)
			require.NoError(t, err)
			require.True(t, json.Valid(patched))
			require.Equal(t, tt.upstreamModel, gjson.GetBytes(patched, "model").String())

			if tt.wantReasoning {
				require.Equal(t, "medium", gjson.GetBytes(patched, "reasoning.effort").String())
				require.Equal(t, "medium", gjson.GetBytes(patched, "reasoning_effort").String())
				require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
				return
			}

			require.False(t, gjson.GetBytes(patched, "reasoning").Exists())
			require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists())
			require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
		})
	}
}

func TestExtractGrokResponsesReasoningEffortSupportsOpenAICompatibleField(t *testing.T) {
	t.Parallel()

	effort := requeststate.ExtractOpenAIReasoningEffortFromBody(
		[]byte(`{"model":"grok-4.3","reasoning_effort":"high"}`),
		"grok-4.3",
	)
	require.NotNil(t, effort)
	require.Equal(t, "high", *effort)
}

func TestPatchGrokResponsesBodyDropsGrok45ReasoningUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": "hello",
		"presence_penalty": 0.1,
		"presencePenalty": 0.2,
		"frequency_penalty": 0.3,
		"frequencyPenalty": 0.4,
		"stop": ["done"]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.5", gjson.GetBytes(patched, "model").String())
	require.False(t, gjson.GetBytes(patched, "presence_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "presencePenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequency_penalty").Exists())
	require.False(t, gjson.GetBytes(patched, "frequencyPenalty").Exists())
	require.False(t, gjson.GetBytes(patched, "stop").Exists())
}

func TestPatchGrokResponsesBodyKeepsPenaltyAndStopFieldsForNon45Models(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-4.3",
		"input": "hello",
		"presence_penalty": 0.1,
		"frequency_penalty": 0.2,
		"stop": ["done"]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.Equal(t, "grok-4.3", gjson.GetBytes(patched, "model").String())
	require.Equal(t, 0.1, gjson.GetBytes(patched, "presence_penalty").Float())
	require.Equal(t, 0.2, gjson.GetBytes(patched, "frequency_penalty").Float())
	require.Len(t, gjson.GetBytes(patched, "stop").Array(), 1)
}

func TestPatchGrokResponsesBodyDropsLogprobsForGrok420Family(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"grok-4.20-0309-reasoning","input":"hello","logprobs":true,"top_logprobs":5}`)
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.20-0309-reasoning")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "logprobs").Exists())
	require.False(t, gjson.GetBytes(patched, "top_logprobs").Exists())
}

func TestPatchGrokResponsesBodyNormalizesReasoningEffortAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		body          string
		upstreamModel string
		path          string
		want          string
	}{
		{name: "minimal nested", body: `{"input":"hi","reasoning":{"effort":"minimal"}}`, upstreamModel: "grok-4.5", path: "reasoning.effort", want: "low"},
		{name: "xhigh stays high for 4.5", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.5", path: "reasoning_effort", want: "high"},
		{name: "xhigh nested for 4.6", body: `{"input":"hi","reasoning":{"effort":"xhigh"}}`, upstreamModel: "grok-4.6", path: "reasoning.effort", want: "xhigh"},
		{name: "xhigh snake for 4.6 latest", body: `{"input":"hi","reasoning_effort":"xhigh"}`, upstreamModel: "grok-4.6-latest", path: "reasoning_effort", want: "xhigh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(tt.body), tt.upstreamModel)
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(patched, tt.path).String(), string(patched))
			require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())
		})
	}
}

func TestPatchGrokResponsesBodyAddsDefaultFunctionParameters(t *testing.T) {
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(
		[]byte(`{"input":"hi","tools":[{"type":"function","name":"lookup","large_id":9007199254740993},{"type":"function","name":"wait","parameters":null}]}`),
		"grok-4.5",
	)
	require.NoError(t, err)
	for _, tool := range gjson.GetBytes(patched, "tools").Array() {
		require.Equal(t, "object", tool.Get("parameters.type").String(), string(patched))
		require.True(t, tool.Get("parameters.properties").IsObject(), string(patched))
	}
	require.Equal(t, "9007199254740993", gjson.GetBytes(patched, "tools.0.large_id").Raw)
}

func TestNormalizeGrokChatReasoningEffort(t *testing.T) {
	patched, err := gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoningEffort":"ultra"}`), "grok-4.3")
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(patched, "reasoning_effort").String())
	require.False(t, gjson.GetBytes(patched, "reasoningEffort").Exists())

	patched, err = gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"xhigh"}`), "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(patched, "reasoning_effort").String())

	patched, err = gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort([]byte(`{"reasoning_effort":"high"}`), "grok-composer-2.5-fast")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists())
}

func TestPatchGrokResponsesBodyDropsNestedUnsupportedFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"external_web_access": true,
		"tools": [
			{"type": "function", "name": "kept_fn", "external_web_access": true, "parameters": {"type": "object", "properties": {"q": {"type": "string", "external_web_access": true}}}}
		],
		"metadata": {"external_web_access": false, "large_id": 9007199254740993}
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, strings.Contains(string(patched), "external_web_access"))
	require.Equal(t, "kept_fn", gjson.GetBytes(patched, "tools.0.name").String())
	require.False(t, gjson.GetBytes(patched, "metadata").Exists())
}

func TestStripAnthropicThinkingSignaturesPreservesLargeIntegers(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"work","signature":"opaque"}]}],"metadata":{"large_id":9007199254740993}}`)

	patched, changed := anthropic.StripThinkingSignaturesJSON(body)

	require.True(t, changed)
	require.False(t, gjson.GetBytes(patched, "messages.0.content.0.signature").Exists())
	require.Equal(t, "9007199254740993", gjson.GetBytes(patched, "metadata.large_id").Raw)
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

	patched, _, err := patchGrokResponsesBodyWithClientTools(body, "grok-4.3")
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

func TestPatchGrokResponsesBodyDropsToolChoiceWhenNoSupportedToolsRemain(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok",
		"input": "hello",
		"tools": [
			{"type": "namespace", "namespace": "functions"},
			{"type": "image_generation", "model": "gpt-image-2"}
		],
		"tool_choice": {"type": "namespace", "namespace": "functions"}
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.3")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
}

func TestSanitizeGrokResponsesToolsRemovesDeferredFlagsWithToolSearch(t *testing.T) {
	body := []byte(`{"tools":[{"type":"tool_search"},{"type":"function","name":"shell","defer_loading":true},{"type":"function","name":"apply_patch"}]}`)

	patched, err := sanitizeGrokResponsesTools(body)
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

	patched, err := sanitizeGrokResponsesTools(body)
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

	patched, _, err := patchGrokResponsesBodyWithClientTools(body, "grok-4.6")
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
			patched, err := sanitizeGrokResponsesTools([]byte(tt.body))
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

	patched, _, err := patchGrokResponsesBodyWithClientTools(body, "grok-4.5")
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

func TestForwardGrokResponsesCodexAdditionalToolsUsesMixedCacheIntent(t *testing.T) {

	body := []byte(`{
		"model":"grok",
		"stream":false,
		"prompt_cache_key":"codex-session",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"function","name":"lookup","description":"look up a key","parameters":{"type":"object"}},
				{"type":"function","name":"web_search","description":"search","parameters":{"type":"object"}},
				{"type":"custom","name":"apply_patch"},
				{"type":"namespace","name":"collaboration"}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("X-Sub2API-Grok-Client-Tool-Cache", "prefer-cache")
	c.Set("api_key", &apikey.APIKey{ID: 4501})

	account := gatewaytestkit.HealthyGrokOAuthAccount(4501, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_codex_lite","object":"response","model":"grok-4.5","status":"completed",
			"output":[],"usage":{"input_tokens":10,"output_tokens":1}
		}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_codex_lite", result.ResponseID)
	require.False(t, gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools")`).Exists())
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 4)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "function", tools[2].Get("type").String())
	require.Equal(t, "apply_patch", tools[2].Get("name").String())
	require.Equal(t, "x_search", tools[3].Get("type").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="namespace")`).Exists())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Empty(t, upstream.lastReq.Header.Get("X-Sub2API-Grok-Client-Tool-Cache"))
}

func TestForwardGrokResponsesClaudeDesktopClientToolsUseCacheRoute(t *testing.T) {

	firstBody := []byte(`{
		"model":"grok","stream":false,"instructions":"You are Claude Desktop.",
		"tools":[
			{"type":"function","name":"Read","parameters":{"type":"object"}},
			{"type":"function","name":"Edit","parameters":{"type":"object"}},
			{"type":"function","name":"WebSearch","parameters":{"type":"object"}},
			{"type":"function","name":"mcp__workspace__bash","parameters":{"type":"object"}}
		],
		"input":[{"role":"user","content":[{"type":"input_text","text":"first turn"}]}]
	}`)
	secondBody := []byte(`{
		"model":"grok","stream":false,"instructions":"You are Claude Desktop.",
		"tools":[
			{"type":"function","name":"Read","parameters":{"type":"object"}},
			{"type":"function","name":"Edit","parameters":{"type":"object"}},
			{"type":"function","name":"WebSearch","parameters":{"type":"object"}},
			{"type":"function","name":"mcp__workspace__bash","parameters":{"type":"object"}}
		],
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"first turn"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"first answer"}]},
			{"role":"user","content":[{"type":"input_text","text":"second turn"}]}
		]
	}`)

	account := gatewaytestkit.HealthyGrokOAuthAccount(4504, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"resp_claude_desktop_1","object":"response","model":"grok-4.5","status":"completed",
				"output":[],"usage":{"input_tokens":30000,"output_tokens":10,"input_tokens_details":{"cached_tokens":0}}
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"resp_claude_desktop_2","object":"response","model":"grok-4.5","status":"completed",
				"output":[],"usage":{"input_tokens":30100,"output_tokens":12,"input_tokens_details":{"cached_tokens":28672}}
			}`)),
		},
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	newContext := func(body []byte) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("User-Agent", "claude-cli/2.1.215 (external, claude-desktop-3p, agent-sdk/0.3.215)")
		c.Request.Header.Set("X-App", "cli")
		c.Request.Header.Set("anthropic-client-platform", "desktop_app")
		c.Request.Header.Set("X-Claude-Code-Session-Id", "claude-desktop-session")
		c.Set("api_key", &apikey.APIKey{ID: 4504})
		return c
	}

	first, err := svc.Grok.ForwardResponses(context.Background(), newContext(firstBody), account, firstBody, "grok", false, time.Now())
	require.NoError(t, err)
	second, err := svc.Grok.ForwardResponses(context.Background(), newContext(secondBody), account, secondBody, "grok", false, time.Now())
	require.NoError(t, err)

	require.Equal(t, 0, first.Usage.CacheReadInputTokens)
	require.Equal(t, 28672, second.Usage.CacheReadInputTokens)
	require.Len(t, upstream.bodies, 2)
	require.Len(t, upstream.requests, 2)
	for i := range upstream.bodies {
		tools := gjson.GetBytes(upstream.bodies[i], "tools").Array()
		require.Len(t, tools, 6)
		require.Equal(t, "Read", tools[0].Get("name").String())
		require.Equal(t, "Edit", tools[1].Get("name").String())
		require.Equal(t, "WebSearch", tools[2].Get("name").String())
		require.Equal(t, "mcp__workspace__bash", tools[3].Get("name").String())
		require.Equal(t, "web_search", tools[4].Get("type").String())
		require.Equal(t, "x_search", tools[5].Get("type").String())
		require.False(t, gjson.GetBytes(upstream.bodies[i], "tool_choice").Exists())
		require.Empty(t, upstream.requests[i].Header.Get("X-App"))
		require.Empty(t, upstream.requests[i].Header.Get("anthropic-client-platform"))
		require.Empty(t, upstream.requests[i].Header.Get("X-Claude-Code-Session-Id"))
	}
	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.Equal(t, firstIdentity, upstream.requests[0].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, secondIdentity, upstream.requests[1].Header.Get(gatewayhttp.GrokConversationIDHeader))
}

func TestCodexUnsupportedAdditionalToolsDoNotBecomeToolFreeCacheIntent(t *testing.T) {
	body := []byte(`{
		"model":"grok","tool_choice":"auto",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"apply_patch"},
				{"type":"namespace","name":"collaboration"}
			]},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())

	mixedCacheIntent := patched
	patched, err = xai.ApplyGrokResponsesCacheIdentity(patched, body, "isolated-id", true)
	require.NoError(t, err)
	account := gatewaytestkit.HealthyGrokOAuthAccount(4502, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	patched, err = gatewayhttp.ApplyGrokFreeRequestToolCacheRoute(nil, patched, mixedCacheIntent, account, "isolated-id")

	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.Equal(t, "isolated-id", gjson.GetBytes(patched, "prompt_cache_key").String())
}

func TestBuildGrokCompactRequestBodyUsesResponsesCompactionTurn(t *testing.T) {
	body := []byte(`{"model":"grok-4.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"shell"}],"stream":true}`)

	patched, err := buildGrokCompactRequestBody(body)
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

	converted, err := (xai.BodyCodec{NewID: uuid.NewString}).ConvertGrokResponseToOpenAICompact(body)
	require.NoError(t, err)
	require.Equal(t, "resp_grok_1", gjson.GetBytes(converted, "id").String())
	require.Len(t, gjson.GetBytes(converted, "output").Array(), 1)
	require.Equal(t, "compaction", gjson.GetBytes(converted, "output.0.type").String())
	require.Equal(t, "grok-encrypted-state", gjson.GetBytes(converted, "output.0.encrypted_content").String())
	require.Equal(t, "summary text", gjson.GetBytes(converted, "output.0.summary.0.text").String())
	require.Equal(t, int64(14), gjson.GetBytes(converted, "usage.total_tokens").Int())
}

func TestPatchGrokResponsesBodyRestoresCompactInput(t *testing.T) {
	body := []byte(`{
		"model":"grok-4.5",
		"input":[
			{"id":"cmp_1","type":"compaction","status":"completed","encrypted_content":"grok-encrypted-state","summary":[{"type":"summary_text","text":"summary text"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, "reasoning", gjson.GetBytes(patched, "input.0.type").String())
	require.Equal(t, "grok-encrypted-state", gjson.GetBytes(patched, "input.0.encrypted_content").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.1.type").String())
	require.Contains(t, gjson.GetBytes(patched, "input.1.content.0.text").String(), "summary text")
	require.Equal(t, "continue", gjson.GetBytes(patched, "input.2.content.0.text").String())
}

func TestConvertGrokResponseToOpenAICompactRequiresEncryptedContent(t *testing.T) {
	_, err := (xai.BodyCodec{NewID: uuid.NewString}).ConvertGrokResponseToOpenAICompact([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"summary"}]}]}`))
	require.ErrorContains(t, err, "reasoning.encrypted_content")
}

func TestForwardGrokResponsesCompactRoundTrip(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"compact this"}]}],"metadata":{"large_id":9007199254740993},"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 71031,
		Name:        "grok-compact-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_grok_compact",
			"object":"response",
			"status":"completed",
			"model":"grok-4.5",
			"output":[
				{"type":"reasoning","summary":[],"encrypted_content":"compact-state"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"compact summary"}]}
			],
			"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}
		}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_grok_compact", result.ResponseID)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "grok", result.BillingModel)
	require.Equal(t, xai.DefaultResponsesModel, result.UpstreamModel)
	require.Equal(t, xai.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(upstream.lastBody, "include.0").String())
	require.Equal(t, "compact this", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.1.content.0.text").String(), "Primary Request and Intent")
	// Grok Responses 不支持 OpenAI metadata，兼容层在出站前明确剥离该字段。
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
	require.Equal(t, "compaction", gjson.Get(recorder.Body.String(), "output.0.type").String())
	require.Equal(t, "compact-state", gjson.Get(recorder.Body.String(), "output.0.encrypted_content").String())
	require.Equal(t, "compact summary", gjson.Get(recorder.Body.String(), "output.0.summary.0.text").String())
}

func TestGrokMediaGenerationGateCoversImagesAndVideo(t *testing.T) {
	tests := []struct {
		name     string
		endpoint xai.GrokMediaEndpoint
		want     bool
	}{
		{name: "image generation", endpoint: xai.GrokMediaEndpointImagesGenerations, want: true},
		{name: "image edit", endpoint: xai.GrokMediaEndpointImagesEdits, want: true},
		{name: "video generation", endpoint: xai.GrokMediaEndpointVideosGenerations, want: true},
		{name: "video edit", endpoint: xai.GrokMediaEndpointVideosEdits, want: true},
		{name: "video extension", endpoint: xai.GrokMediaEndpointVideosExtensions, want: true},
		{name: "video status", endpoint: xai.GrokMediaEndpointVideoStatus, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.endpoint.IsGenerationRequest())
		})
	}
}

func TestParseGrokMediaRequestBuildsMultipartModerationBody(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("prompt", "edit this private image"))
	require.NoError(t, writer.WriteField("model", "grok-imagine-edit"))
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="input.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest(writer.FormDataContentType(), buf.Bytes())
	require.Equal(t, "grok-imagine-edit", info.Model)
	require.Equal(t, "edit this private image", info.Prompt)

	moderationBody := info.ModerationBody()
	require.NotEmpty(t, moderationBody)
	require.Equal(t, "edit this private image", gjson.GetBytes(moderationBody, "prompt").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(moderationBody, "images.0.image_url").String(), "data:image/"))
}

func TestParseGrokMediaVideoRequestResolution(t *testing.T) {
	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-video","prompt":"waves","resolution":"720p"}`))

	require.Equal(t, "grok-imagine-video", info.Model)
	require.Equal(t, "720p", info.Resolution)
}

func TestParseGrokMediaRequestAcceptsOfficialImageURLFields(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"image":{"url":"https://example.com/source.png"},
		"reference_images":[{"url":"https://example.com/reference.png"}]
	}`)

	info := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest("application/json", body)

	require.Equal(t, []string{
		"https://example.com/source.png",
		"https://example.com/reference.png",
	}, info.InputImageURLs)
	require.True(t, info.HasInputImage())
}

func TestNormalizeGrokMediaForwardBodyCanonicalizesImageURLAlias(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"image_url":"https://example.com/source.png"},
		"duration":8
	}`)

	out, contentType, err := gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(xai.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

func TestNormalizeGrokMediaForwardBodyPreservesImageToVideoModelForOfficialURL(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"animate",
		"image":{"url":"https://example.com/source.png"}
	}`)

	out, _, err := gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(xai.GrokMediaEndpointVideosGenerations, body, "application/json")

	require.NoError(t, err)
	require.Equal(t, "grok-imagine-video-1.5", gjson.GetBytes(out, "model").String())
	require.Equal(t, "https://example.com/source.png", gjson.GetBytes(out, "image.url").String())
}

func TestCanonicalizeGrokMediaImageURLFieldsPreservesOfficialURL(t *testing.T) {
	body := []byte(`{
		"image":{"url":"https://example.com/official.png","image_url":"https://example.com/legacy.png"},
		"images":[
			{"image_url":"https://example.com/first.png"},
			{"url":"https://example.com/second.png"}
		],
		"reference_images":[{"image_url":"https://example.com/reference.png"}],
		"mask":{"image_url":"https://example.com/mask.png"}
	}`)

	out, err := canonicalizeGrokMediaImageURLFields(body, "image", "images", "reference_images", "mask")

	require.NoError(t, err)
	require.Equal(t, "https://example.com/official.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
	require.Equal(t, "https://example.com/first.png", gjson.GetBytes(out, "images.0.url").String())
	require.False(t, gjson.GetBytes(out, "images.0.image_url").Exists())
	require.Equal(t, "https://example.com/second.png", gjson.GetBytes(out, "images.1.url").String())
	require.Equal(t, "https://example.com/reference.png", gjson.GetBytes(out, "reference_images.0.url").String())
	require.False(t, gjson.GetBytes(out, "reference_images.0.image_url").Exists())
	require.Equal(t, "https://example.com/mask.png", gjson.GetBytes(out, "mask.url").String())
	require.False(t, gjson.GetBytes(out, "mask.image_url").Exists())
}

func TestCanonicalizeGrokMediaImageURLFieldsReplacesEmptyOfficialURL(t *testing.T) {
	body := []byte(`{"image":{"url":" ","image_url":"https://example.com/legacy.png"}}`)

	out, err := canonicalizeGrokMediaImageURLFields(body, "image")

	require.NoError(t, err)
	require.Equal(t, "https://example.com/legacy.png", gjson.GetBytes(out, "image.url").String())
	require.False(t, gjson.GetBytes(out, "image.image_url").Exists())
}

func TestPrepareGrokImageEditNormalizesOfficialImageObjects(t *testing.T) {
	body := []byte(`{
		"model":"grok-imagine-image-quality",
		"image":{"image_url":{"url":"https://example.com/first.png"}},
		"images":["https://example.com/second.png"],
		"mask":{"image_url":"https://example.com/mask.png"}
	}`)

	out, contentType, err := gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(xai.GrokMediaEndpointImagesEdits, body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	for _, path := range []string{"image", "images.0", "mask"} {
		require.Equal(t, "image_url", gjson.GetBytes(out, path+".type").String())
		require.NotEmpty(t, gjson.GetBytes(out, path+".url").String())
		require.False(t, gjson.GetBytes(out, path+".image_url").Exists())
	}
}

func TestPrepareGrokImageEditRejectsMoreThanThreeSources(t *testing.T) {
	body := []byte(`{"images":["https://example.com/1.png","https://example.com/2.png","https://example.com/3.png","https://example.com/4.png"]}`)

	out, _, err := gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(xai.GrokMediaEndpointImagesEdits, body, "application/json")
	require.Error(t, err)
	require.Nil(t, out)
	require.Contains(t, err.Error(), "maximum of 3 source images")
}

func TestNormalizeGrokMediaModelForEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      xai.GrokMediaEndpoint
		model         string
		hasInputImage bool
		want          string
	}{
		{name: "image generation alias", endpoint: xai.GrokMediaEndpointImagesGenerations, model: "grok-imagine", want: "grok-imagine-image-quality"},
		{name: "image edit alias", endpoint: xai.GrokMediaEndpointImagesEdits, model: "grok-imagine", want: "grok-imagine-image-quality"},
		{name: "image quality passthrough", endpoint: xai.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image-quality", want: "grok-imagine-image-quality"},
		{name: "image fast passthrough", endpoint: xai.GrokMediaEndpointImagesGenerations, model: "grok-imagine-image", want: "grok-imagine-image"},
		{name: "video passthrough", endpoint: xai.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video", want: "grok-imagine-video"},
		{name: "video 1.5 text-only remains explicit", endpoint: xai.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", want: "grok-imagine-video-1.5"},
		{name: "video 1.5 image-to-video passthrough", endpoint: xai.GrokMediaEndpointVideosGenerations, model: "grok-imagine-video-1.5", hasInputImage: true, want: "grok-imagine-video-1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gatewayprovider.GrokMediaCodec().NormalizeGrokMediaModelForEndpoint(tt.endpoint, tt.model, tt.hasInputImage))
		})
	}
}

func TestForwardGrokMediaImagesGenerationNormalizesImagineAlias(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 61,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-image-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/cat.png"}]}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	require.Equal(t, "Bearer api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, xai.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.JSONEq(t, `{"model":"grok-imagine-image-quality","prompt":"draw a cat"}`, string(upstream.lastBody))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"data":[{"url":"https://images.test/cat.png"}]}`, recorder.Body.String())
	require.Equal(t, "xai-image-req", result.RequestID)
	require.Equal(t, "grok-imagine-image-quality", result.Model)
	require.Equal(t, "grok-imagine-image-quality", result.BillingModel)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, pricing.ImageBillingSize2K, result.ImageSize)
}

func TestForwardGrokMediaImagesGenerationRejectsEmptySuccessfulResponse(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-image","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.JSONEq(t, `{"data":[]}`, string(failoverErr.ResponseBody))
	require.Empty(t, recorder.Body.String())
}

func TestForwardGrokMediaAppliesAccountModelMappingAfterEndpointNormalization(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	tests := []struct {
		name             string
		endpoint         xai.GrokMediaEndpoint
		path             string
		body             string
		modelMapping     map[string]any
		wantRequestModel string
		wantBillingModel string
		wantUpstream     string
		wantBody         string
		responseBody     string
	}{
		{
			name:             "image generation maps normalized image alias",
			endpoint:         xai.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw a cat"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "vendor-image-model"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "vendor-image-model",
			wantUpstream:     "vendor-image-model",
			wantBody:         `{"model":"vendor-image-model","prompt":"draw a cat"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "video generation maps explicit text-only model",
			endpoint:         xai.GrokMediaEndpointVideosGenerations,
			path:             "/v1/videos/generations",
			body:             `{"model":"grok-imagine-video-1.5","prompt":"waves"}`,
			modelMapping:     map[string]any{"grok-imagine-video-1.5": "grok-image-video"},
			wantRequestModel: "grok-imagine-video-1.5",
			wantBillingModel: "grok-image-video",
			wantUpstream:     "grok-image-video",
			wantBody:         `{"model":"grok-image-video","prompt":"waves"}`,
			responseBody:     `{"request_id":"video-request-mapped"}`,
		},
		{
			name:             "image-to-video preserves then maps the requested model",
			endpoint:         xai.GrokMediaEndpointVideosGenerations,
			path:             "/v1/videos/generations",
			body:             `{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"url":"https://example.com/input.png"}}`,
			modelMapping:     map[string]any{"grok-imagine-video-1.5": "vendor-image-video"},
			wantRequestModel: "grok-imagine-video-1.5",
			wantBillingModel: "vendor-image-video",
			wantUpstream:     "vendor-image-video",
			wantBody:         `{"model":"vendor-image-video","prompt":"animate","image":{"url":"https://example.com/input.png"}}`,
			responseBody:     `{"request_id":"image-video-request-mapped"}`,
		},
		{
			name:             "mapping and image sanitization compose",
			endpoint:         xai.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw","size":"1024x1024"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "vendor-image-model"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "vendor-image-model",
			wantUpstream:     "vendor-image-model",
			wantBody:         `{"model":"vendor-image-model","prompt":"draw","resolution":"1k","aspect_ratio":"1:1"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "whitespace mapping target safely preserves normalized model",
			endpoint:         xai.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "   "},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "grok-imagine-image-quality",
			wantUpstream:     "grok-imagine-image-quality",
			wantBody:         `{"model":"grok-imagine-image-quality","prompt":"draw"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "account mapping target continues through builtin normalization",
			endpoint:         xai.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "grok-build"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "grok-build",
			wantUpstream:     "grok-build-0.1",
			wantBody:         `{"model":"grok-build-0.1","prompt":"draw"}`,
			responseBody:     `{"data":[{"url":"https://images.test/normalized.png"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
				Name:        "grok-mapped",
				Platform:    capability.PlatformGrok,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":       "api-key",
					"base_url":      "https://xai.test/v1",
					"model_mapping": tt.modelMapping,
				}},
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

			result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, tt.endpoint, "", []byte(tt.body), "application/json")

			require.NoError(t, err)
			require.JSONEq(t, tt.wantBody, string(upstream.lastBody))
			require.Equal(t, tt.wantRequestModel, result.Model)
			require.Equal(t, tt.wantBillingModel, result.BillingModel)
			require.Equal(t, tt.wantUpstream, result.UpstreamModel)
		})
	}
}

func TestForwardGrokMediaImagesGenerationStripsUnsupportedSize(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-image","prompt":"draw a cat","size":"1024x1024"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 65,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "api-key",
			"base_url":      "https://xai.test/v1",
			"model_mapping": map[string]any{"grok-imagine-edit": "vendor-image-edit"},
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/cat.png"}]}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"grok-imagine-image","prompt":"draw a cat","resolution":"1k","aspect_ratio":"1:1"}`, string(upstream.lastBody))
	require.False(t, gjson.GetBytes(upstream.lastBody, "size").Exists())
	require.Equal(t, pricing.ImageBillingSize1K, result.ImageSize)
	require.Equal(t, "1024x1024", result.ImageInputSize)
}

func TestForwardGrokMediaImagesEditMultipartConvertsToJSON(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("model", "grok-imagine-edit"))
	require.NoError(t, writer.WriteField("prompt", "edit this private image"))
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="input.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(buf.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 62,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "api-key",
			"base_url":      "https://xai.test/v1",
			"model_mapping": map[string]any{"grok-imagine-edit": "vendor-image-edit"},
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/edited.png"}]}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointImagesEdits, "", buf.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/images/edits", upstream.lastReq.URL.String())
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.True(t, json.Valid(upstream.lastBody))
	require.Equal(t, "vendor-image-edit", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "edit this private image", gjson.GetBytes(upstream.lastBody, "prompt").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "image.url").String(), "data:image/png;base64,"))
	require.False(t, gjson.GetBytes(upstream.lastBody, "image.image_url").Exists())
	require.Equal(t, "vendor-image-edit", result.BillingModel)
	require.Equal(t, "vendor-image-edit", result.UpstreamModel)
}

func TestForwardGrokMediaVideoGenerationReturnsUsageAndResponseID(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"waves","resolution":"720p","duration":10}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-video-generate-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-123","usage":{"prompt_tokens":3,"completion_tokens":4}}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/generations", upstream.lastReq.URL.String())
	require.JSONEq(t, `{"model":"grok-imagine-video-1.5","prompt":"waves","resolution":"720p","duration":10}`, string(upstream.lastBody))
	require.Equal(t, "video-request-123", result.ResponseID)
	require.Equal(t, "grok-imagine-video-1.5", result.BillingModel)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	// 创建阶段只受理任务，在状态返回 video.url 前 VideoCount 保持为零。
	require.Equal(t, 0, result.ImageCount)
	require.Empty(t, result.ImageSize)
	require.Equal(t, 0, result.VideoCount)
	require.Equal(t, pricing.VideoBillingResolution720P, result.VideoResolution)
	require.Equal(t, 10, result.VideoDurationSeconds)
}

func TestForwardGrokMediaVideoGenerationReturnsTaskIDAsResponseID(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video","prompt":"waves"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"video-task-123"}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "video-task-123", result.ResponseID)
}

func TestExtractGrokMediaVideoRequestIDPreservesExistingPrecedence(t *testing.T) {
	body := []byte(`{
		"request_id":"request-id",
		"id":"id",
		"task_id":"task-id",
		"data":{"request_id":"data-request-id","id":"data-id","task_id":"data-task-id"},
		"video":{"request_id":"video-request-id","id":"video-id","task_id":"video-task-id"}
	}`)

	require.Equal(t, "request-id", gatewayprovider.GrokMediaCodec().ExtractGrokMediaVideoRequestID(body))
}

func TestForwardGrokMediaVideoGenerationPreservesImageToVideoModel(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"image_url":"data:image/png;base64,aW1n"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-456"}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/generations", upstream.lastReq.URL.String())
	require.JSONEq(t, `{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"url":"data:image/png;base64,aW1n"}}`, string(upstream.lastBody))
	require.Equal(t, "video-request-456", result.ResponseID)
	require.Equal(t, "grok-imagine-video-1.5", result.BillingModel)
	// 未指定 duration 时按上游默认 8 秒计费。
	require.Equal(t, pricing.VideoBillingDefaultDurationSeconds, result.VideoDurationSeconds)
}

func TestForwardGrokMediaOAuthImageToVideoUsesOfficialAPIForLargeBody(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	imageData := strings.Repeat("A", 2*1024*1024)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"image_url":"data:image/png;base64,` + imageData + `"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
		Name:        "grok-oauth",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "oauth-access-token",
			"refresh_token": "oauth-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-oauth"}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream}), newGrokTokenSourceForTest(nil, nil))

	_, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/videos/generations", upstream.lastReq.URL.String())
	require.Empty(t, upstream.lastReq.Header.Get("X-XAI-Token-Auth"))
	require.Empty(t, upstream.lastReq.Header.Get("x-grok-client-version"))
	require.Equal(t, "data:image/png;base64,"+imageData, gjson.GetBytes(upstream.lastBody, "image.url").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "image.image_url").Exists())
}

func TestForwardGrokMediaVideoStatusUsesGETWithoutBody(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/request-123", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 62,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-video-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"request-123","status":"completed"}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointVideoStatus, "request-123", nil, "")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/request-123", upstream.lastReq.URL.String())
	require.Equal(t, http.MethodGet, upstream.lastReq.Method)
	require.Equal(t, "Bearer api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, xai.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Empty(t, upstream.lastReq.Header.Get("Content-Type"))
	require.Empty(t, upstream.lastBody)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"id":"request-123","status":"completed"}`, recorder.Body.String())
	require.Equal(t, "xai-video-req", result.RequestID)
}

func TestForwardGrokMediaVideoMutationEndpoints(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	tests := []struct {
		name     string
		endpoint xai.GrokMediaEndpoint
		path     string
	}{
		{name: "edit", endpoint: xai.GrokMediaEndpointVideosEdits, path: "/videos/edits"},
		{name: "extension", endpoint: xai.GrokMediaEndpointVideosExtensions, path: "/videos/extensions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(`{"model":"grok-imagine-video","prompt":"continue","video":{"url":"https://example.com/in.mp4"},"duration":6}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1"+tt.path, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 71, Name: "grok", Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{
					"api_key":       "api-key",
					"base_url":      "https://xai.test/v1",
					"model_mapping": map[string]any{"grok-imagine-video": "vendor-video-mutation"},
				}},
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"request_id":"video-mutation-123"}`)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

			result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, tt.endpoint, "", body, "application/json")
			require.NoError(t, err)
			require.Equal(t, "https://xai.test/v1"+tt.path, upstream.lastReq.URL.String())
			require.Equal(t, http.MethodPost, upstream.lastReq.Method)
			require.JSONEq(t, `{"model":"vendor-video-mutation","prompt":"continue","video":{"url":"https://example.com/in.mp4"},"duration":6}`, string(upstream.lastBody))
			require.Equal(t, "video-mutation-123", result.ResponseID)
			require.Equal(t, 0, result.VideoCount)
			require.Equal(t, 6, result.VideoDurationSeconds)
			require.Equal(t, "vendor-video-mutation", result.BillingModel)
			require.Equal(t, "vendor-video-mutation", result.UpstreamModel)
		})
	}
}

func TestGrokMediaVideoRequestBindingIsScopedToUserAndAPIKey(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/video-request-123", nil)
	c.Request.Header.Set("session_id", "shared-client-session")
	groupID := int64(7)
	cache := &sessiontestkit.StickyCache{}
	tasks := media.NewVideoTasks(cache, nil, media.VideoOptions{})
	const userID int64 = 41
	const apiKeyID int64 = 51
	require.NotEmpty(t, gatewayhttp.GenerateExplicitOpenAISessionHash(c, nil))
	ctx := c.Request.Context()

	hash := media.GrokMediaVideoRequestSessionHash("video-request-123", userID, apiKeyID)
	require.NotEmpty(t, hash)
	require.NoError(t, tasks.BindGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID, 63))

	accountID, err := tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID)
	require.NoError(t, err)
	require.Equal(t, int64(63), accountID)

	accountID, err = tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID+1, apiKeyID)
	require.Error(t, err)
	require.Zero(t, accountID)

	accountID, err = tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID+1)
	require.Error(t, err)
	require.Zero(t, accountID)
}

func TestForwardGrokMedia429ReconcilesRateLimitBeforeCustomErrorBypass(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 64,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                    "api-key",
			"base_url":                   "https://xai.test/v1",
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusBadRequest)},
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-error-req"},
			"Retry-After":    []string{"45"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"do not expose this upstream detail"}}`)),
	}}
	repo := &grokQuotaAccountRepo{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream, accountRepo: repo}))

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, xai.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream gateway error")
	require.NotContains(t, recorder.Body.String(), "do not expose")
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestGrokMedia429FailoverPreservesRetryAfter(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 641, Name: "grok-oauth", Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusTooManyRequests)},
		}},
	}
	repo := &grokQuotaAccountRepo{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}

	result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, "45", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
}

func TestForwardAsChatCompletionsForGrokStopFallsBackToXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false,"stop":"done","prompt_cache_key":"raw-client-cache-key"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5101})

	account := gatewaytestkit.HealthyGrokOAuthAccount(51, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{51: account},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"application/json"},
			"Xai-Request-Id":                 []string{"xai-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"9"},
			"X-Ratelimit-Limit-Tokens":       []string{"1000"},
			"X-Ratelimit-Remaining-Tokens":   []string{"990"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl","object":"chat.completion","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.NotEmpty(t, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.NotEqual(t, "raw-client-cache-key", upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
	require.Equal(t, "grok", result.Model)
	require.Equal(t, "grok-4.5", result.UpstreamModel)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.NotNil(t, repo.updates[51][grokQuotaSnapshotExtraKey])
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestForwardGrokResponsesStreamingDefaultsEmptyModelTo45AndSnapshots(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"input":"hi","stream":true,"reasoning_effort":"high"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Set("api_key", &apikey.APIKey{ID: 5201})

	account := gatewaytestkit.HealthyGrokOAuthAccount(52, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{52: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_grok","model":"grok-4.3","usage":{"input_tokens":5,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"Xai-Request-Id":                 []string{"xai-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"8"},
			"X-Ratelimit-Limit-Tokens":       []string{"1000"},
			"X-Ratelimit-Remaining-Tokens":   []string{"990"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "", true, time.Now())
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.True(t, result.Stream)
	require.Equal(t, "resp_grok", result.ResponseID)
	require.Equal(t, "xai-stream-req", result.RequestID)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), "response.output_text.delta")
	require.NotNil(t, repo.updates[52][grokQuotaSnapshotExtraKey])
}

func TestForwardGrokResponsesAPIKeyUsesXAIResponses(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":"hi","metadata":{"session_id":"abc"},"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 53,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_grok_api_key","model":"grok-4.5","usage":{"input_tokens":2,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", true, time.Now())
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer xai-test-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, xai.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
	require.Equal(t, "resp_grok_api_key", result.ResponseID)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
}

func TestForwardGrokResponsesUsesMetadataSessionForCacheIdentityWithoutForwardingMetadata(t *testing.T) {

	firstBody := []byte(`{"model":"grok","input":"first turn","metadata":{"user_id":"{\"session_id\":\"metadata-session\"}"},"stream":false}`)
	secondBody := []byte(`{"model":"grok","input":"different second turn","metadata":{"user_id":"{\"session_id\":\"metadata-session\"}"},"stream":false}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5401,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_first","object":"response","model":"grok-4.6","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_second","object":"response","model":"grok-4.6","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`)),
		},
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})
	newContext := func(body []byte) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Set("api_key", &apikey.APIKey{ID: 5401})
		return c
	}

	_, err := svc.Grok.ForwardResponses(context.Background(), newContext(firstBody), account, firstBody, "grok", false, time.Now())
	require.NoError(t, err)
	_, err = svc.Grok.ForwardResponses(context.Background(), newContext(secondBody), account, secondBody, "grok", false, time.Now())
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)

	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "metadata").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "metadata").Exists())
}

func TestForwardGrokResponsesRetriesInvalidEncryptedContentOnce(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok",
		"previous_response_id":"resp_valid_history",
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"keep this summary"}],"encrypted_content":"encrypted-reasoning"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		],
		"metadata":{"large_id":9007199254740993},
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 4535})

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4535,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "same-token",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{"recoverable-first"},
			},
			Body: io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content. Ensure the value is unmodified."}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{"recovered-second"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"resp_recovered","object":"response","model":"grok-4.5","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_recovered", result.ResponseID)
	require.Equal(t, "recovered-second", result.RequestID)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)

	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
	require.Equal(t, "resp_valid_history", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
	require.Equal(t, "encrypted-reasoning", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.Equal(t, "resp_valid_history", gjson.GetBytes(upstream.bodies[1], "previous_response_id").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
	require.Equal(t, "keep this summary", gjson.GetBytes(upstream.bodies[1], "input.0.summary.0.text").String())
	require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.1.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "metadata").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "metadata").Exists())

	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	for _, req := range upstream.requests {
		require.Equal(t, "Bearer same-token", req.Header.Get("Authorization"))
		require.Equal(t, firstIdentity, req.Header.Get(gatewayhttp.GrokConversationIDHeader))
	}
	require.Equal(t, billing.StatusActive, account.Record.Status)
	_, hasUpstreamErrors := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.False(t, hasUpstreamErrors)
	_, hasTerminalStatus := c.Get(gatewayhttp.OpsUpstreamStatusCodeKey)
	require.False(t, hasTerminalStatus)
}

func TestForwardGrokResponsesInvalidEncryptedContentRecoveryDoesNotOvermatch(t *testing.T) {

	matchingError := `{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}`
	tests := []struct {
		name         string
		requestBody  string
		responseBody string
	}{
		{
			name:         "different top-level code",
			requestBody:  `{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"}],"stream":false}`,
			responseBody: `{"code":"bad-request","error":"Could not decrypt the provided encrypted_content."}`,
		},
		{
			name:         "message does not mention decryption",
			requestBody:  `{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"}],"stream":false}`,
			responseBody: `{"code":"invalid-argument","error":"The provided encrypted_content is invalid."}`,
		},
		{
			name:         "request has no encrypted reasoning",
			requestBody:  `{"model":"grok","input":[{"type":"message","role":"user","content":"hi"}],"stream":false}`,
			responseBody: matchingError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(tt.requestBody)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4536,
				Name:        "grok-api-key",
				Platform:    capability.PlatformGrok,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "token", "base_url": "https://api.x.ai/v1"}},
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
			}}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

			result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
			require.Nil(t, result)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestGrokCompactionBlobRecoveryStripsCompactionItem(t *testing.T) {
	body := []byte(`{"model":"grok","input":[{"type":"compaction","id":"cmp_1","encrypted_content":"blob"},{"type":"message","role":"user","content":"hi"}]}`)
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(http.StatusUnprocessableEntity, []byte(`{"code":"invalid_compaction","error":"could not decode the compaction blob"}`)))
	retry, changed, err := gatewayprovider.GrokBodyCodec().TrimGrokInvalidEncryptedContentRetryBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(retry, "input.0.type").String())
	require.False(t, gjson.GetBytes(retry, "input.#(type==\"compaction\")").Exists())
}

func TestForwardGrokResponsesInvalidEncryptedContentRecoveryNestedErrorShape(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"message","role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4538,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "token", "base_url": "https://api.x.ai/v1"}},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":{"message":"Could not decrypt the provided encrypted_content."}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
}

func TestForwardGrokResponsesInvalidEncryptedContentRetryFailureIsTerminal(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"message","role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4537,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "same-token", "base_url": "https://api.x.ai/v1"}},
	}
	newInvalidEncryptedResponse := func(requestID string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{requestID},
			},
			Body: io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}`)),
		}
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newInvalidEncryptedResponse("recoverable-first"),
		newInvalidEncryptedResponse("terminal-second"),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{httpUpstream: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.Nil(t, result)
	require.Error(t, err)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], `input.#(type=="reasoning")`).Exists())

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, events)
	for _, event := range events {
		require.NotEqual(t, "recoverable-first", event.UpstreamRequestID)
	}
	require.Equal(t, http.StatusBadRequest, c.GetInt(gatewayhttp.OpsUpstreamStatusCodeKey))
}

func TestForwardAsChatCompletionsForGrokAPIKeyUsesConfiguredRawEndpointWithoutOAuthIdentity(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 706,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "https://grok.example.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl","object":"chat.completion","model":"grok-4.5","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
	}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream})

	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, "https://grok.example.test/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer third-party-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, xai.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
}

func TestForwardAsChatCompletionsForGrokAPIKeyRejectsNonStreamingResponseWithoutUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 707,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "https://grok.example.test/v1",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Xai-Request-Id": []string{"rid-grok-raw-missing-usage"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_missing_usage","object":"chat.completion","model":"grok-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		)),
	}}
	service := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream})

	result, err := service.Text.Chat(context.Background(), c, account, body, "", "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, "grok_missing_usage", gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.Equal(t, "rid-grok-raw-missing-usage", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestForwardAsChatCompletionsForGrokStreamingUsesRawXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := gatewaytestkit.HealthyGrokOAuthAccount(53, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{53: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":1}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"chat-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"7"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.True(t, result.Stream)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
	require.NotNil(t, repo.updates[53][grokQuotaSnapshotExtraKey])
}

func TestForwardGrokResponsesNonStreamingUsesCacheIdentityAndCachedUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":"hi","stream":false,"tools":[{"type":"namespace","name":"client_tools"}],"tool_choice":{"type":"namespace","name":"client_tools"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &apikey.APIKey{ID: 5202})

	account := gatewaytestkit.HealthyGrokOAuthAccount(56, "access-token")
	observedResetAt := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	observedLimitedAt := observedResetAt.Add(-grokRateLimitRepeatCooldown)
	account.Record.RateLimitedAt = &observedLimitedAt
	account.Record.RateLimitResetAt = &observedResetAt
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{56: account},
		},
		recoveryClearResult: true,
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-non-stream-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_grok_non_stream","object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9,"input_tokens_details":{"cached_tokens":4}}}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_grok_non_stream", result.ResponseID)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	// 清理器会移除不受支持的客户端工具，但明确的工具意图仍必须阻止注入原生缓存路由工具。
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.Equal(t, "resp_grok_non_stream", gjson.Get(recorder.Body.String(), "id").String())
	require.Equal(t, 1, repo.recoveryClearCalls)
	require.Equal(t, observedLimitedAt, repo.recoveryObservedAt)
	require.Equal(t, observedResetAt, repo.recoveryObservedReset)
}

// 原生 Responses 的 Free OAuth 函数工具请求必须在实际转发前补齐可缓存原生工具。
func TestForwardGrokResponsesFreeFunctionToolsUseCacheCapableMixedRoute(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","input":"look up alpha","stream":false,
		"tools":[
			{"type":"function","name":"lookup","parameters":{"type":"object"}},
			{"type":"function","name":"web_search","parameters":{"type":"object"}}
		],
		"tool_choice":"auto"
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5204})

	account := gatewaytestkit.HealthyGrokOAuthAccount(60, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{60: account},
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_grok_tools","object":"response","model":"grok-4.5","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1}}`,
		)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
}

func TestForwardGrokResponsesFailoverKeepsCacheIdentityAcrossAccounts(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"role":"user","content":"stable prefix"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5203})

	newAccount := func(id int64, token string) *gatewayprovider.ExecutionAccount {
		account := gatewaytestkit.HealthyGrokOAuthAccount(id, token)
		account.Record.Name = fmt.Sprintf("grok-%d", id)
		return account
	}
	firstAccount := newAccount(58, "access-token-a")
	secondAccount := newAccount(59, "access-token-b")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{58: firstAccount, 59: secondAccount},
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_after_failover","object":"response","model":"grok-4.3","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1}}`)),
		},
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	_, err := svc.Grok.ForwardResponses(context.Background(), c, firstAccount, body, "grok", false, time.Now())
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, secondAccount, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)
	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.Equal(t, firstIdentity, upstream.requests[0].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, secondIdentity, upstream.requests[1].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "Bearer access-token-a", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer access-token-b", upstream.requests[1].Header.Get("Authorization"))
}

func TestForwardAsChatCompletionsForGrokStreamingStopFallsBackToRawXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true,"stop":"done"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set(gatewayhttp.GrokConversationIDHeader, "native-client-conversation")
	c.Set("api_key", &apikey.APIKey{ID: 5301})

	account := gatewaytestkit.HealthyGrokOAuthAccount(53, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{53: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":1}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"chat-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"7"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEmpty(t, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.NotEqual(t, "native-client-conversation", upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.True(t, result.Stream)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
	require.NotNil(t, repo.updates[53][grokQuotaSnapshotExtraKey])
}

func TestForwardAsChatCompletionsForGrokComposerBridgesImageInput(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-composer-2.5-fast","messages":[{"role":"system","content":"You are concise."},{"role":"user","content":[{"type":"text","text":"What is shown?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]}],"metadata":{"large_id":9007199254740993},"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &apikey.APIKey{ID: 5501})

	account := gatewaytestkit.HealthyGrokOAuthAccount(55, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{55: account},
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "xai-request-id": []string{"vision-req"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_vision","object":"response","model":"grok-build-0.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A small diagram with ABC letters."}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":                   []string{"application/json"},
				"X-Request-Id":                   []string{"composer-req"},
				"X-Ratelimit-Limit-Requests":     []string{"10"},
				"X-Ratelimit-Remaining-Requests": []string{"9"},
				"X-Ratelimit-Limit-Tokens":       []string{"1000"},
				"X-Ratelimit-Remaining-Tokens":   []string{"980"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_composer","object":"chat.completion","model":"grok-composer-2.5-fast","choices":[{"index":0,"message":{"role":"assistant","content":"It shows ABC."},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)),
		},
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.requests[0].URL.String())
	require.Empty(t, upstream.requests[0].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "grok-build-0.1", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "input_image", gjson.GetBytes(upstream.bodies[0], "input.0.content.1.type").String())
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.requests[1].URL.String())
	require.NotEmpty(t, upstream.requests[1].Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "grok-composer-2.5-fast", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.False(t, strings.Contains(string(upstream.bodies[1]), "image_url"))
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[1], "metadata.large_id").Raw)
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "messages.1.content").String(), "Image 1 description")
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "messages.1.content").String(), "A small diagram with ABC letters.")
	require.Equal(t, 14, result.Usage.InputTokens)
	require.Equal(t, 12, result.Usage.OutputTokens)
	require.Equal(t, "It shows ABC.", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	require.NotNil(t, repo.updates[55][grokQuotaSnapshotExtraKey])
}

// Codex 身份恢复只能作用于 OpenAI OAuth，Grok Messages 必须保持自己的请求头和端点。
func TestForwardAsAnthropicForGrokUsesXAIResponses(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","max_tokens":32,"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("OpenAI-Beta", "grok-experimental")
	c.Request.Header.Set("originator", "opencode")
	c.Set("api_key", &apikey.APIKey{ID: 5401})

	account := gatewaytestkit.HealthyGrokOAuthAccount(54, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{54: account},
		},
	}
	upstream := &httpUpstreamRecorder{resp: grokMessagesSSECompletedResponse("resp_grok_messages", 3)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Empty(t, upstream.lastReq.Header.Get("originator"))
	require.Empty(t, upstream.lastReq.Header.Get("version"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.Empty(t, upstream.lastReq.Header.Get("session_id"))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.NotContains(t, string(upstream.lastBody), "chatgpt.com")
	require.Equal(t, "grok", result.Model)
	require.Equal(t, "grok", result.BillingModel)
	require.Equal(t, "grok-4.5", result.UpstreamModel)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), `"type":"message"`)
	require.Equal(t, int64(3), gjson.Get(recorder.Body.String(), "usage.cache_read_input_tokens").Int())
	require.Contains(t, recorder.Body.String(), "ok")
}

// Grok Messages 在账号缓存身份变化后应剥离旧推理密文，并通过同一路由重试一次。
func TestForwardAsAnthropicForGrokRetriesInvalidEncryptedContentOnce(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","max_tokens":32,"stream":false,
		"messages":[
			{"role":"user","content":"plan a command"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"enc-old-account"},{"type":"text","text":"run it"}]},
			{"role":"user","content":"continue"}
		]
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5404})

	account := gatewaytestkit.HealthyGrokOAuthAccount(59, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{59: account},
		},
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Could not decrypt the provided encrypted_content."}}`)),
		},
		grokMessagesSSECompletedResponse("resp_grok_messages_retry", 1),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.True(t, gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(upstream.bodies[0]))
	require.False(t, gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(upstream.bodies[1]))
	require.Equal(t, upstream.requests[0].URL.String(), upstream.requests[1].URL.String())
	require.Contains(t, recorder.Body.String(), "ok")
}

func TestForwardAsAnthropicForGrokFunctionToolUsesCacheCapableMixedRoute(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","max_tokens":32,"stream":false,
		"messages":[{"role":"user","content":"look up alpha"}],
		"tools":[{"name":"lookup","description":"look up a key","input_schema":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}},{"name":"web_search","description":"search the web","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],
		"tool_choice":{"type":"auto"}
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5403})

	account := gatewaytestkit.HealthyGrokOAuthAccount(58, "access-token")
	account.Record.Extra = map[string]any{accountcore.GrokUsageBillingExtraKey: map[string]any{
		"status_code":        http.StatusOK,
		"source":             "billing_probe",
		"monthly_updated_at": "2026-07-15T05:00:00Z",
	}}
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{58: account},
		},
	}
	responseBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_grok_function","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"function_call","id":"fc_lookup","call_id":"call_lookup","name":"lookup","arguments":"{\"key\":\"alpha\"}","status":"completed"}],"usage":{"input_tokens":7000,"output_tokens":2,"total_tokens":7002,"input_tokens_details":{"cached_tokens":6144}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "object", tools[0].Get("parameters.type").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())

	require.Equal(t, 7000, result.Usage.InputTokens)
	require.Equal(t, 6144, result.Usage.CacheReadInputTokens)
	clientBody := recorder.Body.String()
	require.Equal(t, "tool_use", gjson.Get(clientBody, "content.0.type").String())
	require.Equal(t, "call_lookup", gjson.Get(clientBody, "content.0.id").String())
	require.Equal(t, "lookup", gjson.Get(clientBody, "content.0.name").String())
	require.Equal(t, "alpha", gjson.Get(clientBody, "content.0.input.key").String())
	require.Equal(t, "tool_use", gjson.Get(clientBody, "stop_reason").String())
	require.Equal(t, int64(856), gjson.Get(clientBody, "usage.input_tokens").Int())
	require.Equal(t, int64(6144), gjson.Get(clientBody, "usage.cache_read_input_tokens").Int())
}

func TestForwardAsAnthropicForGrokStreamingPreservesCacheUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5402})

	account := gatewaytestkit.HealthyGrokOAuthAccount(57, "access-token")
	repo := &grokQuotaAccountRepo{
		mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{57: account},
		},
	}
	upstream := &httpUpstreamRecorder{resp: grokMessagesSSECompletedResponse("resp_grok_messages_stream", 2)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), `"cache_read_input_tokens":2`)
}

func grokMessagesSSECompletedResponse(responseID string, cachedTokens int) *http.Response {
	body := strings.Join([]string{
		fmt.Sprintf(`data: {"type":"response.completed","response":{"id":%q,"object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":%d}}}}`, responseID, cachedTokens),
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// grokPoolPolicyAccountRepo 记录 Grok 池模式错误策略产生的账号状态写入。
type grokPoolPolicyAccountRepo struct {
	*grokQuotaAccountRepo
	setErrorCalls            int
	overloadedCalls          int
	modelRateLimitCalls      int
	lastModelRateLimitScope  string
	lastModelRateLimitReason string
}

func (r *grokPoolPolicyAccountRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

func (r *grokPoolPolicyAccountRepo) SetOverloaded(_ context.Context, _ int64, _ time.Time) error {
	r.overloadedCalls++
	return nil
}

func (r *grokPoolPolicyAccountRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, reason ...string) error {
	r.modelRateLimitCalls++
	r.lastModelRateLimitScope = scope
	if len(reason) > 0 {
		r.lastModelRateLimitReason = reason[0]
	}
	return nil
}

// newGrokPoolPolicyGateway 构造接入真实通用错误策略的 Grok 网关测试实例。
func newGrokPoolPolicyGateway(account *gatewayprovider.ExecutionAccount) (*OpenAIGatewayService, *grokPoolPolicyAccountRepo) {
	baseRepo := &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}
	repo := &grokPoolPolicyAccountRepo{
		grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: baseRepo},
	}
	cfg := &config.Config{}
	var svc *OpenAIGatewayService

	healthObserver := newUpstreamHealthForTest(repo, cfg, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	svc = withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: healthObserver,
		cfg:            cfg,
	}))
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return svc.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(v), h, body)
	}

	return svc, repo
}

// newGrokPoolAccount 返回开启池模式的 Grok API Key 账号。
func newGrokPoolAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}},
	}
}

func TestGrokMediaPoolModeRetryFlagFollowsExplicitPolicies(t *testing.T) {

	t.Run("default 429 remains retryable without local cooldown", func(t *testing.T) {
		account := newGrokPoolAccount(630)
		svc, repo := newGrokPoolPolicyGateway(account)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"60"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		}

		result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.True(t, failoverErr.RetryableOnSameAccount)
		require.Zero(t, repo.rateLimitedCalls)
		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	})

	t.Run("explicit 401 policy stops same account retry", func(t *testing.T) {
		account := newGrokPoolAccount(631)
		account.Record.Credentials["custom_error_codes_enabled"] = true
		account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnauthorized)}
		svc, repo := newGrokPoolPolicyGateway(account)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		resp := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid api key"}}`)),
		}

		result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.False(t, failoverErr.RetryableOnSameAccount)
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	})

	t.Run("mapped model is used by temporary policy", func(t *testing.T) {
		t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
		account := newGrokPoolAccount(632)
		account.Record.Credentials["api_key"] = "api-key"
		account.Record.Credentials["base_url"] = "https://xai.test/v1"
		account.Record.Credentials["model_mapping"] = map[string]any{"image-alias": "vendor-image-model"}
		account.Record.Credentials["temp_unschedulable_enabled"] = true
		account.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolPolicyGateway(account)
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"maintenance in progress"}}`)),
		}}
		svc.httpUpstream = upstream
		if svc.Requests != nil {
			svc.Requests.Transport = svc.httpUpstream
		}
		if svc.Grok != nil {
			svc.Grok.Transport = svc.httpUpstream
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"image-alias","prompt":"draw a cat"}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")

		result, err := svc.Grok.ForwardGrokMedia(
			context.Background(),
			c,
			account,
			xai.GrokMediaEndpointImagesGenerations,
			"",
			body,
			"application/json",
		)

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, 1, repo.modelRateLimitCalls)
		require.Equal(t, "vendor-image-model", repo.lastModelRateLimitScope)
		require.Equal(t, "vendor-image-model", gjson.GetBytes(upstream.lastBody, "model").String())
	})
}

func TestPatchGrokResponsesBody_StripsReasoningContentNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking..."}],"content":null,"encrypted_content":null},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello!"}]}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, json.Valid(patched))

	input := gjson.GetBytes(patched, "input")
	require.True(t, input.IsArray())

	items := input.Array()
	require.Len(t, items, 3)

	reasoning := items[1]
	require.Equal(t, "reasoning", reasoning.Get("type").String())
	require.True(t, reasoning.Get("summary").Exists(), "summary should be preserved")
	require.False(t, reasoning.Get("content").Exists(), "content: null should be stripped")
}

func TestPatchGrokResponsesBody_KeepsReasoningContentNonNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"reasoning","summary":[{"type":"summary_text","text":"ok"}],"content":"real content"}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	reasoning := gjson.GetBytes(patched, "input.0")
	require.Equal(t, "real content", reasoning.Get("content").String(), "non-null content must not be stripped")
}

func TestPatchGrokResponsesBody_MultipleReasoningContentNull(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "grok-latest",
		"input": [
			{"type":"reasoning","summary":[{"type":"summary_text","text":"r1"}],"content":null},
			{"type":"message","role":"user","content":"hi"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"r2"}],"content":null}
		]
	}`)

	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)

	items := gjson.GetBytes(patched, "input").Array()
	require.Len(t, items, 3)

	require.False(t, items[0].Get("content").Exists())
	require.False(t, items[2].Get("content").Exists())
}

func TestIsGrokImageGenerationModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  bool
	}{
		{"grok-imagine", true},
		{"grok-imagine-image-quality", true},
		{"grok-imagine-edit", true},
		{"grok-imagine-image-hd", true},
		{" Grok-Imagine ", true},
		{"grok-imagine-video", false},
		{"grok-4.5", false},
		{"grok-composer", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, media.IsGrokImageGenerationModel(tt.model))
		})
	}
}
func TestForwardGrokResponsesRejectsMappedImageModelWithClientError(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"image-alias","input":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"image-alias": "grok-imagine-image-quality"},
		}},
	}

	result, err := (withSchedulerParametersForTest(&OpenAIGatewayService{})).Grok.ForwardResponses(
		context.Background(), c, account, body, "image-alias", false, time.Now(),
	)

	require.ErrorContains(t, err, "use /v1/images/generations instead")
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Equal(t, "model", gjson.GetBytes(recorder.Body.Bytes(), "error.param").String())
	require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "grok-imagine-image-quality")
}
