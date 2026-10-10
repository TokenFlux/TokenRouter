package identity

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
)

// TestReadAdminSettingsStringFallbacks 覆盖数据库空值、配置默认值和内置默认值的优先级。
func TestReadAdminSettingsStringFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		settings     map[string]string
		fallback     string
		wantOIDC     string
		wantDingTalk string
	}{
		{name: "missing", fallback: " configured ", wantOIDC: "configured", wantDingTalk: "configured"},
		{name: "empty", settings: map[string]string{SettingKeyOIDCConnectScopes: "", SettingKeyDingTalkConnectSyncCorpEmailAttrKey: ""}, fallback: " configured ", wantOIDC: "configured", wantDingTalk: "configured"},
		{name: "whitespace", settings: map[string]string{SettingKeyOIDCConnectScopes: " \t", SettingKeyDingTalkConnectSyncCorpEmailAttrKey: " \n"}, fallback: " configured ", wantOIDC: "configured", wantDingTalk: "configured"},
		{name: "override", settings: map[string]string{SettingKeyOIDCConnectScopes: " custom ", SettingKeyDingTalkConnectSyncCorpEmailAttrKey: " custom "}, fallback: " configured ", wantOIDC: "custom", wantDingTalk: "custom"},
		{name: "builtin", fallback: " \t", wantOIDC: "", wantDingTalk: "dingtalk_email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := NewOAuthSettings(nil, &OAuthSettingsDefaults{
				OIDC:     authconfig.OIDCConnectConfig{Scopes: tc.fallback},
				DingTalk: authconfig.DingTalkConnectConfig{SyncCorpEmailAttrKey: tc.fallback},
			}, nil)
			result := source.ReadAdminSettings(tc.settings, func() int { return 7 })
			require.Equal(t, tc.wantOIDC, result.OIDCConnectScopes)
			require.Equal(t, tc.wantDingTalk, result.DingTalkConnectSyncCorpEmailAttrKey)
			require.Equal(t, 7, result.DefaultConcurrency)
		})
	}
}

// TestReadAdminSettingsExplicitEmptyValues 覆盖允许清空的字段和需要区分缺失与零值的设置。
func TestReadAdminSettingsExplicitEmptyValues(t *testing.T) {
	source := NewOAuthSettings(nil, &OAuthSettingsDefaults{
		LinuxDo: authconfig.LinuxDoConnectConfig{ClientID: " padded "},
		OIDC:    authconfig.OIDCConnectConfig{UserInfoEmailPath: "email", Enabled: true, ClockSkewSeconds: 90},
	}, nil)
	result := source.ReadAdminSettings(map[string]string{
		SettingKeyOIDCConnectUserInfoEmailPath: " ",
		SettingKeyOIDCConnectEnabled:           "",
		SettingKeyOIDCConnectClockSkewSeconds:  "0",
		SettingKeyDefaultConcurrency:           "0",
	}, func() int {
		t.Fatal("合法并发设置不应读取默认值")
		return 1
	})
	require.Empty(t, result.OIDCConnectUserInfoEmailPath)
	require.False(t, result.OIDCConnectEnabled)
	require.Zero(t, result.OIDCConnectClockSkewSeconds)
	require.Zero(t, result.DefaultConcurrency)
	require.Equal(t, " padded ", result.LinuxDoConnectClientID)
}
