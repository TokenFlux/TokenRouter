package egress

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamUsageBaseURLReusesSecurityPolicy(t *testing.T) {
	policy := UsageURLPolicy{Configured: true, Enabled: true, UpstreamHosts: []string{"gateway.example"}}

	value, err := policy.Validate("https://gateway.example/v1/")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/v1", value)
	value, err = policy.Validate("HTTPS://gateway.example/v1/")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/v1", value)
	_, err = policy.Validate("http://gateway.example/v1")
	require.Error(t, err)
	_, err = policy.Validate("https://127.0.0.1/v1")
	require.Error(t, err)
	_, err = policy.Validate("https://unlisted.example/v1")
	require.Error(t, err)
}

func TestUpstreamUsageBaseURLUsesStrictDefaultsWithoutConfig(t *testing.T) {
	policy := UsageURLPolicy{}
	_, err := policy.Validate("http://gateway.example/v1")
	require.Error(t, err)
	_, err = policy.Validate("https://127.0.0.1/v1")
	require.Error(t, err)
	value, err := policy.Validate("https://gateway.example/v1")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/v1", value)
}
