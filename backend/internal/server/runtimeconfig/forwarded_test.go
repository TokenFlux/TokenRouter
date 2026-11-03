package runtimeconfig

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

// forwarded_test.go verifies startup selection and persistence of client-IP trust.
// In-memory settings distinguish new installations, retained legacy defaults,
// and explicit operator choices so migration markers cannot override a saved
// preference during a later startup.

// forwardedRepoStub keeps settings in memory for loader tests so a test can
// assert which values reached persistence.
type forwardedRepoStub struct {
	values map[string]string // stored setting values by key
}

// GetMultiple returns stored values among the requested keys and omits the rest.
func (r *forwardedRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	found := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			found[key] = value
		}
	}
	return found, nil
}

// SetMultiple merges the supplied values into the stored settings map.
func (r *forwardedRepoStub) SetMultiple(_ context.Context, updates map[string]string) error {
	maps.Copy(r.values, updates)
	return nil
}

// TestLoadForwardedClientIPSettingsSelectsUpgradeMode runs startup loading across
// new and existing installations. Saved choices take precedence, while an existing
// installation without an operator choice retains its earlier enabled default.
func TestLoadForwardedClientIPSettingsSelectsUpgradeMode(t *testing.T) {
	existing := map[string]string{SettingKeyForwardedClientIPModeV2: "true"}
	cases := []struct {
		name          string                   // scenario display name
		stored        map[string]string        // persisted settings before loading
		options       ForwardedSettingsOptions // supplied startup configuration
		wantEnabled   bool                     // expected runtime trust
		wantPersisted string                   // expected stored trust text
	}{
		{name: "new installation stays off", stored: map[string]string{}},
		{name: "existing installation keeps earlier default", stored: existing, wantEnabled: true, wantPersisted: "true"},
		{name: "explicit true stays enabled", stored: existing, options: ForwardedSettingsOptions{InitialTrust: true, InitialTrustConfigured: true}, wantEnabled: true},
		{name: "existing installation with explicit switch", stored: existing, options: ForwardedSettingsOptions{InitialTrustConfigured: true}},
		{name: "existing installation with proxy policy", stored: existing, options: ForwardedSettingsOptions{TrustedProxiesConfigured: true}},
		{
			name:          "stored false is never raised",
			stored:        map[string]string{SettingKeyAPIKeyACLTrustForwardedIP: "false"},
			wantPersisted: "false",
		},
		{
			name:          "stored true stays enabled",
			stored:        map[string]string{SettingKeyForwardedClientIPModeV2: "true", SettingKeyAPIKeyACLTrustForwardedIP: "true"},
			wantEnabled:   true,
			wantPersisted: "true",
		},
		{
			name: "completed upgrade is not repeated",
			stored: map[string]string{
				SettingKeyForwardedClientIPModeV2:     "true",
				SettingKeyForwardedClientIPDefaultOff: "true",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &forwardedRepoStub{values: make(map[string]string)}
			maps.Copy(repo.values, testCase.stored)
			published := true
			options := testCase.options
			options.Headers = func() []string { return []string{} }
			options.Publish = func(enabled bool, _ []string) { published = enabled }

			err := NewForwardedSettings(repo, options).LoadForwardedClientIPSettings(context.Background())

			require.NoError(t, err)
			require.Equal(t, testCase.wantEnabled, published)
			require.Equal(t, testCase.wantPersisted, repo.values[SettingKeyAPIKeyACLTrustForwardedIP])
			require.Equal(t, "true", repo.values[SettingKeyForwardedClientIPDefaultOff])
		})
	}
}
