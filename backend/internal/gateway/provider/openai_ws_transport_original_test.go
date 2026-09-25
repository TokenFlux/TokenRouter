package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSProtocolResolver_Resolve(t *testing.T) {
	baseCfg := &wsTransportTestOptions{}
	baseCfg.Enabled = true
	baseCfg.OAuthEnabled = true
	baseCfg.APIKeyEnabled = true
	baseCfg.ResponsesWebsockets = false
	baseCfg.ResponsesWebsocketsV2 = true

	openAIOAuthEnabled := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
		},
	}

	t.Run("v2优先", func(t *testing.T) {
		decision := resolveTransportTest(baseCfg, openAIOAuthEnabled)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_enabled", decision.Reason)
	})

	t.Run("OpenAI setup token复用OAuth开关", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeSetupToken,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_enabled": true,
			},
		}
		decision := resolveTransportTest(baseCfg, account)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_enabled", decision.Reason)
	})

	t.Run("非OpenAI setup token不进入OAuth WS", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.AccountTypeSetupToken,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_enabled": true,
			},
		}
		decision := resolveTransportTest(baseCfg, account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "platform_not_openai", decision.Reason)
	})

	t.Run("v2关闭时回退v1", func(t *testing.T) {
		cfg := *baseCfg
		cfg.ResponsesWebsocketsV2 = false
		cfg.ResponsesWebsockets = true

		decision := resolveTransportTest(&cfg, openAIOAuthEnabled)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocket, decision.Transport)
		require.Equal(t, "ws_v1_enabled", decision.Reason)
	})

	t.Run("透传开关不影响WS协议判定", func(t *testing.T) {
		account := *openAIOAuthEnabled
		account.Extra = map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"openai_passthrough":                           true,
		}
		decision := resolveTransportTest(baseCfg, &account)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_enabled", decision.Reason)
	})

	t.Run("账号级强制HTTP", func(t *testing.T) {
		account := *openAIOAuthEnabled
		account.Extra = map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"openai_ws_force_http":                         true,
		}
		decision := resolveTransportTest(baseCfg, &account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "account_force_http", decision.Reason)
	})

	t.Run("全局强制HTTP无需启用mode router", func(t *testing.T) {
		cfg := *baseCfg
		cfg.ForceHTTP = true

		decision := resolveTransportTest(&cfg, openAIOAuthEnabled)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "global_force_http", decision.Reason)
	})

	t.Run("全局关闭保持HTTP", func(t *testing.T) {
		cfg := *baseCfg
		cfg.Enabled = false
		decision := resolveTransportTest(&cfg, openAIOAuthEnabled)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "global_disabled", decision.Reason)
	})

	t.Run("账号开关关闭保持HTTP", func(t *testing.T) {
		account := *openAIOAuthEnabled
		account.Extra = map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": false,
		}
		decision := resolveTransportTest(baseCfg, &account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "account_disabled", decision.Reason)
	})

	t.Run("OAuth账号不会读取API Key专用开关", func(t *testing.T) {
		account := *openAIOAuthEnabled
		account.Extra = map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}
		decision := resolveTransportTest(baseCfg, &account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "account_disabled", decision.Reason)
	})

	t.Run("兼容旧键openai_ws_enabled", func(t *testing.T) {
		account := *openAIOAuthEnabled
		account.Extra = map[string]any{
			"openai_ws_enabled": true,
		}
		decision := resolveTransportTest(baseCfg, &account)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_enabled", decision.Reason)
	})

	t.Run("按账号类型开关控制", func(t *testing.T) {
		cfg := *baseCfg
		cfg.OAuthEnabled = false
		decision := resolveTransportTest(&cfg, openAIOAuthEnabled)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "oauth_disabled", decision.Reason)
	})

	t.Run("API Key 账号关闭开关时回退HTTP", func(t *testing.T) {
		cfg := *baseCfg
		cfg.APIKeyEnabled = false
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		}
		decision := resolveTransportTest(&cfg, account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "apikey_disabled", decision.Reason)
	})

	t.Run("未知认证类型回退HTTP", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     "unknown_type",
			Extra: map[string]any{
				"responses_websockets_v2_enabled": true,
			},
		}
		decision := resolveTransportTest(baseCfg, account)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "unknown_auth_type", decision.Reason)
	})
}

func TestOpenAIWSProtocolResolver_Resolve_ModeRouterV2(t *testing.T) {
	cfg := &wsTransportTestOptions{}
	cfg.Enabled = true
	cfg.OAuthEnabled = true
	cfg.APIKeyEnabled = true
	cfg.ResponsesWebsocketsV2 = true
	cfg.ModeRouterV2Enabled = true
	cfg.IngressModeDefault = accountcore.OpenAIWSIngressModeCtxPool

	account := &accountcore.Record{
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModeCtxPool,
		},
	}

	t.Run("ctx_pool mode routes to ws v2", func(t *testing.T) {
		decision := resolveTransportTest(cfg, account)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_mode_ctx_pool", decision.Reason)
	})

	t.Run("off mode routes to http", func(t *testing.T) {
		offAccount := &accountcore.Record{
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModeOff,
			},
		}
		decision := resolveTransportTest(cfg, offAccount)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "account_mode_off", decision.Reason)
	})

	t.Run("legacy boolean maps to ctx_pool in v2 router", func(t *testing.T) {
		legacyAccount := &accountcore.Record{
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		}
		decision := resolveTransportTest(cfg, legacyAccount)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_mode_ctx_pool", decision.Reason)
	})

	t.Run("passthrough mode routes to ws v2", func(t *testing.T) {
		passthroughAccount := &accountcore.Record{
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModePassthrough,
			},
		}
		decision := resolveTransportTest(cfg, passthroughAccount)
		require.Equal(t, egress.OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "ws_v2_mode_passthrough", decision.Reason)
	})

	t.Run("http_bridge mode routes to http bridge decision", func(t *testing.T) {
		httpBridgeAccount := &accountcore.Record{
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModeHTTPBridge,
			},
		}
		decision := resolveTransportTest(cfg, httpBridgeAccount)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "ws_v2_mode_http_bridge", decision.Reason)
	})

	t.Run("non-positive concurrency is rejected in v2 router", func(t *testing.T) {
		invalidConcurrency := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_responses_websockets_v2_mode": accountcore.OpenAIWSIngressModeCtxPool,
			},
		}
		decision := resolveTransportTest(cfg, invalidConcurrency)
		require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "account_concurrency_invalid", decision.Reason)
	})
}

// 夹具只提供原开关与模式缺省；认证资格仍由真实账号模型解释。
type wsTransportTestOptions struct {
	egress.OpenAIWSOptions
	IngressModeDefault string
}

func resolveTransportTest(cfg *wsTransportTestOptions, value *accountcore.Record) egress.OpenAIWSProtocolDecision {
	if cfg == nil {
		return ResolveOpenAIWSTransport(value, nil, "")
	}
	return ResolveOpenAIWSTransport(value, &cfg.OpenAIWSOptions, cfg.IngressModeDefault)
}
