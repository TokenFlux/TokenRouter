package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCodexDetectorTestContext(ua string, originator string) *gin.Context {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if ua != "" {
		c.Request.Header.Set("User-Agent", ua)
	}
	if originator != "" {
		c.Request.Header.Set("originator", originator)
	}
	return c
}

func TestOpenAICodexClientRestrictionDetector_Detect(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("未开启开关时绕过", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("开启后 codex_cli_rs 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("codex_cli_rs/0.99.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex-tui 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("codex-tui/0.125.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("codex_vscode/1.0.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode_copilot 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("codex_vscode_copilot/1.0.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_app 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("codex_app/2.1.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 UA 尾部官方客户端命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		ua := "cccc/0.1.0 (Ubuntu 22.04; x86_64) xterm-256color (codex-tui; 0.125.0)"
		result := detector.Detect(newCodexDetectorTestContext(ua, ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 originator 命中", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", "codex_chatgpt_desktop"), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedOriginator, result.Reason)
	})

	t.Run("开启后伪造复合 UA 拒绝", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后伪造 originator 拒绝", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", "my_codex_thing"), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后非官方客户端拒绝", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", "my_client"), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启 ForceCodexCLI 时允许通过", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(&config.Config{
			Gateway: config.GatewayConfig{ForceCodexCLI: true},
		})
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", "my_client"), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonForceCodexCLI, result.Reason)
	})
}

func TestOpenAICodexClientRestrictionDetector_Detect_AllowedClients(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		claudeCodeUA         = "Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)"
		claudeCodeOriginator = "Claude Code"
	)

	t.Run("配置 claude_code 白名单且命中真实签名时放行", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.Detect(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})

	t.Run("配置白名单但伪造 originator 仍拒绝", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.Detect(newCodexDetectorTestContext(claudeCodeUA, "my_client"), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未配置白名单时 Claude Code 签名仍拒绝", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.Detect(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未开启 codex_cli_only 时白名单不参与，直接绕过", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}

		result := detector.Detect(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, TLSFingerprintRouterMatchResult{})
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("全局列表含 claude_code + 命中签名 → 放行(global)", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.Detect(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)", "Claude Code"),
			account,
			[]string{"claude_code"},
			TLSFingerprintRouterMatchResult{},
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedGlobalAllowedClient, result.Reason)
	})

	t.Run("全局列表含 claude_code + 非签名 → 403", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", "my_client"), account, []string{"claude_code"}, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("全局列表为空 + 账号未配 → 403", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.Detect(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			account,
			nil,
			TLSFingerprintRouterMatchResult{},
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("账号白名单优先于全局列表（reason=account）", func(t *testing.T) {
		detector := NewOpenAICodexClientRestrictionDetector(nil)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}
		result := detector.Detect(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			account,
			[]string{"claude_code"},
			TLSFingerprintRouterMatchResult{},
		)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})
}

func TestOpenAICodexClientRestrictionDetector_Detect_ClientPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	detector := NewOpenAICodexClientRestrictionDetector(nil)

	t.Run("新字段 any 直接绕过旧字段", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyAny,
				"codex_cli_only":             true,
			},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.False(t, result.Enabled)
		require.Equal(t, OpenAIOAuthClientPolicyAny, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("新字段 codex_only 仍按官方客户端判定", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyCodexOnly,
			},
		}

		result := detector.Detect(newCodexDetectorTestContext("curl/8.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, OpenAIOAuthClientPolicyCodexOnly, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("TLS 路由器策略未绑定路由器时拒绝", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			},
		}

		result := detector.Detect(newCodexDetectorTestContext("opencode/1.0", ""), account, nil, TLSFingerprintRouterMatchResult{})
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, OpenAIOAuthClientPolicyTLSRouterMatchedOnly, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonTLSRouterMissing, result.Reason)
	})

	t.Run("TLS 路由器策略命中时放行", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.Detect(
			newCodexDetectorTestContext("opencode/1.0", ""),
			account,
			nil,
			TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2},
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略命中时不受伪造 UA 影响", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.Detect(
			newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""),
			account,
			nil,
			TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2},
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略未命中时拒绝", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.Detect(
			newCodexDetectorTestContext("curl/8.0", ""),
			account,
			nil,
			TLSFingerprintRouterMatchResult{RouterID: 9},
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedTLSRouter, result.Reason)
	})
}
