package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// TestConfigurationSecretsSurviveOmissionAndNeverBecomePublic exercises encrypted updates, public projections, and a schema secrecy change.
// Omitted secrets survive, explicit empties clear them, and migration cannot expose old plaintext.
func TestConfigurationSecretsSurviveOmissionAndNeverBecomePublic(t *testing.T) {
	cipher, err := crypto.NewAESEncryptor(make([]byte, 32))
	require.NoError(t, err)
	configuration := NewConfiguration(cipher, true)
	snapshot := VersionSnapshot{Manifest: &manifest.Manifest{Config: manifest.Config{Fields: []manifest.Field{
		{Key: "label", Type: "string", Label: manifest.LocalizedText{"en": "Label"}, Default: json.RawMessage(`"default"`)},
		{Key: "opaque", Type: "secret", Label: manifest.LocalizedText{"en": "Secret"}, Required: true},
	}}}}
	require.NoError(t, configuration.Set(&snapshot, nil, false))
	_, err = configuration.Plain(&snapshot)
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.NoError(t, configuration.Set(&snapshot, map[string]any{"opaque": "hidden-value"}, true))
	require.NotContains(t, snapshot.SecretConfig, "hidden-value")
	require.NotContains(t, snapshot.Config, "opaque")
	public, err := configuration.Public(&snapshot)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"set": true}, public["opaque"])
	require.NoError(t, configuration.Set(&snapshot, map[string]any{"label": "updated"}, true))
	plain, err := configuration.Plain(&snapshot)
	require.NoError(t, err)
	require.Equal(t, "hidden-value", plain["opaque"])
	next := VersionSnapshot{Manifest: &manifest.Manifest{Config: manifest.Config{Fields: []manifest.Field{{Key: "opaque", Type: "string", Label: manifest.LocalizedText{"en": "Ordinary"}}}}}}
	require.NoError(t, configuration.Migrate(&snapshot, &next, false))
	require.NotContains(t, next.Config, "opaque")
	require.Empty(t, next.SecretConfig)
	require.NoError(t, configuration.Set(&snapshot, map[string]any{"opaque": ""}, false))
	require.Empty(t, snapshot.SecretConfig)
	public, err = configuration.Public(&snapshot)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"set": false}, public["opaque"])
}

// TestConfigurationRejectsEphemeralKeyAndPreservesSnapshot supplies a valid secret while the host key is not persistent.
// The failed write leaves ordinary settings and ciphertext unchanged and does not echo plaintext in validation errors.
func TestConfigurationRejectsEphemeralKeyAndPreservesSnapshot(t *testing.T) {
	cipher, err := crypto.NewAESEncryptor(make([]byte, 32))
	require.NoError(t, err)
	configuration := NewConfiguration(cipher, false)
	snapshot := VersionSnapshot{
		Config:   map[string]any{"old": "retained"},
		Manifest: &manifest.Manifest{Config: manifest.Config{Fields: []manifest.Field{{Key: "secret", Type: "secret", Label: manifest.LocalizedText{"en": "Secret"}}}}},
	}
	err = configuration.Set(&snapshot, map[string]any{"secret": "do-not-log"}, false)
	require.ErrorIs(t, err, ErrSecretKeyMissing)
	require.Equal(t, map[string]any{"old": "retained"}, snapshot.Config)
	require.Empty(t, snapshot.SecretConfig)
	require.NotContains(t, err.Error(), "do-not-log")
}
