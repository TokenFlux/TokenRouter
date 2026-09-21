package account_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// newCodexDetectorTestContext 保留原 HTTP Header 的取值形状，核心只按需读取字符串。
func newCodexDetectorTestContext(ua string, originator string) func() (string, string) {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if originator != "" {
		req.Header.Set("originator", originator)
	}
	return func() (string, string) { return req.Header.Get("User-Agent"), req.Header.Get("originator") }
}

func newCodexDetectorFixture(cfg *config.Config) *accountcore.CodexClientDetector {
	return &accountcore.CodexClientDetector{Options: accountcore.CodexClientOptions{ForceCLI: cfg != nil && cfg.Gateway.ForceCodexCLI, OfficialUserAgent: openai.IsCodexOfficialClientRequestStrict, OfficialOriginator: openai.IsCodexOfficialClientOriginator, AllowedClients: openai.MatchAllowedClients}}
}

func TestOpenAICodexClientRestrictionDetector_Detect(t *testing.T) {

	t.Run("未开启开关时绕过", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Extra: map[string]any{}}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), account, nil, false)
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("开启后 codex_cli_rs 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_cli_rs/0.99.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex-tui 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex-tui/0.125.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_vscode/1.0.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode_copilot 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_vscode_copilot/1.0.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_app 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_app/2.1.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 UA 尾部官方客户端命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		ua := "cccc/0.1.0 (Ubuntu 22.04; x86_64) xterm-256color (codex-tui; 0.125.0)"
		result := detector.DetectClient(newCodexDetectorTestContext(ua, ""), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 originator 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "codex_chatgpt_desktop"), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedOriginator, result.Reason)
	})

	t.Run("开启后伪造复合 UA 拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后伪造 originator 拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_codex_thing"), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后非官方客户端拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启 ForceCodexCLI 时允许通过", func(t *testing.T) {
		detector := newCodexDetectorFixture(&config.Config{
			Gateway: config.GatewayConfig{ForceCodexCLI: true},
		})
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonForceCodexCLI, result.Reason)
	})
}

func TestOpenAICodexClientRestrictionDetector_Detect_AllowedClients(t *testing.T) {

	const (
		claudeCodeUA         = "Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)"
		claudeCodeOriginator = "Claude Code"
	)

	t.Run("配置 claude_code 白名单且命中真实签名时放行", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})

	t.Run("配置白名单但伪造 originator 仍拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, "my_client"), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未配置白名单时 Claude Code 签名仍拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未开启 codex_cli_only 时白名单不参与，直接绕过", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), account, nil, false)
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("全局列表含 claude_code + 命中签名 → 放行(global)", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)", "Claude Code"),
			account,
			[]string{"claude_code"},
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedGlobalAllowedClient, result.Reason)
	})

	t.Run("全局列表含 claude_code + 非签名 → 403", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), account, []string{"claude_code"}, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("全局列表为空 + 账号未配 → 403", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			account,
			nil,
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("账号白名单优先于全局列表（reason=account）", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			account,
			[]string{"claude_code"},
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})
}

func TestOpenAICodexClientRestrictionDetector_Detect_ClientPolicy(t *testing.T) {

	detector := newCodexDetectorFixture(nil)

	t.Run("新字段 any 直接绕过旧字段", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyAny,
				"codex_cli_only":             true,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), account, nil, false)
		require.False(t, result.Enabled)
		require.Equal(t, accountcore.OpenAIOAuthClientPolicyAny, result.Policy)
		require.Equal(t, accountcore.CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("新字段 codex_only 仍按官方客户端判定", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyCodexOnly,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.OpenAIOAuthClientPolicyCodexOnly, result.Policy)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("TLS 路由器策略未绑定路由器时拒绝", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("opencode/1.0", ""), account, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly, result.Policy)
		require.Equal(t, accountcore.CodexClientRestrictionReasonTLSRouterMissing, result.Reason)
	})

	t.Run("TLS 路由器策略命中时放行", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("opencode/1.0", ""),
			account,
			nil,
			(egress.TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略命中时不受伪造 UA 影响", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""),
			account,
			nil,
			(egress.TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略未命中时拒绝", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("curl/8.0", ""),
			account,
			nil,
			(egress.TLSFingerprintRouterMatchResult{RouterID: 9}).Matched,
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedTLSRouter, result.Reason)
	})
}
