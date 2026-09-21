package qoder_test

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

func TestQoderThinkingDirectiveFromBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		effortPaths []string
		want        qoder.QoderThinkingDirective
	}{
		{name: "missing stays disabled", body: `{}`, effortPaths: []string{"reasoning.effort"}},
		{name: "minimal normalizes low", body: `{"reasoning":{"effort":"minimal"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "low"}},
		{name: "low stays low", body: `{"reasoning_effort":"LOW"}`, effortPaths: []string{"reasoning_effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "low"}},
		{name: "medium stays medium", body: `{"reasoning":{"effort":"medium"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "medium"}},
		{name: "high stays high", body: `{"reasoning":{"effort":"high"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "high"}},
		{name: "very high aliases map max", body: `{"reasoning":{"effort":"very_high"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "positive budget maps max", body: `{"thinking":{"budget_tokens":1}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "zero budget stays disabled", body: `{"thinking":{"budget_tokens":0}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "enabled without budget maps max", body: `{"thinking":{"type":"enabled"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "adaptive without budget maps max", body: `{"thinking":{"type":"adaptive"}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "explicit effort beats budget", body: `{"reasoning":{"effort":"low"},"thinking":{"budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "low"}},
		{name: "disabled beats effort and budget", body: `{"reasoning":{"effort":"max"},"thinking":{"type":"disabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "none effort beats enabled", body: `{"reasoning":{"effort":"none"},"thinking":{"type":"enabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "invalid effort falls back to budget", body: `{"reasoning":{"effort":"banana"},"thinking":{"budget_tokens":8}}`, effortPaths: []string{"reasoning.effort"}, want: qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "invalid effort alone stays disabled", body: `{"reasoning":{"effort":"banana"}}`, effortPaths: []string{"reasoning.effort"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, qoder.QoderThinkingDirectiveFromBody([]byte(tt.body), tt.effortPaths...))
		})
	}
}

func TestQoderThinkingParsersUseProtocolNativeFields(t *testing.T) {
	chat, err := qoder.ParseQoderChatCompletionsPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoder.QoderThinkingDirective{Enabled: true, Effort: "medium"}, chat.Thinking)

	responses, err := qoder.ParseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning":{"effort":"xhigh"},
		"input":"hello"
	}`))
	require.NoError(t, err)
	require.Equal(t, qoder.QoderThinkingDirective{Enabled: true, Effort: "max"}, responses.Thinking)

	// Anthropic 的显式等级优先于同时出现的预算。
	messages, err := qoder.ParseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"low"},
		"thinking":{"type":"enabled","budget_tokens":32768},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoder.QoderThinkingDirective{Enabled: true, Effort: "low"}, messages.Thinking)

	// Qoder 会忽略未知等级，不能被通用 Anthropic 转换层提前拒绝。
	messages, err = qoder.ParseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"ultra"},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoder.QoderThinkingDirective{}, messages.Thinking)
}

func TestBuildQoderAnthropicThinkingPayloadDefaultsToGlobalSite(t *testing.T) {
	// 不带站点的导出构造函数必须沿用旧账号语义，按国际站应用 Thinking 能力。
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"thinking":{"type":"enabled","budget_tokens":1},
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, modelKey, err := qoder.BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "dmodel", modelKey)
	assertQoderThinkingPayload(t, payload, true, "max", true)
}

func TestBuildQoderThinkingPayloadBySiteAndModelCapability(t *testing.T) {
	tests := []struct {
		name           string
		site           qoder.Site
		model          string
		extra          map[string]any
		wantEnabled    bool
		wantEffort     string
		wantEffortPath bool
	}{
		{name: "global qwen 38 public alias on", site: qoder.SiteGlobal, model: "qwen3.8-max", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true},
		{name: "global qwen 38 raw route on", site: qoder.SiteGlobal, model: "qmodel_38max", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "global qwen 38 missing control off", site: qoder.SiteGlobal, model: "qwen3.8-max", wantEffort: "none", wantEffortPath: true},
		{name: "global qwen 38 explicit disabled", site: qoder.SiteGlobal, model: "qwen3.8-max", extra: map[string]any{"thinking": map[string]any{"type": "disabled", "budget_tokens": 32768}}, wantEffort: "none", wantEffortPath: true},
		{name: "global qwen 38 invalid effort off", site: qoder.SiteGlobal, model: "qwen3.8-max", extra: map[string]any{"reasoning_effort": "banana"}, wantEffort: "none", wantEffortPath: true},
		{name: "global qwen 38 invalid effort with budget on", site: qoder.SiteGlobal, model: "qwen3.8-max", extra: map[string]any{"reasoning_effort": "banana", "thinking": map[string]any{"budget_tokens": 8}}, wantEnabled: true},
		{name: "global qwen 37 max default off", site: qoder.SiteGlobal, model: "qwen3.7-max", wantEffort: "none", wantEffortPath: true},
		{name: "global qwen 37 plus toggle on", site: qoder.SiteGlobal, model: "qwen3.7-plus", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "global deepseek pro low to high", site: qoder.SiteGlobal, model: "deepseek-v4-pro", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true, wantEffort: "high", wantEffortPath: true},
		{name: "global deepseek flash budget to max", site: qoder.SiteGlobal, model: "deepseek-v4-flash", extra: map[string]any{"thinking": map[string]any{"budget_tokens": 1}}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "global glm high to max", site: qoder.SiteGlobal, model: "glm-5.2", extra: map[string]any{"reasoning_effort": "high"}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "global glm 53 low stays low", site: qoder.SiteGlobal, model: "glm-5.3", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true, wantEffort: "low", wantEffortPath: true},
		{name: "global glm 53 high stays high", site: qoder.SiteGlobal, model: "glm-5.3", extra: map[string]any{"reasoning_effort": "high"}, wantEnabled: true, wantEffort: "high", wantEffortPath: true},
		{name: "cn qwen 38 public alias on", site: qoder.SiteCN, model: "qwen3.8-max", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true},
		{name: "cn qwen 38 raw route on", site: qoder.SiteCN, model: "qmodel_38max", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "cn qwen 37 max default off", site: qoder.SiteCN, model: "qwen3.7-max", wantEffort: "none", wantEffortPath: true},
		{name: "cn qwen 37 plus toggle on", site: qoder.SiteCN, model: "qwen3.7-plus", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "cn deepseek pro low to high", site: qoder.SiteCN, model: "deepseek-v4-pro", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true, wantEffort: "high", wantEffortPath: true},
		{name: "cn deepseek flash budget to max", site: qoder.SiteCN, model: "deepseek-v4-flash", extra: map[string]any{"thinking": map[string]any{"budget_tokens": 1}}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "cn glm high to max", site: qoder.SiteCN, model: "glm-5.2", extra: map[string]any{"reasoning_effort": "high"}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "cn glm 53 medium maps high", site: qoder.SiteCN, model: "glm-5.3", extra: map[string]any{"reasoning_effort": "medium"}, wantEnabled: true, wantEffort: "high", wantEffortPath: true},
		{name: "cn glm 53 max stays max", site: qoder.SiteCN, model: "gmodel", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "cn deepseek explicit disabled", site: qoder.SiteCN, model: "deepseek-v4-pro", extra: map[string]any{"thinking": map[string]any{"type": "disabled", "budget_tokens": 32768}}, wantEffort: "none", wantEffortPath: true},
		{name: "cn auto ignored", site: qoder.SiteCN, model: "auto", extra: map[string]any{"reasoning_effort": "max"}},
		{name: "cn qwen 36 ignored", site: qoder.SiteCN, model: "qwen3.6-flash", extra: map[string]any{"reasoning_effort": "max"}},
		{name: "cn kimi ignored", site: qoder.SiteCN, model: "kimi-k2.7-code", extra: map[string]any{"reasoning_effort": "max"}},
		{name: "cn minimax ignored", site: qoder.SiteCN, model: "minimax-m2.7", extra: map[string]any{"reasoning_effort": "max"}},
		{name: "cn unknown route ignored", site: qoder.SiteCN, model: "custom-model", extra: map[string]any{"reasoning_effort": "max"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model": tt.model,
				"messages": []any{
					map[string]any{"role": "user", "content": "hello"},
				},
			}
			for key, value := range tt.extra {
				body[key] = value
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			payload, _, err := qoder.BuildQoderPayloadFromChatCompletionsForSite(raw, "personal_standard", tt.site)
			require.NoError(t, err)
			assertQoderThinkingPayload(t, payload, tt.wantEnabled, tt.wantEffort, tt.wantEffortPath)
		})
	}
}

func TestBuildQoderThinkingPayloadKeepsGlobalOnlyModelIsolated(t *testing.T) {
	body := []byte(`{
		"model":"kimi-k3",
		"reasoning_effort":"max",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, _, err := qoder.BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", qoder.SiteGlobal)
	require.NoError(t, err)
	assertQoderThinkingPayload(t, payload, false, "", false)
}
