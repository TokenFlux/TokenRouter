//go:build unit

package identity_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestAuthService_RegisterSnapshotsDefaultUserAPIKeyLimit(t *testing.T) {
	tests := []struct {
		name   string
		value  *string
		expect int
	}{
		{name: "设置缺失回退内置值", expect: identity.DefaultUserAPIKeyLimit},
		{name: "显式不限量", value: stringPointer("0"), expect: 0},
		{name: "显式上限", value: stringPointer("23"), expect: 23},
		{name: "非法设置回退内置值", value: stringPointer("invalid"), expect: identity.DefaultUserAPIKeyLimit},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := map[string]string{identity.SettingKeyRegistrationEnabled: "true"}
			if tt.value != nil {
				settings[identity.SettingKeyDefaultUserAPIKeyLimit] = *tt.value
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
		identity.SettingKeyRegistrationEnabled:    "true",
		identity.SettingKeyDefaultUserAPIKeyLimit: "10",
	}
	repo := &userRepoStub{}
	svc := newAuthService(repo, settings, nil, nil)

	_, first, err := svc.Register(context.Background(), "api-limit-first@example.com", "strong-pass")
	require.NoError(t, err)
	require.Equal(t, 10, first.APIKeyLimit)

	settings[identity.SettingKeyDefaultUserAPIKeyLimit] = "20"
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
				identity.SettingKeyRegistrationEnabled:    "true",
				identity.SettingKeyDefaultUserAPIKeyLimit: "29",
			}, nil, nil)
			svc.RefreshTokens = &refreshTokenCacheStub{}
			rebuildOriginalSession(svc)

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
