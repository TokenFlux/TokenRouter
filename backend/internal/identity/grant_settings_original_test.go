//go:build unit

package identity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

type authSourceDefaultsRepoStub struct {
	values  map[string]string
	updates map[string]string
}

func (s *authSourceDefaultsRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *authSourceDefaultsRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *authSourceDefaultsRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *authSourceDefaultsRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *authSourceDefaultsRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.updates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.updates[key] = value
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

func (s *authSourceDefaultsRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *authSourceDefaultsRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestSettingService_GetAuthSourceDefaultSettings_ParsesValuesAndDefaults(t *testing.T) {
	repo := &authSourceDefaultsRepoStub{
		values: map[string]string{
			identity.SettingKeyAuthSourceDefaultEmailBalance:            "12.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:        "7",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions:      `[{"plan_id":11}]`,
			identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup:      "false",
			identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind: "true",
			identity.SettingKeyForceEmailOnThirdPartySignup:             "true",
		},
	}
	svc := identity.NewGrantSettings(repo, identity.GrantSettingsOptions{})

	got, err := svc.GetAuthSourceDefaultSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 12.5, got.Email.Balance)
	require.Equal(t, 7, got.Email.Concurrency)
	require.Equal(t, []identity.DefaultSubscriptionSetting{{PlanID: 11}}, got.Email.Subscriptions)
	require.False(t, got.Email.GrantOnSignup)
	require.False(t, got.Email.GrantOnFirstBind)
	require.Equal(t, 0.0, got.LinuxDo.Balance)
	require.Equal(t, 5, got.LinuxDo.Concurrency)
	require.Equal(t, []identity.DefaultSubscriptionSetting{}, got.LinuxDo.Subscriptions)
	require.False(t, got.LinuxDo.GrantOnSignup)
	require.True(t, got.LinuxDo.GrantOnFirstBind)
	require.Equal(t, 5, got.OIDC.Concurrency)
	require.Equal(t, 5, got.WeChat.Concurrency)
	require.False(t, got.OIDC.GrantOnSignup)
	require.False(t, got.WeChat.GrantOnSignup)
	require.True(t, got.ForceEmailOnThirdPartySignup)
}

func TestSettingService_UpdateAuthSourceDefaultSettings_PersistsAllKeys(t *testing.T) {
	repo := &authSourceDefaultsRepoStub{}
	svc := identity.NewGrantSettings(repo, identity.GrantSettingsOptions{})

	err := svc.UpdateAuthSourceDefaultSettings(context.Background(), &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{
			Balance:          1.25,
			Concurrency:      3,
			Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 21}},
			GrantOnSignup:    false,
			GrantOnFirstBind: true,
		},
		LinuxDo: identity.ProviderDefaultGrantSettings{
			Balance:          2,
			Concurrency:      4,
			Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 22}},
			GrantOnSignup:    true,
			GrantOnFirstBind: false,
		},
		OIDC: identity.ProviderDefaultGrantSettings{
			Balance:          3,
			Concurrency:      5,
			Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 23}},
			GrantOnSignup:    true,
			GrantOnFirstBind: true,
		},
		WeChat: identity.ProviderDefaultGrantSettings{
			Balance:          4,
			Concurrency:      6,
			Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 24}},
			GrantOnSignup:    false,
			GrantOnFirstBind: false,
		},
		ForceEmailOnThirdPartySignup: true,
	})
	require.NoError(t, err)
	require.Equal(t, "1.25000000", repo.updates[identity.SettingKeyAuthSourceDefaultEmailBalance])
	require.Equal(t, "3", repo.updates[identity.SettingKeyAuthSourceDefaultEmailConcurrency])
	require.Equal(t, "false", repo.updates[identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup])
	require.Equal(t, "true", repo.updates[identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind])
	require.Equal(t, "true", repo.updates[identity.SettingKeyForceEmailOnThirdPartySignup])

	var got []identity.DefaultSubscriptionSetting
	require.NoError(t, json.Unmarshal([]byte(repo.updates[identity.SettingKeyAuthSourceDefaultWeChatSubscriptions]), &got))
	require.Equal(t, []identity.DefaultSubscriptionSetting{{PlanID: 24}}, got)
}
