package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountSupportsOpenAIEndpointCapability(t *testing.T) {
	t.Run("OpenAI APIKey 默认兼容 chat、embeddings 和 alpha search", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityEmbeddings))
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityAlphaSearch))
	})

	t.Run("OpenAI OAuth 默认仅兼容 chat", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("alpha search 允许 OpenAI OAuth/PAT 与 APIKey 账号，拒绝 Grok", func(t *testing.T) {
		// OAuth/PAT 走 chatgpt.com Codex 端点，APIKey 走 {base_url}/v1/alpha/search，
		// 两类都能承接独立搜索（APIKey 被排除曾导致纯 APIKey 分组搜索失效的回归）。
		apiKey := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
		}
		oauth := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
		}
		grok := &accountcore.Record{
			Platform: capability.PlatformGrok,
			Type:     capability.AccountTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(apiKey, accountcore.OpenAIEndpointCapabilityAlphaSearch))
		require.True(t, SupportsOpenAIEndpoint(oauth, accountcore.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(grok, accountcore.OpenAIEndpointCapabilityAlphaSearch))
	})

	t.Run("显式列表支持同时声明 chat 和 embeddings", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation", "embeddings"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("显式列表只声明 chat 时不支持 embeddings", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
		// chat 能力隐含放行 alpha search（OAuth/APIKey 语义一致）。
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("OAuth 显式列表沿用 chat 能力放行 alpha search", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityAlphaSearch))
	})

	// OAuth 历史数据可能保留空能力容器，应与缺失字段一样不阻断文本调度。
	for _, emptyCapabilities := range []struct {
		name  string
		value any
	}{
		{name: "map[string]any", value: map[string]any{}},
		{name: "[]any", value: []any{}},
		{name: "[]string", value: []string{}},
	} {
		t.Run("OAuth 空能力 "+emptyCapabilities.name, func(t *testing.T) {
			account := &accountcore.Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Credentials: map[string]any{
					accountcore.OpenAIWorkloadCapabilitiesCredentialKey: emptyCapabilities.value,
				},
			}

			require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
			require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
		})
	}

	t.Run("API Key 空能力仍表示显式禁用", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				accountcore.OpenAIWorkloadCapabilitiesCredentialKey: []any{},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("SetupToken 空能力与 OAuth 一样回退为未配置", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeSetupToken,
			Credentials: map[string]any{
				accountcore.OpenAIWorkloadCapabilitiesCredentialKey: map[string]any{},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("非空全 false 能力仍按显式禁用处理", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Credentials: map[string]any{
				accountcore.OpenAIWorkloadCapabilitiesCredentialKey: map[string]any{
					string(accountcore.OpenAIEndpointCapabilityTextGeneration): false,
				},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("类型异常仍按已配置但不含能力处理", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Credentials: map[string]any{
				accountcore.OpenAIWorkloadCapabilitiesCredentialKey: "text_generation",
			},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("显式数组支持单独关闭文本生成并开启 embeddings", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"embeddings"},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("未知能力不应默认放行", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapability("unknown")))
	})

	t.Run("responses 能力：未探测的 APIKey 默认放行", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：历史探测不再排除 APIKey", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"openai_responses_probe_status": "unsupported"},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
		// 非生图路径仍可选中（只要求 chat_completions）。
		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("responses 能力：探测确认支持的 APIKey 放行", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"openai_responses_probe_status": "supported"},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：force_chat_completions 覆盖排除 APIKey", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"openai_text_route_mode": "force_chat_completions"},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：OAuth 账号不受探测标记影响", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"openai_responses_probe_status": "unsupported"},
		}

		require.True(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：仍需通过 chat_completions 配置集校验", func(t *testing.T) {
		// 未探测（默认支持 responses），但显式能力集未声明 chat_completions。
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"embeddings"},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(account, accountcore.OpenAIEndpointCapabilityResponses))
	})
}
