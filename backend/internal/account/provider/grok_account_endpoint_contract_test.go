//go:build unit

package provider

import (
	acct "github.com/TokenFlux/TokenRouter/internal/account"

	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestGetGrokBaseURLUsesSubscriptionProxyForOAuth(t *testing.T) {
	tests := []struct {
		name     string
		account  acct.Record
		expected string
	}{
		{
			name: "oauth without base_url uses CLI subscription proxy",
			account: acct.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth stored official API endpoint is honored (manual endpoint switch)",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored regional API endpoint is honored",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://us-west-2.api.x.ai/v1",
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth stored CLI proxy is honored verbatim",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultCLIBaseURL,
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth unparseable base_url falls back to CLI proxy",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "not a url",
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth explicit custom base_url redirects forwarding traffic",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://custom.example.com/v1",
				},
			},
			expected: "https://custom.example.com/v1",
		},
		{
			name: "oauth custom base_url with path prefix redirects forwarding traffic",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://relay.example.com/xai/v1",
				},
			},
			expected: "https://relay.example.com/xai/v1",
		},
		{
			name: "API key without base_url uses official credit-backed API",
			account: acct.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, GrokAccountBaseURL(&tt.account))
		})
	}
}

func TestGetGrokBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	account := acct.Record{
		Type:     capability.AccountTypeOAuth,
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"base_url": "https://custom.example.com/v1",
		},
	}

	require.Equal(t, "https://custom.example.com/v1", GrokAccountBaseURL(&account))
}

func TestGetGrokMediaBaseURLRedirectsCLIGatewayToOfficialAPI(t *testing.T) {
	tests := []struct {
		name     string
		account  acct.Record
		expected string
	}{
		{
			name: "oauth without base_url uses official media API",
			account: acct.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy is separated from the media API",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultCLIBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy variant is canonicalized to the media API",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "HTTPS://CLI-CHAT-PROXY.GROK.COM:443/%76%31/",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth unparseable base_url falls back to official media API",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "not a url",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored official API endpoint is honored (manual endpoint switch)",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored regional API endpoint is honored for media",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://us-west-2.api.x.ai/v1",
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth custom base_url redirects media traffic",
			account: acct.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://custom.example.com/v1",
				},
			},
			expected: "https://custom.example.com/v1",
		},
		{
			name: "API key retains its configured media API",
			account: acct.Record{
				Type:     capability.AccountTypeAPIKey,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://grok.example.com/v1",
				},
			},
			expected: "https://grok.example.com/v1",
		},
		{
			name: "non-Grok account has no media base URL",
			account: acct.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformOpenAI,
				Credentials: map[string]any{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, GrokAccountMediaBaseURL(&tt.account))
		})
	}
}

func TestGetGrokMediaBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	account := acct.Record{
		Type:     capability.AccountTypeOAuth,
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"base_url": "https://custom.example.com/v1",
		},
	}

	require.Equal(t, "https://custom.example.com/v1", GrokAccountMediaBaseURL(&account))
}
