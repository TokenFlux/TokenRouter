package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			credentials := map[string]any{"site": string(tt.site)}
			account := &accountcore.Record{
				ID:          int64(920 + index),
				Name:        "qoder-" + string(tt.site),
				Platform:    capability.PlatformQoder,
				Type:        capability.AccountTypeCosy,
				Credentials: credentials,
			}
			client := &gatewaytestkit.QoderClient{
				Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
					"data: {\"body\":\"[DONE]\"}\n\n",
			}
			service := gatewaytestkit.NewQoderFixture(accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{}),
				client, nil)

			service.Tokens.Core.Sessions = map[int64]accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
				account.ID: {
					CredentialsHash: accountcore.QoderCredentialsHash(account.Credentials),
					Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
				},
			}

			var err error
			switch tt.endpoint {
			case "chat":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, account, []byte(tt.body), protocolcore.ProtocolOpenAIChatCompletions)
			case "responses":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, account, []byte(tt.body), protocolcore.ProtocolOpenAIResponses)
			case "messages":
				_, err = ForwardQoderAttempt(context.Background(), c, service.Runtime, account, []byte(tt.body), protocolcore.ProtocolAnthropicMessages)
			default:
				t.Fatalf("unexpected endpoint %q", tt.endpoint)
			}
			require.NoError(t, err)
			payload := qoderLastUpstreamPayloadForTest(t, client)
			assertQoderThinkingPayload(t, payload, tt.wantEnabled, tt.wantEffort, tt.wantEffortPath)
		})
	}
}

func TestQoderThinkingUsesAccountMappedRouteKey(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	account := &accountcore.Record{
		ID:       901,
		Name:     "qoder-global",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site": "global",
			"model_mapping": map[string]any{
				"custom-qwen": "qmodel_38max",
			},
		},
	}
	client := &gatewaytestkit.QoderClient{
		Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	service := gatewaytestkit.NewQoderFixture(accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{}),
		client, nil)

	service.Tokens.Core.Sessions = map[int64]accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		account.ID: {
			CredentialsHash: accountcore.QoderCredentialsHash(account.Credentials),
			Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{
		"model":"custom-qwen",
		"reasoning_effort":"medium",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`)

	result, err := ForwardQoderAttempt(context.Background(), c, service.Runtime, account, body, protocolcore.ProtocolOpenAIChatCompletions)
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
