//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetDefaultUserAPIKeyLimit(t *testing.T) {
	tests := []struct {
		name   string
		value  *string
		expect int
	}{
		{name: "设置缺失", expect: DefaultUserAPIKeyLimit},
		{name: "非法字符串", value: stringPointer("invalid"), expect: DefaultUserAPIKeyLimit},
		{name: "负数", value: stringPointer("-1"), expect: DefaultUserAPIKeyLimit},
		{name: "超过数据库上限", value: stringPointer(fmt.Sprint(MaxUserAPIKeyLimit + 1)), expect: DefaultUserAPIKeyLimit},
		{name: "显式不限量", value: stringPointer("0"), expect: 0},
		{name: "显式上限", value: stringPointer("37"), expect: 37},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{}
			if tt.value != nil {
				values[SettingKeyDefaultUserAPIKeyLimit] = *tt.value
			}
			svc := NewSettingService(&settingRepoStub{values: values}, &config.Config{})
			require.Equal(t, tt.expect, svc.GetDefaultUserAPIKeyLimit(context.Background()))
		})
	}
}

func TestSettingService_ParseDefaultUserAPIKeyLimit(t *testing.T) {
	svc := NewSettingService(&settingGetAllRepoStub{}, &config.Config{})

	tests := []struct {
		name     string
		settings map[string]string
		expect   int
	}{
		{name: "设置缺失", settings: map[string]string{}, expect: DefaultUserAPIKeyLimit},
		{name: "非法设置", settings: map[string]string{SettingKeyDefaultUserAPIKeyLimit: "bad"}, expect: DefaultUserAPIKeyLimit},
		{name: "负数设置", settings: map[string]string{SettingKeyDefaultUserAPIKeyLimit: "-2"}, expect: DefaultUserAPIKeyLimit},
		{name: "超过数据库上限", settings: map[string]string{SettingKeyDefaultUserAPIKeyLimit: fmt.Sprint(MaxUserAPIKeyLimit + 1)}, expect: DefaultUserAPIKeyLimit},
		{name: "显式不限量", settings: map[string]string{SettingKeyDefaultUserAPIKeyLimit: "0"}, expect: 0},
		{name: "显式上限", settings: map[string]string{SettingKeyDefaultUserAPIKeyLimit: "58"}, expect: 58},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := svc.parseSettings(tt.settings)
			require.Equal(t, tt.expect, settings.DefaultUserAPIKeyLimit)
		})
	}
}

func TestSettingService_UpdateDefaultUserAPIKeyLimit(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{DefaultUserAPIKeyLimit: 0}))
	require.Equal(t, "0", repo.updates[SettingKeyDefaultUserAPIKeyLimit])

	require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{DefaultUserAPIKeyLimit: 64}))
	require.Equal(t, "64", repo.updates[SettingKeyDefaultUserAPIKeyLimit])

	err := svc.UpdateSettings(context.Background(), &SystemSettings{DefaultUserAPIKeyLimit: -1})
	require.ErrorIs(t, err, ErrUserAPIKeyLimitInvalid)
	require.Equal(t, 400, infraerrors.Code(err))

	err = svc.UpdateSettings(context.Background(), &SystemSettings{DefaultUserAPIKeyLimit: MaxUserAPIKeyLimit + 1})
	require.ErrorIs(t, err, ErrUserAPIKeyLimitInvalid)
	require.Equal(t, 400, infraerrors.Code(err))
}

func TestAuthService_RegisterSnapshotsDefaultUserAPIKeyLimit(t *testing.T) {
	tests := []struct {
		name   string
		value  *string
		expect int
	}{
		{name: "设置缺失回退内置值", expect: DefaultUserAPIKeyLimit},
		{name: "显式不限量", value: stringPointer("0"), expect: 0},
		{name: "显式上限", value: stringPointer("23"), expect: 23},
		{name: "非法设置回退内置值", value: stringPointer("invalid"), expect: DefaultUserAPIKeyLimit},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := map[string]string{SettingKeyRegistrationEnabled: "true"}
			if tt.value != nil {
				settings[SettingKeyDefaultUserAPIKeyLimit] = *tt.value
			}
			repo := &userRepoStub{nextID: int64(index + 1)}
			svc := newAuthService(repo, settings, nil, nil)

			_, user, err := svc.Register(context.Background(), fmt.Sprintf("api-limit-%d@example.com", index), "strong-pass")
			require.NoError(t, err)
			require.Equal(t, tt.expect, user.APIKeyLimit)
			require.Equal(t, tt.expect, repo.created[0].APIKeyLimit)
		})
	}
}

// 修改系统默认值只影响之后注册的用户，已有用户保留注册时的快照。
func TestAuthService_DefaultUserAPIKeyLimitDoesNotRetroactivelyChangeUsers(t *testing.T) {
	settings := map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyDefaultUserAPIKeyLimit: "10",
	}
	repo := &userRepoStub{}
	svc := newAuthService(repo, settings, nil, nil)

	_, first, err := svc.Register(context.Background(), "api-limit-first@example.com", "strong-pass")
	require.NoError(t, err)
	require.Equal(t, 10, first.APIKeyLimit)

	settings[SettingKeyDefaultUserAPIKeyLimit] = "20"
	_, second, err := svc.Register(context.Background(), "api-limit-second@example.com", "strong-pass")
	require.NoError(t, err)
	require.Equal(t, 20, second.APIKeyLimit)
	require.Equal(t, 10, first.APIKeyLimit)
	require.Equal(t, 10, repo.created[0].APIKeyLimit)
}

// 各 OAuth 来源共用同一注册入口，都必须固化当前的默认 API Key 上限。
func TestAuthService_AllOAuthSourcesSnapshotDefaultUserAPIKeyLimit(t *testing.T) {
	for index, signupSource := range []string{"linuxdo", "wechat", "oidc", "github", "google", "dingtalk"} {
		t.Run(signupSource, func(t *testing.T) {
			repo := &userRepoStub{}
			svc := newAuthService(repo, map[string]string{
				SettingKeyRegistrationEnabled:    "true",
				SettingKeyDefaultUserAPIKeyLimit: "29",
			}, nil, nil)
			svc.refreshTokenCache = &refreshTokenCacheStub{}

			_, user, err := svc.LoginOrRegisterOAuthWithTokenPairForSource(
				context.Background(),
				fmt.Sprintf("api-limit-oauth-%d@example.com", index),
				"OAuth User",
				"",
				"",
				signupSource,
			)
			require.NoError(t, err)
			require.Equal(t, 29, user.APIKeyLimit)
			require.Equal(t, 29, repo.created[0].APIKeyLimit)
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
