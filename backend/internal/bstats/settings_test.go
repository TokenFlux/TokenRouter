package bstats

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// settings_test.go protects official defaults and the writable field boundary.
// It exercises partial participant input independently of HTTP so omission,
// explicit false, and internal identity keys cannot accidentally share handling.

// TestSettingsDefaultsAndPartialUpdate reads an unconfigured installation and
// prepares one explicit opt-out. Logging remains disabled by default and the
// participant cannot rewrite either omitted controls or the internal UUID.
func TestSettingsDefaultsAndPartialUpdate(t *testing.T) {
	require.Equal(t, Config{Enabled: true}, ReadSettings(nil))
	require.Equal(t, Config{}, ReadSettings(map[string]string{SettingKeyEnabled: "false"}))
	require.Equal(t, Config{}, ReadSettings(map[string]string{SettingKeyEnabled: "invalid"}))
	participant := SettingsParticipant()
	change, err := participant.Prepare(context.Background(), settings.Fields{
		SettingKeyEnabled:    json.RawMessage("false"),
		SettingKeyServerUUID: json.RawMessage(`"forged"`),
	}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]string{SettingKeyEnabled: "false"}, change.Values)
	require.NotContains(t, participant.Fields, SettingKeyServerUUID)
	_, err = participant.Prepare(context.Background(), settings.Fields{SettingKeyEnabled: json.RawMessage(`"false"`)}, nil)
	require.Error(t, err)
	config := Config{Enabled: true, LogErrors: true, LogSentData: true, LogResponseStatusText: true}
	require.Equal(t, config, ReadSettings(config.Values()))
}
