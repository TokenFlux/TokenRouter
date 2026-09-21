package account

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// ollamaSettingsStore 仅提供配置迁移测试实际使用的单键读写。
type ollamaSettingsStore struct{ values map[string]string }

func (s *ollamaSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *ollamaSettingsStore) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestOllamaCloudUsageSettingsDefaultOffAndValidation(t *testing.T) {
	repo := &ollamaSettingsStore{values: map[string]string{}}
	settingsService := NewRuntimeSettings(repo, errors.New("missing"))
	settings, err := settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 60, settings.IntervalMinutes)
	require.Equal(t, 1, settings.DebounceMinutes)

	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 14, DebounceMinutes: 1})
	require.Error(t, err)
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 61})
	require.Error(t, err)
	// DebounceMinutes=0 表示旧客户端省略字段，写入时应补为默认值 1。
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 0})
	require.NoError(t, err)
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, settings.DebounceMinutes)
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 90, DebounceMinutes: 2})
	require.NoError(t, err)
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 90, settings.IntervalMinutes)
	require.Equal(t, 2, settings.DebounceMinutes)

	// debounce >= interval 会使 min(lastUsed+debounce, fetchedAt+maxWait) 中的
	// debounce 项永远无法生效，相当于静默忽略管理员设置，因此应直接拒绝。
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 15, DebounceMinutes: 15})
	require.Error(t, err, "debounce equal to interval must be rejected")
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 15, DebounceMinutes: 60})
	require.Error(t, err, "debounce greater than interval must be rejected")
	err = settingsService.SetOllamaCloudUsageSettings(context.Background(), &OllamaCloudUsageSettings{Enabled: true, IntervalMinutes: 16, DebounceMinutes: 15})
	require.NoError(t, err, "debounce below interval stays valid")

	// 旧版 JSON 缺少 debounce_minutes 时应默认取 1。
	repo.values[SettingKeyOllamaCloudUsageSettings] = `{"enabled":true,"interval_minutes":45}`
	settings, err = settingsService.GetOllamaCloudUsageSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 45, settings.IntervalMinutes)
	require.Equal(t, 1, settings.DebounceMinutes)
}
