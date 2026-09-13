package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 配置替换基于锁内最新累计，仍按原取时点处理真正跨越的资金窗口。
func TestConfigurationUsesCurrentWindowAndIsolatedValues(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, currentDay := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous_window", true: "current_window"}[currentDay], func(t *testing.T) {
			start := now.Add(-24 * time.Hour)
			if currentDay {
				start = now.Add(-time.Hour)
			}
			current := &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, LoadLocation: time.LoadLocation,
				Credentials: map[string]any{"api_key": "latest-secret"},
				Extra:       map[string]any{"quota_used": 7.0, "quota_daily_used": 5.0, "quota_daily_start": start.Format(time.RFC3339)}}
			note := "new-note"
			desired := CloneRecord(current)
			desired.Notes = &note
			desired.Extra = map[string]any{"quota_daily_limit": 10.0, "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 0.0}
			out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigNotes | ConfigExtra, ComputeResetAt: &now, NormalizeWindowAt: &now})
			require.NoError(t, err)
			require.Equal(t, 7.0, out.Extra["quota_used"])
			if currentDay {
				require.Equal(t, 5.0, out.Extra["quota_daily_used"])
			} else {
				require.Equal(t, 0.0, out.Extra["quota_daily_used"])
			}
			*out.Notes = "changed-copy"
			out.Credentials["api_key"] = "changed-copy"
			require.Equal(t, "new-note", note)
			require.Equal(t, "latest-secret", current.Credentials["api_key"])
			require.Equal(t, 5.0, current.Extra["quota_daily_used"])
		})
	}
}

// 未提供的秘密继承最新值，显式清空和旋转则保持管理员意图。
func TestConfigurationSensitiveCredentialIntent(t *testing.T) {
	for _, mode := range []string{"omitted", "clear", "rotate"} {
		t.Run(mode, func(t *testing.T) {
			current := &Record{Credentials: map[string]any{"api_key": "latest-secret"}}
			desired := &Record{Credentials: map[string]any{"api_key": "stale-secret", "model_mapping": map[string]any{"alias": "model"}}}
			input := map[string]any{}
			expected := "latest-secret"
			switch mode {
			case "clear":
				expected = ""
				input["api_key"] = expected
				desired.Credentials["api_key"] = expected
			case "rotate":
				expected = "explicit-secret"
				input["api_key"] = expected
				desired.Credentials["api_key"] = expected
			}
			out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigCredentials, PreserveSensitive: true, CredentialInput: input})
			require.NoError(t, err)
			require.Equal(t, expected, out.Credentials["api_key"])
			require.Equal(t, "latest-secret", current.Credentials["api_key"])
		})
	}
}

// 最新秘密的继承不能撤销编辑入口已经完成的临时认证材料清理。
func TestConfigurationDoesNotRestoreTemporaryCredentials(t *testing.T) {
	current := &Record{Platform: PlatformGrok, Credentials: map[string]any{
		"access_token": "current-token", "password": "old-password", "sso_token": "old-sso", "cookie": "old-cookie"}}
	desired := &Record{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "model"}}}
	out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigCredentials, PreserveSensitive: true})
	require.NoError(t, err)
	require.Equal(t, "current-token", out.Credentials["access_token"])
	for _, key := range []string{"password", "sso_token", "cookie"} {
		require.NotContains(t, out.Credentials, key)
		require.Contains(t, current.Credentials, key)
	}
}
