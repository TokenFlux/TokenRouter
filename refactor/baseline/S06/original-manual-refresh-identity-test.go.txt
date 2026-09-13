package admin

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

// 模拟手动交换返回前管理员换凭据；旧交换结果必须让位，不能重新覆盖。
func TestManualRefreshDoesNotOverwriteNewAdministratorCredentials(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.accounts = []service.Account{{ID: 977, Platform: service.PlatformQoder, Type: service.AccountTypeCosy, Status: service.StatusActive, Credentials: map[string]any{"refresh_token": "observed", "security_oauth_token": "old", "machine_id": "machine"}}}
	observed := adminSvc.accounts[0]
	previous := newQoderTokenRefresherForAdmin
	t.Cleanup(func() { newQoderTokenRefresherForAdmin = previous })
	newQoderTokenRefresherForAdmin = func(service.AdminService, *service.QoderOAuthService) qoderAdminTokenRefresher {
		return qoderAdminTokenRefresherFunc(func(context.Context, *service.Account) (map[string]any, error) {
			adminSvc.accounts[0].Credentials = map[string]any{"refresh_token": "administrator", "security_oauth_token": "administrator-token", "machine_id": "machine"}
			return map[string]any{"refresh_token": "late", "security_oauth_token": "late-token", "machine_id": "machine"}, nil
		})
	}
	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	updated, _, err := h.refreshSingleAccount(context.Background(), &observed)
	require.NoError(t, err)
	require.Nil(t, adminSvc.updateAccountInput, "迟到交换不应提交旧凭据")
	require.Equal(t, "administrator", updated.GetCredential("refresh_token"))
}
