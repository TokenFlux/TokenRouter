package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// newCodexDetectorTestContext returns a lazy reader for fixed user-agent and
// originator test headers. Empty inputs leave their headers absent; subsequent
// reads return the same fixture strings for detector predicates.
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

// newCodexDetectorFixture binds the actual OpenAI identity predicates for tests.
// A nil configuration disables ForceCLI; other configurations copy its current
// flag into detector options for the fixture's lifetime.
func newCodexDetectorFixture(cfg *config.Config) *CodexClientDetector {
	return &CodexClientDetector{Options: CodexClientOptions{ForceCLI: cfg != nil && cfg.Gateway.OpenAI.Codex.ForceCLI, OfficialUserAgent: openai.IsCodexOfficialClientRequestStrict, OfficialOriginator: openai.IsCodexOfficialClientOriginator, AllowedClients: openai.MatchAllowedClients}}
}

// TestOpenAICodexClientRestrictionDetector_Detect exercises disabled enforcement,
// recognized header formats, unrecognized spellings, and the force override.
// It checks the enforcement flag, match result, and reason chosen for each fixture.
func TestOpenAICodexClientRestrictionDetector_Detect(t *testing.T) {
	t.Run("未开启开关时绕过", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Extra: map[string]any{}}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), provider, nil, false)
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("开启后 codex_cli_rs 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_cli_rs/0.99.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex-tui 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex-tui/0.125.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_vscode/1.0.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_vscode_copilot 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_vscode_copilot/1.0.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 codex_app 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("codex_app/2.1.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 UA 尾部官方客户端命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		ua := "cccc/0.1.0 (Ubuntu 22.04; x86_64) xterm-256color (codex-tui; 0.125.0)"
		result := detector.DetectClient(newCodexDetectorTestContext(ua, ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedUA, result.Reason)
	})

	t.Run("开启后 originator 命中", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "codex_chatgpt_desktop"), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedOriginator, result.Reason)
	})

	t.Run("开启后伪造复合 UA 拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后伪造 originator 拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_codex_thing"), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启后非官方客户端拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("开启 ForceCodexCLI 时允许通过", func(t *testing.T) {
		detector := newCodexDetectorFixture(&config.Config{Gateway: config.GatewayConfig{OpenAI: config.GatewayOpenAIConfig{Codex: config.CodexConfig{ForceCLI: true}}}})
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonForceCodexCLI, result.Reason)
	})
}

// TestOpenAICodexClientRestrictionDetector_Detect_AllowedClients combines named
// provider and global exceptions with matching and mismatched header pairs.
// It checks their acceptance, disabled-policy behavior, and provider reason
// precedence when both exception lists match.
func TestOpenAICodexClientRestrictionDetector_Detect_AllowedClients(t *testing.T) {
	const (
		claudeCodeUA         = "Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)"
		claudeCodeOriginator = "Claude Code"
	)

	t.Run("配置 claude_code 白名单且命中真实签名时放行", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), provider, nil, false)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})

	t.Run("配置白名单但伪造 originator 仍拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, "my_client"), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未配置白名单时 Claude Code 签名仍拒绝", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("未开启 codex_cli_only 时白名单不参与，直接绕过", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}

		result := detector.DetectClient(newCodexDetectorTestContext(claudeCodeUA, claudeCodeOriginator), provider, nil, false)
		require.False(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("全局列表含 claude_code + 命中签名 → 放行(global)", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)", "Claude Code"),
			provider,
			[]string{"claude_code"},
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedGlobalAllowedClient, result.Reason)
	})

	t.Run("全局列表含 claude_code + 非签名 → 403", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", "my_client"), provider, []string{"claude_code"}, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("全局列表为空 + 提供商未配 → 403", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"codex_cli_only": true},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			provider,
			nil,
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("提供商白名单优先于全局列表（reason=provider）", func(t *testing.T) {
		detector := newCodexDetectorFixture(nil)
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"codex_cli_only":                 true,
				"codex_cli_only_allowed_clients": []any{"claude_code"},
			},
		}
		result := detector.DetectClient(
			newCodexDetectorTestContext("Claude Code/0.5.0 (Macos) (Claude Code; 1.0.4)", "Claude Code"),
			provider,
			[]string{"claude_code"},
			(egress.TLSFingerprintRouterMatchResult{}).Matched,
		)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedAllowedClient, result.Reason)
	})
}

// TestOpenAICodexClientRestrictionDetector_Detect_ClientPolicy supplies explicit
// unrestricted, Codex, and TLS-router policies alongside legacy or header inputs.
// It checks policy precedence and router presence/match decisions, including
// acceptance independent of the supplied user agent under TLS policy.
func TestOpenAICodexClientRestrictionDetector_Detect_ClientPolicy(t *testing.T) {
	detector := newCodexDetectorFixture(nil)

	t.Run("新字段 any 直接绕过旧字段", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyAny,
				"codex_cli_only":             true,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), provider, nil, false)
		require.False(t, result.Enabled)
		require.Equal(t, OpenAIOAuthClientPolicyAny, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonDisabled, result.Reason)
	})

	t.Run("新字段 codex_only 仍按官方客户端判定", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyCodexOnly,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("curl/8.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, OpenAIOAuthClientPolicyCodexOnly, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, result.Reason)
	})

	t.Run("TLS 路由器策略未绑定路由器时拒绝", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			},
		}

		result := detector.DetectClient(newCodexDetectorTestContext("opencode/1.0", ""), provider, nil, false)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, OpenAIOAuthClientPolicyTLSRouterMatchedOnly, result.Policy)
		require.Equal(t, CodexClientRestrictionReasonTLSRouterMissing, result.Reason)
	})

	t.Run("TLS 路由器策略命中时放行", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("opencode/1.0", ""),
			provider,
			nil,
			(egress.TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略命中时不受伪造 UA 影响", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("Mozilla/5.0 codex_cli_rs/0.1.0", ""),
			provider,
			nil,
			(egress.TLSFingerprintRouterMatchResult{Matched: true, RouterID: 9, TLSFingerprintProfileID: 2}).Matched,
		)
		require.True(t, result.Enabled)
		require.True(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonMatchedTLSRouter, result.Reason)
	})

	t.Run("TLS 路由器策略未命中时拒绝", func(t *testing.T) {
		provider := &Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
				"tls_fingerprint_router_id":  int64(9),
			},
		}

		result := detector.DetectClient(
			newCodexDetectorTestContext("curl/8.0", ""),
			provider,
			nil,
			(egress.TLSFingerprintRouterMatchResult{RouterID: 9}).Matched,
		)
		require.True(t, result.Enabled)
		require.False(t, result.Matched)
		require.Equal(t, CodexClientRestrictionReasonNotMatchedTLSRouter, result.Reason)
	})
}
