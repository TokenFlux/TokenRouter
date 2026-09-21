//go:build unit

package account_test

import (
	"context"
	"encoding/json"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestGetOverloadCooldownSettings_DefaultsWhenNotSet(t *testing.T) {
	repo := newCooldownSettingsStore()
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 10, settings.CooldownMinutes)
}

func TestGetOverloadCooldownSettings_ReadsFromDB(t *testing.T) {
	repo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.OverloadCooldownSettings{Enabled: false, CooldownMinutes: 30})
	require.NoError(t, err)
	repo.data[accountcore.SettingKeyOverloadCooldownSettings] = string(data)
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 30, settings.CooldownMinutes)
}

func TestGetOverloadCooldownSettings_ClampsMinValue(t *testing.T) {
	repo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.OverloadCooldownSettings{Enabled: true, CooldownMinutes: 0})
	require.NoError(t, err)
	repo.data[accountcore.SettingKeyOverloadCooldownSettings] = string(data)
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, settings.CooldownMinutes)
}

func TestGetOverloadCooldownSettings_ClampsMaxValue(t *testing.T) {
	repo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.OverloadCooldownSettings{Enabled: true, CooldownMinutes: 999})
	require.NoError(t, err)
	repo.data[accountcore.SettingKeyOverloadCooldownSettings] = string(data)
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 120, settings.CooldownMinutes)
}

func TestGetOverloadCooldownSettings_InvalidJSON_ReturnsDefaults(t *testing.T) {
	repo := newCooldownSettingsStore()
	repo.data[accountcore.SettingKeyOverloadCooldownSettings] = "not-json"
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 10, settings.CooldownMinutes)
}

func TestGetOverloadCooldownSettings_EmptyValue_ReturnsDefaults(t *testing.T) {
	repo := newCooldownSettingsStore()
	repo.data[accountcore.SettingKeyOverloadCooldownSettings] = ""
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 10, settings.CooldownMinutes)
}

func TestSetOverloadCooldownSettings_Success(t *testing.T) {
	repo := newCooldownSettingsStore()
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	err := svc.SetOverloadCooldownSettings(context.Background(), &accountcore.OverloadCooldownSettings{
		Enabled:         false,
		CooldownMinutes: 25,
	})
	require.NoError(t, err)

	// Verify round-trip
	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 25, settings.CooldownMinutes)
}

func TestSetOverloadCooldownSettings_RejectsNil(t *testing.T) {
	svc := accountcore.NewRuntimeSettings(newCooldownSettingsStore(), errCooldownSettingMissing)
	err := svc.SetOverloadCooldownSettings(context.Background(), nil)
	require.Error(t, err)
}

func TestSetOverloadCooldownSettings_EnabledRejectsOutOfRange(t *testing.T) {
	svc := accountcore.NewRuntimeSettings(newCooldownSettingsStore(), errCooldownSettingMissing)

	for _, minutes := range []int{0, -1, 121, 999} {
		err := svc.SetOverloadCooldownSettings(context.Background(), &accountcore.OverloadCooldownSettings{
			Enabled: true, CooldownMinutes: minutes,
		})
		require.Error(t, err, "should reject enabled=true + cooldown_minutes=%d", minutes)
		require.Contains(t, err.Error(), "cooldown_minutes must be between 1-120")
	}
}

func TestSetOverloadCooldownSettings_DisabledNormalizesOutOfRange(t *testing.T) {
	repo := newCooldownSettingsStore()
	svc := accountcore.NewRuntimeSettings(repo, errCooldownSettingMissing)

	// enabled=false + cooldown_minutes=0 应该保存成功，值被归一化为10
	err := svc.SetOverloadCooldownSettings(context.Background(), &accountcore.OverloadCooldownSettings{
		Enabled: false, CooldownMinutes: 0,
	})
	require.NoError(t, err, "disabled with invalid minutes should NOT be rejected")

	// 验证持久化后读回来的值
	settings, err := svc.GetOverloadCooldownSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 10, settings.CooldownMinutes, "should be normalized to default")
}

func TestSetOverloadCooldownSettings_AcceptsBoundaries(t *testing.T) {
	svc := accountcore.NewRuntimeSettings(newCooldownSettingsStore(), errCooldownSettingMissing)

	for _, minutes := range []int{1, 60, 120} {
		err := svc.SetOverloadCooldownSettings(context.Background(), &accountcore.OverloadCooldownSettings{
			Enabled: true, CooldownMinutes: minutes,
		})
		require.NoError(t, err, "should accept cooldown_minutes=%d", minutes)
	}
}

func TestDefaultOverloadCooldownSettings(t *testing.T) {
	d := accountcore.DefaultOverloadCooldownSettings()
	require.True(t, d.Enabled)
	require.Equal(t, 10, d.CooldownMinutes)
}

func TestOverloadCooldownSettings_JSONRoundTrip(t *testing.T) {
	original := accountcore.OverloadCooldownSettings{Enabled: false, CooldownMinutes: 42}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded accountcore.OverloadCooldownSettings
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, original, decoded)

	// Verify JSON uses snake_case field names
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasEnabled := raw["enabled"]
	_, hasCooldown := raw["cooldown_minutes"]
	require.True(t, hasEnabled, "JSON must use 'enabled'")
	require.True(t, hasCooldown, "JSON must use 'cooldown_minutes'")
}
