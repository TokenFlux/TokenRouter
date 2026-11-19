package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// record_endpoints_test.go verifies base URL defaults, passthrough switches,
// OpenAI OAuth client policies, warmup interception, and TLS fingerprint gates.

// TestProvider_IsAnthropicAPIKeyPassthroughEnabled varies saved booleans and
// provider eligibility for Anthropic passthrough. Only an eligible API Key with
// a true boolean enables the accessor; unrelated types and malformed values
// remain disabled.
func TestProvider_IsAnthropicAPIKeyPassthroughEnabled(t *testing.T) {
	t.Run("Anthropic API Key 开启", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.True(t, provider.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("Anthropic API Key 关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": false,
			},
		}
		require.False(t, provider.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("字段类型非法默认关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": "true",
			},
		}
		require.False(t, provider.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("非 Anthropic API Key 提供商始终关闭", func(t *testing.T) {
		oauth := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.False(t, oauth.IsAnthropicAPIKeyPassthroughEnabled())

		openai := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.False(t, openai.IsAnthropicAPIKeyPassthroughEnabled())
	})
}

// TestGetBaseURL supplies API Key and OAuth records with missing or custom base
// URLs. The API Key uses the Anthropic default when absent, while other types
// return empty.
func TestGetBaseURL(t *testing.T) {
	tests := []struct {
		name     string // test case display name
		provider Record // provider configuration under test
		expected string // expected accessor result
	}{
		{
			name: "non-apikey type returns empty",
			provider: Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformAnthropic,
			},
			expected: "",
		},
		{
			name: "apikey without base_url returns default anthropic",
			provider: Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformAnthropic,
				Credentials: map[string]any{},
			},
			expected: "https://api.anthropic.com",
		},
		{
			name: "apikey with custom base_url",
			provider: Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformAnthropic,
				Credentials: map[string]any{"base_url": "https://custom.example.com"},
			},
			expected: "https://custom.example.com",
		},
		{
			name: "antigravity non-apikey returns empty",
			provider: Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.provider.GetBaseURL()
			if result != tt.expected {
				t.Errorf("GetBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestGetGeminiBaseURL supplies custom or missing endpoints across Gemini API
// Key and Antigravity OAuth records. The accessor returns the supplied endpoint
// directly or the caller's default without adding an Antigravity path.
func TestGetGeminiBaseURL(t *testing.T) {
	const defaultGeminiURL = "https://generativelanguage.googleapis.com"

	tests := []struct {
		name     string // test case display name
		provider Record // provider configuration under test
		expected string // expected accessor result
	}{
		{
			name: "apikey without base_url returns default",
			provider: Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformGemini,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "apikey with custom base_url",
			provider: Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformGemini,
				Credentials: map[string]any{"base_url": "https://custom-gemini.example.com"},
			},
			expected: "https://custom-gemini.example.com",
		},
		{
			name: "antigravity oauth does NOT append /antigravity",
			provider: Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com",
		},
		{
			name: "oauth without base_url returns default",
			provider: Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "nil credentials returns default",
			provider: Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformGemini,
			},
			expected: defaultGeminiURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.provider.GetGeminiBaseURL(defaultGeminiURL)
			if result != tt.expected {
				t.Errorf("GetGeminiBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestProvider_GetCodexCLIOnlyAllowedClients supplies typed and mixed client-ID
// arrays on OAuth and API Key records. The accessor filters blank or nonstring
// entries and returns no IDs for ineligible or absent configuration.
func TestProvider_GetCodexCLIOnlyAllowedClients(t *testing.T) {
	t.Run("OAuth 提供商读取 []any 字符串列表", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}
		require.Equal(t, []string{"claude_code"}, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("OAuth 提供商读取 []string 列表", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []string{"claude_code"}},
		}
		require.Equal(t, []string{"claude_code"}, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("[]string 跳过空白元素", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []string{"claude_code", "", "  "}},
		}
		require.Equal(t, []string{"claude_code"}, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("跳过非字符串与空白元素", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code", 123, "", "  "}},
		}
		require.Equal(t, []string{"claude_code"}, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("非 OAuth 提供商返回空", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}
		require.Empty(t, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("Extra 为空返回空", func(t *testing.T) {
		provider := &Record{Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}
		require.Empty(t, provider.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("字段缺失返回空", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{},
		}
		require.Empty(t, provider.GetCodexCLIOnlyAllowedClients())
	})
}

// TestProvider_IsInterceptWarmupEnabled varies the credential object's
// warmup-interception value and presence. Boolean true enables it, while
// missing, nil, string, integer, and false inputs keep it disabled.
func TestProvider_IsInterceptWarmupEnabled(t *testing.T) {
	tests := []struct {
		name        string         // test case display name
		credentials map[string]any // input provider credential object
		expected    bool           // expected accessor result
	}{
		{name: "nil credentials", credentials: nil, expected: false},
		{name: "empty map", credentials: map[string]any{}, expected: false},
		{name: "field not present", credentials: map[string]any{"access_token": "tok"}, expected: false},
		{name: "field is true", credentials: map[string]any{"intercept_warmup_requests": true}, expected: true},
		{name: "field is false", credentials: map[string]any{"intercept_warmup_requests": false}, expected: false},
		{name: "field is string true", credentials: map[string]any{"intercept_warmup_requests": "true"}, expected: false},
		{name: "field is int 1", credentials: map[string]any{"intercept_warmup_requests": 1}, expected: false},
		{name: "field is nil", credentials: map[string]any{"intercept_warmup_requests": nil}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Record{Credentials: tt.credentials}
			require.Equal(t, tt.expected, a.IsInterceptWarmupEnabled())
		})
	}
}

// TestProvider_IsOpenAIPassthroughEnabled checks the current and legacy OpenAI
// passthrough keys alongside unrelated platforms and absent Extra. Either
// supported OpenAI selection enables it, while the unrelated and absent cases
// stay disabled.
func TestProvider_IsOpenAIPassthroughEnabled(t *testing.T) {
	t.Run("新字段开启", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"openai_passthrough": true},
		}
		require.True(t, provider.IsOpenAIPassthroughEnabled())
	})

	t.Run("兼容旧字段", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"openai_oauth_passthrough": true},
		}
		require.True(t, provider.IsOpenAIPassthroughEnabled())
	})

	t.Run("非OpenAI提供商始终关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"openai_passthrough": true},
		}
		require.False(t, provider.IsOpenAIPassthroughEnabled())
	})

	t.Run("空额外配置默认关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		}
		require.False(t, provider.IsOpenAIPassthroughEnabled())
	})
}

// TestProvider_IsOpenAIOAuthPassthroughEnabled supplies the same true
// passthrough setting to OAuth and API Key records. The legacy OAuth-specific
// accessor permits the OAuth record and rejects the API Key.
func TestProvider_IsOpenAIOAuthPassthroughEnabled(t *testing.T) {
	t.Run("仅OAuth类型允许返回开启", func(t *testing.T) {
		oauthProvider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"openai_passthrough": true},
		}
		require.True(t, oauthProvider.IsOpenAIOAuthPassthroughEnabled())

		apiKeyProvider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"openai_passthrough": true},
		}
		require.False(t, apiKeyProvider.IsOpenAIOAuthPassthroughEnabled())
	})
}

// TestProvider_IsCodexCLIOnlyEnabled varies legacy booleans, provider
// eligibility, and recognized current policy values. Current any policy
// overrides a true legacy flag, and TLS-router policy remains distinct from
// Codex-only policy.
func TestProvider_IsCodexCLIOnlyEnabled(t *testing.T) {
	t.Run("OpenAI OAuth 开启", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		require.True(t, provider.IsCodexCLIOnlyEnabled())
	})

	t.Run("OpenAI OAuth 关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": false},
		}
		require.False(t, provider.IsCodexCLIOnlyEnabled())
	})

	t.Run("字段缺失默认关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{},
		}
		require.False(t, provider.IsCodexCLIOnlyEnabled())
	})

	t.Run("类型非法默认关闭", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": "true"},
		}
		require.False(t, provider.IsCodexCLIOnlyEnabled())
	})

	t.Run("非 OAuth 提供商始终关闭", func(t *testing.T) {
		apiKeyProvider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		require.False(t, apiKeyProvider.IsCodexCLIOnlyEnabled())

		otherPlatform := &Record{
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		require.False(t, otherPlatform.IsCodexCLIOnlyEnabled())
	})

	t.Run("新策略字段优先于旧字段", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyAny,
				"codex_cli_only":             true,
			},
		}
		require.False(t, provider.IsCodexCLIOnlyEnabled())
		require.Equal(t, OpenAIOAuthClientPolicyAny, provider.GetOpenAIOAuthClientPolicy())
	})

	t.Run("TLS 路由器策略不等同于 Codex-only", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(12),
			},
		}
		require.False(t, provider.IsCodexCLIOnlyEnabled())
		require.True(t, provider.IsOpenAIOAuthTLSRouterMatchedOnly())
		require.Equal(t, int64(12), provider.GetTLSFingerprintRouterID())
	})
}

// TestProvider_IsTLSFingerprintEnabled combines supported token types, an
// unsupported OpenAI API Key, and malformed settings. Emulation needs a
// supported provider and true boolean; missing and nil cases remain disabled.
func TestProvider_IsTLSFingerprintEnabled(t *testing.T) {
	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		want     bool    // expected accessor result
	}{
		{
			name: "Anthropic OAuth 开启",
			provider: &Record{
				Platform: capability.PlatformAnthropic,
				Type:     capability.ProviderTypeOAuth,
				Extra:    map[string]any{"enable_tls_fingerprint": true},
			},
			want: true,
		},
		{
			name: "Anthropic SetupToken 开启",
			provider: &Record{
				Platform: capability.PlatformAnthropic,
				Type:     capability.ProviderTypeSetupToken,
				Extra:    map[string]any{"enable_tls_fingerprint": true},
			},
			want: true,
		},
		{
			name: "OpenAI OAuth 开启",
			provider: &Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Extra:    map[string]any{"enable_tls_fingerprint": true},
			},
			want: true,
		},
		{
			name: "OpenAI API Key 不支持",
			provider: &Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeAPIKey,
				Extra:    map[string]any{"enable_tls_fingerprint": true},
			},
			want: false,
		},
		{
			name: "非法类型按关闭处理",
			provider: &Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Extra:    map[string]any{"enable_tls_fingerprint": "true"},
			},
			want: false,
		},
		{
			name: "字段缺失按关闭处理",
			provider: &Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Extra:    map[string]any{},
			},
			want: false,
		},
		{
			name:     "nil 提供商按关闭处理",
			provider: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.provider.IsTLSFingerprintEnabled())
		})
	}
}
