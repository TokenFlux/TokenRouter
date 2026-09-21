//go:build unit

package httpapi

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/stretchr/testify/require"
)

type grokRefreshOAuthStub struct {
	account *accountcore.Record
	info    *accountcore.GrokTokenInfo
	calls   int
}

func (s *grokRefreshOAuthStub) RefreshAccountToken(_ context.Context, account *accountcore.Record) (*accountcore.GrokTokenInfo, error) {
	s.calls++
	s.account = account
	return s.info, nil
}

func (s *grokRefreshOAuthStub) BuildAccountCredentials(info *accountcore.GrokTokenInfo) map[string]any {
	return map[string]any{
		"access_token":  info.AccessToken,
		"refresh_token": info.RefreshToken,
		"expires_at":    info.ExpiresAt,
		"base_url":      "https://api.x.ai/v1",
	}
}

type grokRefreshAdminService struct {
	*managementMutationFixture
	updatedCredentials map[string]any
}

func (s *grokRefreshAdminService) UpdateAccount(_ context.Context, id int64, input *accountcore.UpdateAccountInput) (*accountcore.Record, error) {
	s.updatedCredentials = input.Credentials
	return &accountcore.Record{
		ID:          id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: input.Credentials,
	}, nil
}

func TestRefreshSingleAccountRoutesGrokThroughGrokOAuthService(t *testing.T) {
	t.Parallel()

	adminSvc := &grokRefreshAdminService{managementMutationFixture: newManagementMutationFixture()}
	grokOAuth := &grokRefreshOAuthStub{info: &accountcore.GrokTokenInfo{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    1_800_000_000,
	}}
	handler := newManagedRefreshFixture(adminSvc, &accountcore.ManualCredentialExchange{Grok: grokOAuth})
	account := &accountcore.Record{
		ID:       4227,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "old-access",
			"refresh_token":      "old-refresh",
			"base_url":           "https://example.invalid/v1",
			"subscription_tier":  "SUPER_GROK",
			"entitlement_status": "ACTIVE",
		},
	}

	updated, warning, err := handler.Refresh(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, 1, grokOAuth.calls)
	// 原始账号的数据必须完整传入；原生记录的时钟函数不参与值比较。
	require.Equal(t, account, grokOAuth.account)
	require.Equal(t, "new-access", adminSvc.updatedCredentials["access_token"])
	require.Equal(t, "new-refresh", adminSvc.updatedCredentials["refresh_token"])
	require.Equal(t, "https://example.invalid/v1", adminSvc.updatedCredentials["base_url"])
	require.Equal(t, "SUPER_GROK", adminSvc.updatedCredentials["subscription_tier"])
	require.Equal(t, "ACTIVE", adminSvc.updatedCredentials["entitlement_status"])
	require.Equal(t, adminSvc.updatedCredentials, updated.Credentials)
}
