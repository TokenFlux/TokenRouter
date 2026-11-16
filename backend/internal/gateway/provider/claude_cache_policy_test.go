package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// claude_cache_policy_test.go verifies request-context helpers and model
// eligibility rules for per-Key forced one-hour Claude prompt caching. The
// tests cover Anthropic, OpenCode, and OpenAI provider platforms alongside
// mapped passback families so non-Claude destinations never receive Claude TTL
// rewrites.

// TestAPIKeyForceClaude1hCacheContextHelpers reads and derives the per-Key
// Claude one-hour cache flag across nil, empty, enabled, and disabled contexts.
// It checks nil inputs default safely to false and derived contexts isolate
// their boolean values.
func TestAPIKeyForceClaude1hCacheContextHelpers(t *testing.T) {
	var nilCtx context.Context
	require.False(t, APIKeyForceClaude1hCache(nilCtx))
	require.False(t, APIKeyForceClaude1hCache(context.Background()))

	enabledFromNil := WithAPIKeyForceClaude1hCache(nilCtx, true)
	require.True(t, APIKeyForceClaude1hCache(enabledFromNil))

	disabled := WithAPIKeyForceClaude1hCache(enabledFromNil, false)
	require.True(t, APIKeyForceClaude1hCache(enabledFromNil))
	require.False(t, APIKeyForceClaude1hCache(disabled))
}

// TestShouldForceClaude1hCache evaluates platform, credential type, and mapped
// model precedence when forced one-hour Claude caching is enabled or disabled.
// It checks Claude targets qualify across Anthropic, OpenCode, and OpenAI
// relays while passback families and non-Claude platforms remain excluded.
func TestShouldForceClaude1hCache(t *testing.T) {
	newTarget := func(platform, providerType string) *ExecutionProvider {
		return &ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation,
				Platform:     platform,
				Type:         providerType,
			},
		}
	}

	enabledCtx := WithAPIKeyForceClaude1hCache(context.Background(), true)
	disabledCtx := WithAPIKeyForceClaude1hCache(context.Background(), false)

	require.False(t, ShouldForceClaude1hCache(disabledCtx, newTarget(capability.PlatformAnthropic, capability.ProviderTypeOAuth), "claude-sonnet-4-6"))
	require.False(t, ShouldForceClaude1hCache(enabledCtx, nil, "claude-sonnet-4-6"))

	tests := []struct {
		name         string   // cache eligibility scenario label
		platform     string   // selected provider platform
		providerType string   // selected provider credential kind
		models       []string // ordered mapped-then-client model candidates
		want         bool     // expected forced 1h cache decision
	}{
		{
			name:         "anthropic oauth with claude model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeOAuth,
			models:       []string{"claude-sonnet-4-6"},
			want:         true,
		},
		{
			name:         "anthropic oauth without model candidate",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeOAuth,
			want:         true,
		},
		{
			name:         "anthropic setup token",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeSetupToken,
			models:       []string{"claude-haiku-4-5"},
			want:         true,
		},
		{
			name:         "anthropic apikey with claude model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"claude-opus-4-6"},
			want:         true,
		},
		{
			name:         "anthropic bedrock with region model id",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeBedrock,
			models:       []string{"us.anthropic.claude-opus-4-6-v1"},
			want:         true,
		},
		{
			name:         "anthropic vertex service account",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeServiceAccount,
			models:       []string{"claude-sonnet-4-6@20260218"},
			want:         true,
		},
		{
			name:         "anthropic apikey mapped from claude alias to glm",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"glm-5", "claude-sonnet-4-6"},
			want:         false,
		},
		{
			name:         "anthropic apikey with kimi passback model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"kimi-k2.5"},
			want:         false,
		},
		{
			name:         "anthropic apikey with deepseek reasoner",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"deepseek-reasoner"},
			want:         false,
		},
		{
			name:         "anthropic apikey with minimax passback model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"minimax-m2.5"},
			want:         false,
		},
		{
			name:         "anthropic apikey with qwen thinking model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"qwen3-max-thinking"},
			want:         false,
		},
		{
			name:         "anthropic apikey with non-claude unknown model",
			platform:     capability.PlatformAnthropic,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"gpt-oss:20b"},
			want:         false,
		},
		{
			name:         "opencode with claude model",
			platform:     capability.PlatformOpenCode,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"claude-sonnet-4-6"},
			want:         true,
		},
		{
			name:         "opencode with glm model",
			platform:     capability.PlatformOpenCode,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"glm-5"},
			want:         false,
		},
		{
			name:         "openai compatible relay with prefixed claude model",
			platform:     capability.PlatformOpenAI,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"anthropic/claude-sonnet-4.6"},
			want:         true,
		},
		{
			name:         "openai with gpt model",
			platform:     capability.PlatformOpenAI,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"gpt-5.4"},
			want:         false,
		},
		{
			name:         "kimi platform excluded even with claude alias",
			platform:     capability.PlatformKimi,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"claude-sonnet-4-6"},
			want:         false,
		},
		{
			name:         "zhipu platform excluded",
			platform:     capability.PlatformZhipu,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"claude-sonnet-4-6"},
			want:         false,
		},
		{
			name:         "deepseek platform excluded",
			platform:     capability.PlatformDeepseek,
			providerType: capability.ProviderTypeAPIKey,
			models:       []string{"claude-sonnet-4-6"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldForceClaude1hCache(enabledCtx, newTarget(tt.platform, tt.providerType), tt.models...)
			require.Equal(t, tt.want, got)
		})
	}
}
