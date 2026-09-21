//go:build unit

package settings_test

import (
	"context"
	"testing"

	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/stretchr/testify/require"
)

func newSettingServiceForPlatformThresholdTest(seed map[string]string) *settingskit.Composite {
	svc, _ := newSettingServiceAndRepoForPlatformThresholdTest(seed)
	return svc
}
func newSettingServiceAndRepoForPlatformThresholdTest(seed map[string]string) (*settingskit.Composite, *mockSettingRepo) {

	repo := newMockSettingRepo()
	for k, v := range seed {
		repo.data[k] = v
	}
	return settingskit.NewComposite(repo, &config.Config{}), repo
}

func TestPlatformSchedulingThresholds_RoundTrip_DefaultsAndStoredValues(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(nil)

	got := composite.Parse(map[string]string{}, svc.Read)
	require.Equal(t, map[string]int{
		capability.PlatformOpenAI:    100,
		capability.PlatformAnthropic: 100,
		capability.PlatformGrok:      100,
	}, got.AccountSchedulingThresholds)

	got = composite.Parse(map[string]string{account.SettingKeyAccountSchedulingThresholds: `{"openai":91,"grok":77,"gemini":85,"kiro":99}`}, svc.Read)
	require.Equal(t, 91, got.AccountSchedulingThresholds[capability.PlatformOpenAI])
	require.Equal(t, 100, got.AccountSchedulingThresholds[capability.PlatformAnthropic])
	require.Equal(t, 77, got.AccountSchedulingThresholds[capability.PlatformGrok])
	require.NotContains(t, got.AccountSchedulingThresholds, capability.PlatformGemini)
	require.NotContains(t, got.AccountSchedulingThresholds, "kiro")
}

func TestBuildSystemSettingsUpdates_PersistsAccountSchedulingThresholds(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(nil)

	updates, err := composite.Prepare(context.Background(), &composite.Snapshot{
		AccountSchedulingThresholds: map[string]int{
			capability.PlatformOpenAI:    91,
			capability.PlatformAnthropic: 88,
			capability.PlatformGrok:      77,
		},
	}, svc.Prepare)
	require.NoError(t, err)
	require.JSONEq(t, `{"openai":91,"anthropic":88,"grok":77}`, updates[account.SettingKeyAccountSchedulingThresholds])
}

func TestValidateAndNormalizeAccountSchedulingThresholds_FillsMissingPlatforms(t *testing.T) {
	normalized, err := account.ValidateAndNormalizeAccountSchedulingThresholds(map[string]int{
		capability.PlatformOpenAI: 91,
	})
	require.NoError(t, err)
	require.Equal(t, 91, normalized[capability.PlatformOpenAI])
	require.Equal(t, 100, normalized[capability.PlatformAnthropic])
	require.Equal(t, 100, normalized[capability.PlatformGrok])
	require.NotContains(t, normalized, capability.PlatformGemini)
	require.NotContains(t, normalized, "kiro")
	require.NotContains(t, normalized, capability.PlatformAntigravity)
}

func TestValidateAndNormalizeAccountSchedulingThresholds_RejectsUnsupportedPlatforms(t *testing.T) {
	_, err := account.ValidateAndNormalizeAccountSchedulingThresholds(map[string]int{
		capability.PlatformGemini: 85,
	})
	require.Error(t, err)
}

func TestUpdateSettings_StoresAccountSchedulingThresholds(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(nil)

	err := svc.Save(context.Background(), &composite.Snapshot{
		AccountSchedulingThresholds: map[string]int{
			capability.PlatformOpenAI:    92,
			capability.PlatformAnthropic: 89,
			capability.PlatformGrok:      76,
		},
	})
	require.NoError(t, err)

	stored, err := svc.Store.GetValue(context.Background(), account.SettingKeyAccountSchedulingThresholds)
	require.NoError(t, err)
	got := composite.Parse(map[string]string{account.SettingKeyAccountSchedulingThresholds: stored}, svc.Read)
	require.Equal(t, 92, got.AccountSchedulingThresholds[capability.PlatformOpenAI])
	require.Equal(t, 89, got.AccountSchedulingThresholds[capability.PlatformAnthropic])
	require.Equal(t, 76, got.AccountSchedulingThresholds[capability.PlatformGrok])
	require.NotContains(t, got.AccountSchedulingThresholds, "kiro")
}

func TestGetAccountSchedulingThresholds_ReadsStoredValue(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(map[string]string{account.SettingKeyAccountSchedulingThresholds: `{"openai":93,"grok":88,"kiro":87}`})

	got := svc.Account.GetAccountSchedulingThresholds(context.Background())

	require.Equal(t, 93, got[capability.PlatformOpenAI])
	require.Equal(t, 100, got[capability.PlatformAnthropic])
	require.Equal(t, 88, got[capability.PlatformGrok])
	require.NotContains(t, got, "kiro")
}

func TestUpdateSettings_OmittedAccountSchedulingThresholdsDoesNotCacheDefaults(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(map[string]string{account.SettingKeyAccountSchedulingThresholds: `{"openai":85,"grok":88,"kiro":87}`})

	err := svc.Save(context.Background(), &composite.Snapshot{
		FrontendURL: "https://example.test",
	})
	require.NoError(t, err)

	got := svc.Account.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, 85, got[capability.PlatformOpenAI])
	require.Equal(t, 88, got[capability.PlatformGrok])
	require.NotContains(t, got, "kiro")
}

func TestAccountSchedulingThresholds_InvalidStoredValueUsesSameDefaultsInSettingsAndCache(t *testing.T) {
	svc := newSettingServiceForPlatformThresholdTest(map[string]string{account.SettingKeyAccountSchedulingThresholds: `{"openai":0,"grok":88,"kiro":87}`})

	settings := composite.Parse(map[string]string{account.SettingKeyAccountSchedulingThresholds: `{"openai":0,"grok":88,"kiro":87}`}, svc.Read)
	cached := svc.Account.GetAccountSchedulingThresholds(context.Background())

	require.Equal(t, settings.AccountSchedulingThresholds, cached)
	require.Equal(t, 100, cached[capability.PlatformOpenAI])
	require.Equal(t, 88, cached[capability.PlatformGrok])
	require.NotContains(t, cached, "kiro")
}

func TestGetAccountSchedulingThresholds_NilRepoReturnsDefaults(t *testing.T) {
	svc := account.NewRuntimeSettings(nil, nil)
	got := svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, map[string]int{
		capability.PlatformOpenAI:    100,
		capability.PlatformAnthropic: 100,
		capability.PlatformGrok:      100,
	}, got)
}
