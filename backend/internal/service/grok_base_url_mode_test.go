//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokBaseURLSettingRepoStub struct{ values map[string]string }

func (r *grokBaseURLSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("setting %s not found", key)
}
func (r *grokBaseURLSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, fmt.Errorf("unused")
}
func (r *grokBaseURLSettingRepoStub) Set(context.Context, string, string) error { return nil }
func (r *grokBaseURLSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *grokBaseURLSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *grokBaseURLSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *grokBaseURLSettingRepoStub) Delete(context.Context, string) error { return nil }

func TestGrokBaseURLForMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"api", xai.DefaultBaseURL},
		{"us-east-1", xai.DefaultUSEast1BaseURL},
		{"us-west-2", xai.DefaultUSWest2BaseURL},
		{"eu-west-1", xai.DefaultEUWest1BaseURL},
		{"cli", xai.DefaultCLIBaseURL},
		{"invalid", xai.DefaultCLIBaseURL},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			require.Equal(t, tc.want, GrokBaseURLForMode(tc.mode))
		})
	}
}

func TestSettingServiceResolveGrokBaseURLHonorsModeAndExplicitPins(t *testing.T) {
	repo := &grokBaseURLSettingRepoStub{values: map[string]string{SettingKeyGrokDefaultBaseURLMode: GrokDefaultBaseURLModeUSWest2}}
	svc := NewSettingService(repo, nil)
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{}}
	require.Equal(t, xai.DefaultUSWest2BaseURL, svc.ResolveGrokBaseURL(context.Background(), account))

	// 显式指定的官方端点应保持固定。
	account.Credentials["base_url"] = xai.DefaultBaseURL
	require.Equal(t, xai.DefaultBaseURL, svc.ResolveGrokBaseURL(context.Background(), account))

	// 显式指定的区域端点仍具有最高优先级。
	account.Credentials["base_url"] = xai.DefaultEUWest1BaseURL
	require.Equal(t, xai.DefaultEUWest1BaseURL, svc.ResolveGrokBaseURL(context.Background(), account))
}

func TestAccountGetGrokBaseURLOrPreservesCustomOAuthURLForPolicyValidation(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{
		"base_url": "https://attacker.invalid/v1",
	}}
	require.Equal(t, "https://attacker.invalid/v1", account.GetGrokBaseURLOr(xai.DefaultCLIBaseURL))
}
