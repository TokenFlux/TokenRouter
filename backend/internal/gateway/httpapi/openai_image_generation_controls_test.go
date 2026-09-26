package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountconfig "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayServiceForward_RejectsDisabledImageGenerationIntents(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "image model",
			body: []byte(`{"model":"gpt-image-2","input":"draw"}`),
		},
		{
			name: "image tool",
			body: []byte(`{"model":"gpt-5.4","input":"draw","tools":[{"type":"image_generation"}]}`),
		},
		{
			name: "image tool choice",
			body: []byte(`{"model":"gpt-5.4","input":"draw","tool_choice":{"type":"image_generation"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(false, "unit-test-agent/1.0")
			account := newOpenAIImageGenerationControlTestAccount()

			result, err := svc.Forward(context.Background(), c, account, tt.body)

			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, "permission_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			require.Nil(t, upstream.lastReq, "disabled image request must not reach upstream")
		})
	}
}

func TestOpenAIGatewayServiceForward_DisabledGroupAllowsTextOnlyResponses(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_text","model":"gpt-5.4","usage":{"input_tokens":3,"output_tokens":2}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(false, "unit-test-agent/1.0")
	account := newOpenAIImageGenerationControlTestAccount()

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 0, result.ImageCount)
	require.NotNil(t, upstream.lastReq)
}

func TestOpenAIGatewayServiceForward_DisabledGroupAllowsPassiveImageNamespace(t *testing.T) {
	tests := []struct {
		name        string
		passthrough bool
	}{
		{name: "managed forwarding"},
		{name: "passthrough forwarding", passthrough: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_passive_namespace","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":2}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(false, "codex_cli_rs/0.144.1")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := newOpenAIImageGenerationControlTestAccount()
			account.Record.Extra = map[string]any{"openai_passthrough": tt.passthrough}
			body := []byte(`{
				"model":"gpt-5.5",
				"input":"write code",
				"stream":false,
				"tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],
				"tool_choice":"auto"
			}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `tools.#(name=="image_gen").type`).String())
			cached, known := GetOpenAIImageIntentHint(c)
			require.True(t, known)
			require.True(t, cached, "宽泛意图仍应保留给转发和计费")
		})
	}
}

func TestOpenAIGatewayServiceForward_CodexImageInjectionRespectsGroupCapability(t *testing.T) {
	tests := []struct {
		name          string
		allowImages   bool
		bridgeEnabled bool
		responsesLite bool
		wantInjected  bool
	}{
		{name: "disabled group skips injection", allowImages: false, bridgeEnabled: true, wantInjected: false},
		{name: "enabled group skips injection by default", allowImages: true, bridgeEnabled: false, wantInjected: false},
		{name: "enabled group injects image tool when bridge enabled", allowImages: true, bridgeEnabled: true, wantInjected: true},
		{name: "responses lite skips hosted image bridge", allowImages: true, bridgeEnabled: true, responsesLite: true, wantInjected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.ImageBridge.DefaultEnabled = tt.bridgeEnabled
			c, _ := newOpenAIImageGenerationControlTestContext(tt.allowImages, "codex_cli_rs/0.98.0")
			if tt.responsesLite {
				c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			}
			account := newOpenAIImageGenerationControlTestAccount()

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			hasImageTool := gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists()
			require.Equal(t, tt.wantInjected, hasImageTool)
			toolChoice := gjson.GetBytes(upstream.lastBody, "tool_choice")
			require.Equal(t, tt.wantInjected, toolChoice.Exists())
			if tt.wantInjected {
				require.Equal(t, "auto", toolChoice.String())
			}
			expectedLiteHeader := ""
			if tt.responsesLite {
				expectedLiteHeader = "true"
			}
			require.Equal(t, expectedLiteHeader, upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
			require.Equal(t, tt.wantInjected, strings.Contains(instructions, "image_generation"))
		})
	}
}

func TestOpenAIBuildUpstreamRequestOpenAIPassthroughForwardsResponsesLiteHeader(t *testing.T) {
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	c.Request.Header.Set(media.ResponsesLiteHeader, "true")

	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	req, err := svc.Requests.BuildPassthrough(
		c.Request.Context(),
		c,
		newOpenAIImageGenerationControlTestAccount(),
		[]byte(`{"model":"gpt-5.4","input":"write code"}`),
		"test-token",
	)

	require.NoError(t, err)
	require.Equal(t, "true", req.Header.Get(media.ResponsesLiteHeader))
}

func TestOpenAIGatewayServiceForward_ExplicitImageToolWorksWithBridgeDisabled(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_explicit_image","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	account := newOpenAIImageGenerationControlTestAccount()
	body := []byte(`{"model":"gpt-5.4","input":"draw","stream":false,"tools":[{"type":"image_generation","format":"jpeg"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.Equal(t, "jpeg", gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation").output_format`).String())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation").format`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

func TestOpenAIGatewayServiceForward_AccountPolicyStripsExplicitImageTool(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_stripped_image","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Record.Extra = map[string]any{
		accountconfig.CodexImageGenerationExplicitToolPolicyKey: accountconfig.CodexImagePolicyStrip,
	}
	body := []byte(`{
		"model":"gpt-5.4",
		"input":"draw",
		"stream":false,
		"tools":[
			{"type":"function","name":"shell","parameters":{"type":"object"}},
			{"type":"image_generation","format":"jpeg"}
		],
		"tool_choice":{"type":"image_generation"}
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="function")`).Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

func TestOpenAIGatewayServiceForward_AccountPolicyStripsImageNamespaceTools(t *testing.T) {
	tests := []struct {
		name        string
		passthrough bool
	}{
		{name: "managed forwarding"},
		{name: "passthrough forwarding", passthrough: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_stripped_namespace","model":"gpt-5.5","usage":{"input_tokens":2,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, _ := newOpenAIImageGenerationControlTestContext(false, "codex_cli_rs/0.144.1")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := newOpenAIImageGenerationControlTestAccount()
			account.Record.Extra = map[string]any{
				accountconfig.CodexImageGenerationExplicitToolPolicyKey: accountconfig.CodexImagePolicyStrip,
				"openai_passthrough": tt.passthrough,
			}
			body := []byte(`{
				"model":"gpt-5.5",
				"stream":false,
				"tools":[
					{"type":"function","name":"shell","parameters":{"type":"object"}},
					{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]},
					{"type":"namespace","name":"code_tools","tools":[{"type":"function","name":"run"}]}
				],
				"input":[
					{"type":"message","role":"user","content":[{"type":"input_text","text":"write code"}]},
					{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}
				],
				"tool_choice":"auto"
			}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(upstream.lastBody, &forwarded))
			require.False(t, openai.HasOpenAIImageGenerationTool(forwarded))
			require.Equal(t, "auto", forwarded["tool_choice"])
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(name=="shell")`).Exists())
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(name=="code_tools")`).Exists())
			require.Equal(t, "write code", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
			cached, known := GetOpenAIImageIntentHint(c)
			require.True(t, known)
			require.True(t, cached)
		})
	}
}

func TestOpenAIGatewayServiceForward_GroupProtocolEnablesCodexInjection(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_group_protocol","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	key, ok := c.MustGet("api_key").(*apikey.APIKey)
	require.True(t, ok)
	key.Group.ResponsesImagePolicy = "enabled"
	account := newOpenAIImageGenerationControlTestAccount()

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.Contains(t, instructions, "image_generation")
}

func TestOpenAIGatewayServiceForward_CodexBridgeDoesNotInjectHostedToolAlongsideImageGenNamespace(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_namespace_image","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	body := []byte(`{
		"model":"gpt-5.5",
		"stream":false,
		"tools":[
			{"type":"function","name":"shell","parameters":{"type":"object"}},
			{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}
		],
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"draw a cat"}]},
			{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}
		],
		"tool_choice":"auto"
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `tools.#(name=="image_gen").type`).String())
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools").tools.#(name=="image_gen").type`).String())
}

func TestOpenAIGatewayServiceForward_CodexBridgePreservesImageGenFunction(t *testing.T) {
	tests := []struct {
		name string
		tool string
	}{
		{
			name: "flat function",
			tool: `{"type":"function","name":"image_gen.imagegen","parameters":{"type":"object"}}`,
		},
		{
			name: "nested function",
			tool: `{"type":"function","function":{"name":"image_gen.imagegen","parameters":{"type":"object"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_function_image","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.ImageBridge.DefaultEnabled = true
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			body := []byte(`{"model":"gpt-5.5","input":"draw a cat","stream":false,"tools":[` + tt.tool + `]}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)

			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(upstream.lastBody, &forwarded))
			require.True(t, openai.HasCodexImageGenerationFunctionTool(forwarded))
			require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
			require.NotContains(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), openai.CodexImageGenerationBridgeMarker)
		})
	}
}

func TestOpenAIGatewayServiceForward_CodexBridgePreservesExistingToolChoice(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex_tool_choice","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	account := newOpenAIImageGenerationControlTestAccount()

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","input":"draw","stream":false,"tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "image_generation", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
}

func TestOpenAIGatewayServiceForward_CodexBridgeSkipsCompactRequests(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex_compact","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses/compact", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
	account := newOpenAIImageGenerationControlTestAccount()

	// /responses/compact 上游不接受 tool_choice，bridge 注入必须整体豁免 compact 请求。
	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","input":"summarize the conversation","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

// 分组协议设置优先于账号，旧分组功能默认值不再影响任何请求。
func TestOpenAIGatewayService_CodexImageGenerationBridgeOverridePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		global  bool
		group   string
		account *bool
		legacy  bool
		want    bool
	}{
		{name: "全局开启", global: true, want: true},
		{name: "忽略旧分组开启值", legacy: true, want: false},
		{name: "忽略旧分组关闭值", global: true, want: true},
		{name: "账号关闭覆盖全局", global: true, account: routing.BoolOverridePtr(false), want: false},
		{name: "账号开启覆盖全局", account: routing.BoolOverridePtr(true), want: true},
		{name: "分组协议开启优先于账号关闭", group: "enabled", account: routing.BoolOverridePtr(false), want: true},
		{name: "分组协议关闭优先于账号开启", group: "disabled", account: routing.BoolOverridePtr(true), want: false},
		{name: "分组协议屏蔽优先于账号开启", group: "block", account: routing.BoolOverridePtr(true), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
			svc.ImageBridge.DefaultEnabled = tt.global
			account := newOpenAIImageGenerationControlTestAccount()
			if tt.account != nil {
				account.Record.Extra = map[string]any{accountconfig.CodexImageGenerationBridgeKey: *tt.account}
			}
			groupID := int64(4242)
			key := &apikey.APIKey{GroupID: &groupID, Group: &routing.Group{ID: groupID, ResponsesImagePolicy: tt.group, RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: true, FeaturesConfig: map[string]any{accountconfig.CodexImageGenerationBridgeKey: map[string]any{capability.PlatformOpenAI: tt.legacy}},
			}}}
			require.Equal(t, tt.want, svc.ImageBridge.Enabled(context.Background(), account, key))
		})
	}
}

func TestOpenAIGatewayServiceHandleResponsesImageOutputs_NonStreaming(t *testing.T) {
	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	c, _ := newOpenAIImageGenerationControlTestContext(true, "unit-test-agent/1.0")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_image_json",
			"model":"gpt-5.4",
			"output":[{"id":"ig_json_1","type":"image_generation_call","result":"final-image"}],
			"usage":{"input_tokens":7,"output_tokens":3,"output_tokens_details":{"image_tokens":2}}
		}`)),
	}

	result, err := svc.Output.NonStream(context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountconfig.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.AccountTypeAPIKey}}, "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.NotNil(t, result.Usage)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.ImageOutputTokens)
}

func TestOpenAIGatewayServiceHandleResponsesImageOutputs_Streaming(t *testing.T) {
	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "unit-test-agent/1.0")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"result\":\"final-image\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_image_stream\",\"model\":\"gpt-5.5\",\"output\":[{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"result\":\"final-image\"}],\"usage\":{\"input_tokens\":11,\"output_tokens\":5,\"output_tokens_details\":{\"image_tokens\":4}}}}\n\n",
		)),
	}

	result, err := svc.Output.Stream(context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountconfig.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "gpt-5.5", "gpt-5.5", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.NotNil(t, result.Usage)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.ImageOutputTokens)
	require.NotContains(t, recorder.Body.String(), `"status":"generating"`)
	require.Equal(t, 2, strings.Count(recorder.Body.String(), `"status":"completed"`))
}

func TestOpenAIGatewayServiceHandleResponsesImageOutputs_StreamingPassthrough(t *testing.T) {
	svc := newOpenAIImageGenerationControlTestService(&auxiliaryHTTPRecorder{})
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "unit-test-agent/1.0")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"in_progress\",\"result\":\"final-image\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_image_stream\",\"model\":\"gpt-5.5\",\"output\":[{\"id\":\"ig_stream_1\",\"type\":\"image_generation_call\",\"status\":\"in_progress\",\"result\":\"final-image\"}],\"usage\":{\"input_tokens\":11,\"output_tokens\":5,\"output_tokens_details\":{\"image_tokens\":4}}}}\n\n",
		)),
	}

	result, err := svc.Output.PassthroughStream(context.Background(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountconfig.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "gpt-5.5", "gpt-5.5")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotContains(t, recorder.Body.String(), `"status":"in_progress"`)
	require.Equal(t, 2, strings.Count(recorder.Body.String(), `"status":"completed"`))
}

func TestNormalizeCompletedImageGenerationStatus(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		want        string
		wantChanged bool
	}{
		{
			name:        "output item done with result",
			input:       `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			want:        `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"completed","result":"image-data"}}`,
			wantChanged: true,
		},
		{
			name:        "terminal response only changes completed image result",
			input:       `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"in_progress","result":"image-data"},{"type":"image_generation_call","status":"failed","result":"partial-data"}]}}`,
			want:        `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"completed","result":"image-data"},{"type":"image_generation_call","status":"failed","result":"partial-data"}]}}`,
			wantChanged: true,
		},
		{
			name:        "done item without result",
			input:       `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`,
			want:        `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`,
			wantChanged: false,
		},
		{
			name:        "non-final image event",
			input:       `{"type":"response.output_item.added","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			want:        `{"type":"response.output_item.added","item":{"type":"image_generation_call","status":"generating","result":"image-data"}}`,
			wantChanged: false,
		},
		{
			name:        "done preserves base64 result",
			input:       `{"type":"response.done","response":{"output":[{"type":"image_generation_call","status":"generating","result":"iVBORw0KGgoAAAANSUhEUg/+=="}]}}`,
			want:        `{"type":"response.done","response":{"output":[{"type":"image_generation_call","status":"completed","result":"iVBORw0KGgoAAAANSUhEUg/+=="}]}}`,
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := openaicore.NormalizeCompletedImageGenerationStatus([]byte(tt.input))

			require.Equal(t, tt.wantChanged, changed)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func newOpenAIImageGenerationControlTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

func newOpenAIImageGenerationControlTestContext(allowImages bool, userAgent string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", userAgent)
	groupID := int64(4242)
	c.Set("api_key", &apikey.APIKey{
		ID:      2424,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: allowImages,
			RateMultiplier:       1,
		},
	})
	return c, recorder
}

func newOpenAIImageGenerationControlTestAccount() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{
		Record: accountconfig.Record{
			LoadLocation: time.LoadLocation, ID: 5151,
			Name:        "openai-image-controls",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key": "sk-test",
			},
		},
	}
}
