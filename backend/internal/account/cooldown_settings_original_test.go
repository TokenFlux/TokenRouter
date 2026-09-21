//go:build unit

package account_test

import (
	"context"
	"encoding/json"
	"testing"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagesOAuthUnavailableCooldownSettingsDefaultAndStoredValue(t *testing.T) {
	repo := newMockSettingRepo()
	svc := account.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound)

	settings, err := svc.GetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 30, settings.CooldownMinutes)

	require.NoError(t, svc.SetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background(), &account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: 7}))
	settings, err = svc.GetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 7, settings.CooldownMinutes)
}

func TestSetOpenAIImagesOAuthUnavailableCooldownSettingsBoundaries(t *testing.T) {
	svc := account.NewRuntimeSettings(newMockSettingRepo(), settingscore.ErrSettingNotFound)

	for _, minutes := range []int{1, account.OpenAIImagesOAuthUnavailableMaxCooldownMinutes} {
		err := svc.SetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background(), &account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: minutes})
		require.NoError(t, err, "should accept cooldown_minutes=%d", minutes)
	}

	for _, minutes := range []int{0, -1, account.OpenAIImagesOAuthUnavailableMaxCooldownMinutes + 1} {
		err := svc.SetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background(), &account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: minutes})
		require.ErrorContains(t, err, "cooldown_minutes must be between 1-120")
	}
}

func TestOpenAIImagesOAuthUnavailableCooldownSettingsRejectsOverflowingValue(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	repo := newMockSettingRepo()
	svc := account.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound)

	err := svc.SetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background(), &account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: maxInt})
	require.ErrorContains(t, err, "cooldown_minutes must be between 1-120")

	for _, minutes := range []int{account.OpenAIImagesOAuthUnavailableMaxCooldownMinutes + 1, maxInt} {
		data, marshalErr := json.Marshal(account.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: minutes})
		require.NoError(t, marshalErr)
		repo.data[account.SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings] = string(data)

		settings, getErr := svc.GetOpenAIImagesOAuthUnavailableCooldownSettings(context.Background())
		require.NoError(t, getErr)
		require.Equal(t, account.OpenAIImagesOAuthUnavailableDefaultCooldownMinutes, settings.CooldownMinutes)
	}
}
