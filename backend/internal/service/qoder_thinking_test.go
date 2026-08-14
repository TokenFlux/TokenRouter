package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestQoderThinkingDirectiveFromBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		effortPaths []string
		want        qoderThinkingDirective
	}{
		{name: "missing stays disabled", body: `{}`, effortPaths: []string{"reasoning.effort"}},
		{name: "minimal maps high", body: `{"reasoning":{"effort":"minimal"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "high"}},
		{name: "low maps high", body: `{"reasoning_effort":"LOW"}`, effortPaths: []string{"reasoning_effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "high"}},
		{name: "medium maps high", body: `{"reasoning":{"effort":"medium"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "high"}},
		{name: "high maps max", body: `{"reasoning":{"effort":"high"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "very high aliases map max", body: `{"reasoning":{"effort":"very_high"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "positive budget maps max", body: `{"thinking":{"budget_tokens":1}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "zero budget stays disabled", body: `{"thinking":{"budget_tokens":0}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "enabled without budget maps max", body: `{"thinking":{"type":"enabled"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "adaptive without budget maps max", body: `{"thinking":{"type":"adaptive"}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "explicit effort beats budget", body: `{"reasoning":{"effort":"low"},"thinking":{"budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "high"}},
		{name: "disabled beats effort and budget", body: `{"reasoning":{"effort":"max"},"thinking":{"type":"disabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "none effort beats enabled", body: `{"reasoning":{"effort":"none"},"thinking":{"type":"enabled","budget_tokens":32768}}`, effortPaths: []string{"reasoning.effort"}},
		{name: "invalid effort falls back to budget", body: `{"reasoning":{"effort":"banana"},"thinking":{"budget_tokens":8}}`, effortPaths: []string{"reasoning.effort"}, want: qoderThinkingDirective{Enabled: true, Effort: "max"}},
		{name: "invalid effort alone stays disabled", body: `{"reasoning":{"effort":"banana"}}`, effortPaths: []string{"reasoning.effort"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, qoderThinkingDirectiveFromBody([]byte(tt.body), tt.effortPaths...))
		})
	}
}

func TestQoderThinkingParsersUseProtocolNativeFields(t *testing.T) {
	chat, err := parseQoderChatCompletionsPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoderThinkingDirective{Enabled: true, Effort: "high"}, chat.thinking)

	responses, err := parseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"reasoning":{"effort":"xhigh"},
		"input":"hello"
	}`))
	require.NoError(t, err)
	require.Equal(t, qoderThinkingDirective{Enabled: true, Effort: "max"}, responses.thinking)

	// Anthropic 的显式等级优先于同时出现的预算。
	messages, err := parseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"low"},
		"thinking":{"type":"enabled","budget_tokens":32768},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoderThinkingDirective{Enabled: true, Effort: "high"}, messages.thinking)

	// Qoder 会忽略未知等级，不能被通用 Anthropic 转换层提前拒绝。
	messages, err = parseQoderAnthropicMessagesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"output_config":{"effort":"ultra"},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, qoderThinkingDirective{}, messages.thinking)
}

func TestBuildQoderAnthropicThinkingPayloadDefaultsToGlobalSite(t *testing.T) {
	// 不带站点的导出构造函数必须沿用旧账号语义，按国际站应用 Thinking 能力。
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"max_tokens":1024,
		"thinking":{"type":"enabled","budget_tokens":1},
		"messages":[{"role":"user","content":"hello"}]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "dmodel", modelKey)
	assertQoderThinkingPayload(t, payload, true, "max", true)
}

func TestQoderThinkingFlowsThroughAllEndpoints(t *testing.T) {
	tests := []struct {
		name           string
		site           qoder.Site
		endpoint       string
		body           string
		wantEnabled    bool
		wantEffort     string
		wantEffortPath bool
	}{
		{
			name:           "cn deepseek chat completions",
			site:           qoder.SiteCN,
			endpoint:       "chat",
			body:           `{"model":"deepseek-v4-pro","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "cn deepseek responses",
			site:           qoder.SiteCN,
			endpoint:       "responses",
			body:           `{"model":"deepseek-v4-pro","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:           "cn deepseek anthropic messages",
			site:           qoder.SiteCN,
			endpoint:       "messages",
			body:           `{"model":"deepseek-v4-pro","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek chat completions",
			site:           qoder.SiteGlobal,
			endpoint:       "chat",
			body:           `{"model":"deepseek-v4-pro","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "high",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek responses",
			site:           qoder.SiteGlobal,
			endpoint:       "responses",
			body:           `{"model":"deepseek-v4-pro","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:           "global deepseek anthropic budget",
			site:           qoder.SiteGlobal,
			endpoint:       "messages",
			body:           `{"model":"deepseek-v4-pro","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled:    true,
			wantEffort:     "max",
			wantEffortPath: true,
		},
		{
			name:        "global qwen 38 chat completions public alias",
			site:        qoder.SiteGlobal,
			endpoint:    "chat",
			body:        `{"model":"qwen3.8-max","reasoning_effort":"low","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled: true,
		},
		{
			name:        "global qwen 38 responses raw route",
			site:        qoder.SiteGlobal,
			endpoint:    "responses",
			body:        `{"model":"qmodel_38max","reasoning":{"effort":"high"},"input":"hello","stream":true}`,
			wantEnabled: true,
		},
		{
			name:        "global qwen 38 anthropic messages",
			site:        qoder.SiteGlobal,
			endpoint:    "messages",
			body:        `{"model":"qwen3.8-max","max_tokens":1024,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled: true,
		},
		{
			name:        "cn qwen 38 chat completions raw route",
			site:        qoder.SiteCN,
			endpoint:    "chat",
			body:        `{"model":"qmodel_38max","reasoning_effort":"medium","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled: true,
		},
		{
			name:        "cn qwen 38 responses public alias",
			site:        qoder.SiteCN,
			endpoint:    "responses",
			body:        `{"model":"qwen3.8-max","reasoning":{"effort":"max"},"input":"hello","stream":true}`,
			wantEnabled: true,
		},
		{
			name:        "cn qwen 38 anthropic messages",
			site:        qoder.SiteCN,
			endpoint:    "messages",
			body:        `{"model":"qwen3.8-max","max_tokens":1024,"thinking":{"budget_tokens":1},"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantEnabled: true,
		},
	}

	gin.SetMode(gin.TestMode)
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			credentials := map[string]any{"site": string(tt.site)}
			account := &Account{
				ID:          int64(920 + index),
				Name:        "qoder-" + string(tt.site),
				Platform:    PlatformQoder,
				Type:        AccountTypeCosy,
				Credentials: credentials,
			}
			client := &qoderAccountTestClientStub{
				body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
					"data: {\"body\":\"[DONE]\"}\n\n",
			}
			service := &QoderGatewayService{
				tokenProvider: &QoderTokenProvider{},
				client:        client,
			}
			service.tokenProvider.sessions = map[int64]qoderSessionCacheEntry{
				account.ID: {
					credentialsHash: qoderCredentialsHash(account.Credentials),
					session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
				},
			}

			var err error
			switch tt.endpoint {
			case "chat":
				_, err = service.ForwardChatCompletions(context.Background(), c, account, []byte(tt.body))
			case "responses":
				_, err = service.ForwardResponses(context.Background(), c, account, []byte(tt.body))
			case "messages":
				_, err = service.ForwardMessages(context.Background(), c, account, []byte(tt.body))
			default:
				t.Fatalf("unexpected endpoint %q", tt.endpoint)
			}
			require.NoError(t, err)
			payload := qoderLastUpstreamPayloadForTest(t, client)
			assertQoderThinkingPayload(t, payload, tt.wantEnabled, tt.wantEffort, tt.wantEffortPath)
		})
	}
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
		{name: "cn qwen 38 public alias on", site: qoder.SiteCN, model: "qwen3.8-max", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true},
		{name: "cn qwen 38 raw route on", site: qoder.SiteCN, model: "qmodel_38max", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "cn qwen 37 max default off", site: qoder.SiteCN, model: "qwen3.7-max", wantEffort: "none", wantEffortPath: true},
		{name: "cn qwen 37 plus toggle on", site: qoder.SiteCN, model: "qwen3.7-plus", extra: map[string]any{"reasoning_effort": "max"}, wantEnabled: true},
		{name: "cn deepseek pro low to high", site: qoder.SiteCN, model: "deepseek-v4-pro", extra: map[string]any{"reasoning_effort": "low"}, wantEnabled: true, wantEffort: "high", wantEffortPath: true},
		{name: "cn deepseek flash budget to max", site: qoder.SiteCN, model: "deepseek-v4-flash", extra: map[string]any{"thinking": map[string]any{"budget_tokens": 1}}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
		{name: "cn glm high to max", site: qoder.SiteCN, model: "glm-5.2", extra: map[string]any{"reasoning_effort": "high"}, wantEnabled: true, wantEffort: "max", wantEffortPath: true},
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
			payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(raw, "personal_standard", tt.site)
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

	payload, _, err := BuildQoderPayloadFromChatCompletionsForSite(body, "personal_standard", qoder.SiteGlobal)
	require.NoError(t, err)
	assertQoderThinkingPayload(t, payload, false, "", false)
}

func TestQoderThinkingUsesAccountMappedRouteKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	account := &Account{
		ID:       901,
		Name:     "qoder-global",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"site": "global",
			"model_mapping": map[string]any{
				"custom-qwen": "qmodel_38max",
			},
		},
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	service := &QoderGatewayService{
		tokenProvider: &QoderTokenProvider{},
		client:        client,
	}
	service.tokenProvider.sessions = map[int64]qoderSessionCacheEntry{
		account.ID: {
			credentialsHash: qoderCredentialsHash(account.Credentials),
			session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{
		"model":"custom-qwen",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`)

	result, err := service.ForwardChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Equal(t, "qmodel_38max", result.UpstreamModel)
	payload := qoderLastUpstreamPayloadForTest(t, client)
	assertQoderThinkingPayload(t, payload, true, "", false)
}

// assertQoderThinkingPayload 校验 Qoder 会读取的所有开关和等级副本保持一致。
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
