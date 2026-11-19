package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestDecodeConfigNodePreservesMapKeyCaseAndDots decodes Gemini tier quota
// overrides whose map keys contain uppercase letters and literal dots. Direct
// YAML decoding must retain those keys verbatim alongside optional pointer fields.
func TestDecodeConfigNodePreservesMapKeyCaseAndDots(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte(`config_version: 2
provider:
  gemini:
    quota:
      tiers:
        Tier.Pro.V2:
          pro_rpd: 250
          cooldown_minutes: 15
`), "fixture.yaml")
	require.NoError(t, err)

	cfg := defaultConfig()
	present, err := decodeConfigNode(doc, &cfg)
	require.NoError(t, err)
	require.True(t, present["provider.gemini.quota.tiers"])

	tier, ok := cfg.Provider.Gemini.Quota.Tiers["Tier.Pro.V2"]
	require.True(t, ok, "map key casing and dots must survive decoding")
	require.NotNil(t, tier.ProRPD)
	require.EqualValues(t, 250, *tier.ProRPD)
	require.Nil(t, tier.FlashRPD)
	require.NotNil(t, tier.CooldownMinutes)
	require.Equal(t, 15, *tier.CooldownMinutes)
}

// TestDecodeConfigNodeRejectsInvalidTypeWithoutLeakingSecret supplies a secret-like
// non-numeric string to an integer field. Decoding must fail with a path diagnostic
// that never echoes the rejected scalar value.
func TestDecodeConfigNodeRejectsInvalidTypeWithoutLeakingSecret(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("config_version: 2\nserver:\n  port: secret-not-a-port\n"), "fixture.yaml")
	require.NoError(t, err)

	cfg := defaultConfig()
	_, err = decodeConfigNode(doc, &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-not-a-port")
}

// TestDefaultConfigRoundTripsThroughYAMLNode encodes defaultConfig into a YAML
// node tree and decodes it back. Every defaulted field must survive round-trip
// serialization and satisfy checkUnknownKeys, accounting for yaml.v3 encoding
// nil slices and maps as empty sequences and mappings.
func TestDefaultConfigRoundTripsThroughYAMLNode(t *testing.T) {
	defaults := defaultConfig()
	var doc yaml.Node
	require.NoError(t, doc.Encode(defaults))
	require.NoError(t, checkUnknownKeys(&doc))

	var decoded Config
	_, err := decodeConfigNode(&doc, &decoded)
	require.NoError(t, err)

	// yaml.Node.Encode serializes nil Go slices and maps as [] and {}, which
	// decode back as allocated empty collections.
	expected := defaults
	expected.Egress.ProxyProbe.URLs = []ProbeURLConfig{}
	expected.Provider.Gemini.Quota.Tiers = map[string]GeminiTierQuotaConfig{}
	expected.Server.ClientIP.ForwardedHeaders = []string{}
	expected.Server.ClientIP.TrustedProxies = []string{}
	require.Equal(t, expected, decoded)
}
