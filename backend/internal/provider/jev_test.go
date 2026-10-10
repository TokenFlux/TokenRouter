package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestJevCredentialsAndProtocols 覆盖统一保存校验、默认协议和关闭协议。
func TestJevCredentialsAndProtocols(t *testing.T) {
	value := &Record{Platform: PlatformJev, Type: ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test"}}
	require.NoError(t, NormalizeProviderProtocols(value))
	require.Equal(t, []capability.ProtocolID{capability.ProtocolSystemOne}, value.UpstreamProtocols())
	require.Equal(t, DefaultJevBaseURL, value.GetJevBaseURL())
	require.True(t, value.IsHeaderOverrideEligible())
	config, err := EffectiveUpstreamUsageConfig(value)
	require.NoError(t, err)
	require.False(t, config.Enabled)
	value.Extra = map[string]any{UpstreamUsageQueryExtraKey: map[string]any{"enabled": true, "adapter": "sub2api"}}
	config, err = EffectiveUpstreamUsageConfig(value)
	require.NoError(t, err)
	require.True(t, config.Enabled)
	value.Credentials[UpstreamProtocolsKey] = []string{}
	require.NoError(t, NormalizeProviderProtocols(value))
	require.Empty(t, value.UpstreamProtocols())
	value.Credentials[UpstreamProtocolsKey] = []string{"openai_responses"}
	require.Error(t, NormalizeProviderProtocols(value))
	value.Type = ProviderTypeOAuth
	value.Credentials[UpstreamProtocolsKey] = []string{}
	require.Error(t, NormalizeProviderProtocols(value))
	value.Type = ProviderTypeAPIKey
	value.Credentials["api_key"] = ""
	require.Error(t, NormalizeProviderProtocols(value))
}
