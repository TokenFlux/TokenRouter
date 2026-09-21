package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestEffectiveUpstreamUsageConfigDefaultsAndNormalization(t *testing.T) {
	account := &accountcore.Record{Type: capability.AccountTypeAPIKey}
	config, err := accountcore.EffectiveUpstreamUsageConfig(account)
	require.NoError(t, err)
	require.Equal(t, accountcore.UpstreamUsageQueryConfig{Enabled: true, Adapter: accountcore.UpstreamUsageAdapterSub2API}, config)

	extra := map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{
		"enabled":  false,
		"adapter":  accountcore.UpstreamUsageAdapterNewAPI,
		"base_url": "https://usage.example/v1",
	}}
	require.NoError(t, accountcore.NormalizeUpstreamUsageExtra(extra))
	require.Equal(t, map[string]any{
		"enabled":  false,
		"adapter":  accountcore.UpstreamUsageAdapterNewAPI,
		"base_url": "https://usage.example/v1",
	}, extra[accountcore.UpstreamUsageQueryExtraKey])

	bad := map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"api_key": "secret"}}
	require.Error(t, accountcore.NormalizeUpstreamUsageExtra(bad))

	disabledAccount := &accountcore.Record{Extra: map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"enabled": false}}}
	disabled, err := accountcore.EffectiveUpstreamUsageConfig(disabledAccount)
	require.NoError(t, err)
	require.False(t, disabled.Enabled)
	require.Equal(t, accountcore.UpstreamUsageAdapterSub2API, disabled.Adapter)

	unknown := map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{"adapter": "custom-script"}}
	require.ErrorIs(t, accountcore.NormalizeUpstreamUsageExtra(unknown), accountcore.ErrUpstreamUsageConfigInvalid)
	_, err = accountcore.EffectiveUpstreamUsageConfig(&accountcore.Record{Extra: unknown})
	require.ErrorIs(t, err, accountcore.ErrUpstreamUsageUnsupported)

	unsafeURL := map[string]any{accountcore.UpstreamUsageQueryExtraKey: map[string]any{
		"base_url": "https://user:secret@gateway.example/v1?token=secret",
	}}
	require.ErrorIs(t, accountcore.NormalizeUpstreamUsageExtra(unsafeURL), accountcore.ErrUpstreamUsageConfigInvalid)
	_, ok := accountcore.NormalizedUpstreamUsageConfigValue(map[string]any{"api_key": "secret"})
	require.False(t, ok)

	require.Equal(t, []accountcore.UpstreamUsageAdapterOption{
		{Name: accountcore.UpstreamUsageAdapterSub2API, Label: "Sub2API / TokenRouter"},
		{Name: accountcore.UpstreamUsageAdapterNewAPI, Label: "New API"},
		{Name: accountcore.UpstreamUsageAdapterZivv, Label: "Zivv"},
	}, accountcore.UpstreamUsageAdapterOptions())
}
