package service

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestQoderHighestContextFlowsThroughAllProtocols(t *testing.T) {
	tests := []struct {
		name  string
		body  []byte
		parse func([]byte) (qoderPayloadRequest, error)
	}{
		{
			name:  "Chat Completions",
			body:  []byte(`{"model":"qwen3.8-max","messages":[{"role":"user","content":"hello"}]}`),
			parse: parseQoderChatCompletionsPayload,
		},
		{
			name:  "Responses",
			body:  []byte(`{"model":"qwen3.8-max","input":"hello"}`),
			parse: parseQoderResponsesPayload,
		},
		{
			name:  "Anthropic Messages",
			body:  []byte(`{"model":"qwen3.8-max","max_tokens":1024,"messages":[{"role":"user","content":"hello"}]}`),
			parse: parseQoderAnthropicMessagesPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := tt.parse(tt.body)
			require.NoError(t, err)
			request.site = qoder.SiteGlobal
			payload, modelKey := buildQoderPayloadWithOptions(request, "", request.messages, true, true)

			require.Equal(t, "qmodel_38max", modelKey)
			assertQoderContextCapabilityForTest(t, payload, 1000000, true)
		})
	}
}

func TestQoderContextCapabilityUsesSiteAndRouteFallback(t *testing.T) {
	tests := []struct {
		name        string
		site        qoder.Site
		model       string
		wantTokens  int
		wantRuntime bool
	}{
		{name: "国际站固定 Auto", site: qoder.SiteGlobal, model: "auto", wantTokens: 180000},
		{name: "国际站 Kimi K2.7", site: qoder.SiteGlobal, model: "kimi-k2.7-code", wantTokens: 256000, wantRuntime: true},
		{name: "国际站 MiniMax M3", site: qoder.SiteGlobal, model: "minimax-m3", wantTokens: 1000000, wantRuntime: true},
		{name: "国内站 Qwen3.6", site: qoder.SiteCN, model: "qwen3.6-flash", wantTokens: 1000000, wantRuntime: true},
		{name: "国内站 MiniMax M2.7", site: qoder.SiteCN, model: "minimax-m2.7", wantTokens: 200000, wantRuntime: true},
		{name: "未知 route", site: qoder.SiteGlobal, model: "custom-route", wantTokens: qoder.FallbackMaxInputTokens},
		{name: "隐藏 route", site: qoder.SiteGlobal, model: "cmodel", wantTokens: qoder.FallbackMaxInputTokens},
		{name: "已移除 route", site: qoder.SiteCN, model: "qmodel_preview", wantTokens: qoder.FallbackMaxInputTokens},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _, err := BuildQoderPayloadFromChatCompletionsForSite([]byte(`{"model":"`+tt.model+`","messages":[{"role":"user","content":"hello"}]}`), "personal_standard", tt.site)
			require.NoError(t, err)
			assertQoderContextCapabilityForTest(t, payload, tt.wantTokens, tt.wantRuntime)
		})
	}
}

func TestQoderContextCapabilityMergesWithThinkingOverride(t *testing.T) {
	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning_effort":"high",
		"messages":[{"role":"user","content":"hello"}]
	}`), "personal_standard", qoder.SiteGlobal)
	require.NoError(t, err)
	assertQoderContextCapabilityForTest(t, payload, 1000000, true)

	parameters := requireQoderPayloadMapForTest(t, payload["parameters"], "parameters")
	require.Equal(t, "max", parameters["reasoning_effort"])
	chatContext := requireQoderPayloadMapForTest(t, payload["chat_context"], "chat_context")
	extra := requireQoderPayloadMapForTest(t, chatContext["extra"], "chat_context.extra")
	runtimeOverride := requireQoderPayloadMapForTest(t, extra["ideModelConfigOverride"], "chat_context.extra.ideModelConfigOverride")
	require.Equal(t, "max", runtimeOverride["reasoning_effort"])
	require.Equal(t, 1000000, runtimeOverride["max_input_tokens"])
}

// assertQoderContextCapabilityForTest 同时校验顶层上限和可选档位的两个运行时字段。
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

// requireQoderPayloadMapForTest 校验 payload 路径为 JSON 对象并返回对应 map。
func requireQoderPayloadMapForTest(t *testing.T, value any, path string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "%s 应为 JSON 对象", path)
	return result
}
