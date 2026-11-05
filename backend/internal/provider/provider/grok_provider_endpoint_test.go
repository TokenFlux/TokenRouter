package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	acct "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Grok OAuth text defaults and media defaults can resolve to different hosts.
// These cases exercise grok_provider_endpoint.go with empty, official, regional,
// malformed and custom addresses, checking that endpoint selection retains custom
// routing while canonical CLI targets map to the media API.

// TestGetGrokBaseURLUsesSubscriptionProxyForOAuth compares OAuth and API-key
// provider endpoint choices. It checks saved official/custom addresses, malformed
// OAuth fallback, CLI subscription defaults and the public API-key default.
func TestGetGrokBaseURLUsesSubscriptionProxyForOAuth(t *testing.T) {
	tests := []struct {
		name     string      // subtest display label
		provider acct.Record // input provider endpoint fixture
		expected string      // expected selected endpoint address
	}{
		{
			name: "oauth without base_url uses CLI subscription proxy",
			provider: acct.Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth stored official API endpoint is honored (manual endpoint switch)",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored regional API endpoint is honored",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://us-west-2.api.x.ai/v1",
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth stored CLI proxy is honored verbatim",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultCLIBaseURL,
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth unparseable base_url falls back to CLI proxy",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "not a url",
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth explicit custom base_url redirects forwarding traffic",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://custom.example.com/v1",
				},
			},
			expected: "https://custom.example.com/v1",
		},
		{
			name: "oauth custom base_url with path prefix redirects forwarding traffic",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://relay.example.com/xai/v1",
				},
			},
			expected: "https://relay.example.com/xai/v1",
		},
		{
			name: "API key without base_url uses official credit-backed API",
			provider: acct.Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, GrokProviderBaseURL(&tt.provider))
		})
	}
}

// TestGetGrokBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides supplies a custom
// OAuth address with the legacy unsafe-override environment setting enabled.
// It checks endpoint selection returns the same configured custom address.
func TestGetGrokBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides(t *testing.T) {
	xai.ConfigureOverrides(xai.Overrides{AllowUnsafeURLOverrides: true})
	t.Cleanup(func() { xai.ConfigureOverrides(xai.Overrides{}) })
	provider := acct.Record{
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"base_url": "https://custom.example.com/v1",
		},
	}

	require.Equal(t, "https://custom.example.com/v1", GrokProviderBaseURL(&provider))
}

// TestGetGrokMediaBaseURLRedirectsCLIGatewayToOfficialAPI compares OAuth CLI
// spellings with regional, custom and API-key routes. It checks canonical media
// redirection, malformed OAuth fallback and empty results for non-Grok records.
func TestGetGrokMediaBaseURLRedirectsCLIGatewayToOfficialAPI(t *testing.T) {
	tests := []struct {
		name     string      // subtest display label
		provider acct.Record // input provider endpoint fixture
		expected string      // expected selected endpoint address
	}{
		{
			name: "oauth without base_url uses official media API",
			provider: acct.Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy is separated from the media API",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultCLIBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy variant is canonicalized to the media API",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "HTTPS://CLI-CHAT-PROXY.GROK.COM:443/%76%31/",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth unparseable base_url falls back to official media API",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "not a url",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored official API endpoint is honored (manual endpoint switch)",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored regional API endpoint is honored for media",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://us-west-2.api.x.ai/v1",
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth custom base_url redirects media traffic",
			provider: acct.Record{
				Type:     capability.ProviderTypeOAuth,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://custom.example.com/v1",
				},
			},
			expected: "https://custom.example.com/v1",
		},
		{
			name: "API key retains its configured media API",
			provider: acct.Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://grok.example.com/v1",
				},
			},
			expected: "https://grok.example.com/v1",
		},
		{
			name: "non-Grok provider has no media base URL",
			provider: acct.Record{
				Type:        capability.ProviderTypeOAuth,
				Platform:    capability.PlatformOpenAI,
				Credentials: map[string]any{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, GrokProviderMediaBaseURL(&tt.provider))
		})
	}
}

// TestGetGrokMediaBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides supplies a
// custom OAuth media address with the legacy unsafe-override setting enabled.
// It checks the custom endpoint still remains the selected media address.
func TestGetGrokMediaBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides(t *testing.T) {
	xai.ConfigureOverrides(xai.Overrides{AllowUnsafeURLOverrides: true})
	t.Cleanup(func() { xai.ConfigureOverrides(xai.Overrides{}) })
	provider := acct.Record{
		Type:     capability.ProviderTypeOAuth,
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"base_url": "https://custom.example.com/v1",
		},
	}

	require.Equal(t, "https://custom.example.com/v1", GrokProviderMediaBaseURL(&provider))
}
