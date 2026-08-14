//go:build unit

package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokAPIKeyURLPolicyFollowsGlobalSecurityConfig(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "http://grok.example.test/v1",
		},
	}

	t.Run("insecure HTTP enabled with allowlist disabled", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true

		responsesURL, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "http://grok.example.test/v1/responses", responsesURL)

		chatURL, err := buildGrokChatCompletionsURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "http://grok.example.test/v1/chat/completions", chatURL)

		mediaURL, err := buildGrokMediaURL(account, cfg, GrokMediaEndpointImagesGenerations, "")
		require.NoError(t, err)
		require.Equal(t, "http://grok.example.test/v1/images/generations", mediaURL)

		contentURL, err := buildGrokMediaURL(account, cfg, GrokMediaEndpointVideoContent, "request 123")
		require.NoError(t, err)
		require.Equal(t, "http://grok.example.test/v1/videos/request%20123/content", contentURL)
	})

	t.Run("insecure HTTP disabled", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = false

		_, err := buildGrokResponsesURL(account, cfg)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	})

	t.Run("enabled allowlist remains HTTPS only", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"grok.example.test"}

		_, err := buildGrokResponsesURL(account, cfg)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	})
}

func TestGrokAPIKeyURLPolicyAppliesAllowlistAndPrivateHostControls(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://grok.example.test/v1",
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"grok.example.test"}

	target, err := buildGrokResponsesURL(account, cfg)
	require.NoError(t, err)
	require.Equal(t, "https://grok.example.test/v1/responses", target)

	cfg.Security.URLAllowlist.UpstreamHosts = []string{"other.example.test"}
	_, err = buildGrokResponsesURL(account, cfg)
	require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")

	account.Credentials["base_url"] = "https://127.0.0.1/v1"
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"127.0.0.1"}
	_, err = buildGrokResponsesURL(account, cfg)
	require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")

	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	target, err = buildGrokResponsesURL(account, cfg)
	require.NoError(t, err)
	require.Equal(t, "https://127.0.0.1/v1/responses", target)
}

func TestGrokAPIKeyURLPolicyRedactsMalformedConfiguredURL(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://%zz:secret@grok.example.test/v1",
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	_, err := buildGrokResponsesURL(account, cfg)
	require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	require.NotContains(t, err.Error(), "secret")
}

func TestGrokOAuthURLPolicy(t *testing.T) {
	t.Run("default CLI gateway always allowed under restrictive allowlist", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"other.example.test"}

		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultCLIBaseURL+"/responses", target)
	})

	t.Run("stored official API endpoint is honored (manual endpoint switch)", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": xai.DefaultBaseURL,
			},
		}
		cfg := &config.Config{}

		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultBaseURL+"/responses", target)
	})

	t.Run("stored regional API endpoint is trusted even under restrictive allowlist", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://us-west-2.api.x.ai/v1",
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"other.example.test"}

		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "https://us-west-2.api.x.ai/v1/responses", target)
	})

	t.Run("custom forwarding address follows operator policy", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://relay.example.test/v1",
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false

		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "https://relay.example.test/v1/responses", target)
	})

	t.Run("custom path prefix is preserved", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://relay.example.test/xai/v1",
			},
		}
		cfg := &config.Config{}

		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "https://relay.example.test/xai/v1/responses", target)
	})

	t.Run("custom forwarding address rejected by allowlist", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://relay.example.test/v1",
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"other.example.test"}

		_, err := buildGrokResponsesURL(account, cfg)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	})

	t.Run("insecure HTTP custom address requires operator opt-in", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "http://relay.example.test/v1",
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = false

		_, err := buildGrokResponsesURL(account, cfg)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")

		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		target, err := buildGrokResponsesURL(account, cfg)
		require.NoError(t, err)
		require.Equal(t, "http://relay.example.test/v1/responses", target)
	})

	t.Run("unsafe override switch does not relax the operator allowlist for custom hosts", func(t *testing.T) {
		// XAI_ALLOW_UNSAFE_URL_OVERRIDES 会放宽受信任主机校验，但自定义 OAuth 转发主机
		// 仍必须受运营方白名单约束，避免 bearer token 被发送到任意主机。
		t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"cli-chat-proxy.grok.com"}

		custom := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "http://10.0.0.1/v1",
			},
		}
		_, err := buildGrokResponsesURL(custom, cfg)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")

		// 即使白名单严格限制自定义主机，官方网关仍应正常解析。
		official := &Account{
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{},
		}
		target, err := buildGrokResponsesURL(official, cfg)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultCLIBaseURL+"/responses", target)
	})
}

func TestBuildGrokBillingURLUsesCLIForOfficialAPIHosts(t *testing.T) {
	for _, baseURL := range []string{xai.DefaultBaseURL, "https://us-west-2.api.x.ai/v1"} {
		account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": baseURL}}

		weekly, err := buildGrokBillingURL(account, &config.Config{}, true)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultCLIBaseURL+xai.BillingWeeklyPath, weekly)
	}
}

func TestBuildGrokBillingURLKeepsCustomRelay(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{
		"base_url": "https://relay.example.test/xai/v1",
	}}

	monthly, err := buildGrokBillingURL(account, &config.Config{}, false)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.test/xai/v1"+xai.BillingMonthlyPath, monthly)
}

func TestGrokBillingURLFollowsAccountBaseURL(t *testing.T) {
	t.Run("oauth default stays on CLI gateway", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{},
		}

		weeklyURL, err := buildGrokBillingURL(account, nil, true)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultCLIBaseURL+"/billing?format=credits", weeklyURL)

		monthlyURL, err := buildGrokBillingURL(account, nil, false)
		require.NoError(t, err)
		require.Equal(t, xai.DefaultCLIBaseURL+"/billing", monthlyURL)
	})

	t.Run("oauth custom forwarding address carries billing probes", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://relay.example.test/v1",
			},
		}

		weeklyURL, err := buildGrokBillingURL(account, nil, true)
		require.NoError(t, err)
		require.Equal(t, "https://relay.example.test/v1/billing?format=credits", weeklyURL)
	})

	t.Run("billing probe honors the operator allowlist like forwarding", func(t *testing.T) {
		// 探测路径必须复用转发 URL 策略，避免被白名单拒绝的自定义主机
		// 通过 billing 探测收到 OAuth bearer token。
		account := &Account{
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"base_url": "https://relay.example.test/v1",
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = true
		cfg.Security.URLAllowlist.UpstreamHosts = []string{"cli-chat-proxy.grok.com"}

		_, err := buildGrokBillingURL(account, cfg, true)
		require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	})
}

func TestGrokOAuthMediaURLFollowsCustomForwardingUpstream(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": "https://custom.example.test/v1",
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	target, err := buildGrokMediaURL(account, cfg, GrokMediaEndpointVideosGenerations, "")
	require.NoError(t, err)
	require.Equal(t, "https://custom.example.test/v1/videos/generations", target)
}
