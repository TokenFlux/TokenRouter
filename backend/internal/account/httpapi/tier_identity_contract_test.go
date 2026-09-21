package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 模拟 Drive 查询期间管理员已经替换凭据与非 tier 配置。
type tierIdentityPort interface {
	AccountManagement
	account.TierManagementStore
}

type tierIdentityAdmin struct {
	tierIdentityPort
	current account.Record
}

func (s *tierIdentityAdmin) GetAccount(context.Context, int64) (*account.Record, error) {
	v := s.current
	v.Credentials = map[string]any{}
	for k, x := range s.current.Credentials {
		v.Credentials[k] = x
	}
	v.Extra = map[string]any{}
	for k, x := range s.current.Extra {
		v.Extra[k] = x
	}
	return &v, nil
}
func (s *tierIdentityAdmin) UpdateAccount(_ context.Context, _ int64, input *account.UpdateAccountInput) (*account.Record, error) {
	if input.ExpectedCredentials != nil && !account.MatchesCredentialVersion(&s.current, *input.ExpectedCredentials) {
		return nil, account.ErrRefreshAccountStateChanged
	}
	if input.Credentials != nil {
		s.current.Credentials = input.Credentials
	}
	if input.Extra != nil {
		s.current.Extra = input.Extra
	}
	return &s.current, nil
}

type tierIdentityDrive struct{ admin *tierIdentityAdmin }

func (d tierIdentityDrive) GetStorageQuota(context.Context, string, string) (*geminicli.DriveStorageInfo, error) {
	d.admin.current.Credentials = map[string]any{"oauth_type": "google_one", "access_token": "admin-new", "tier_id": "current"}
	d.admin.current.Extra = map[string]any{"admin_setting": "new"}
	return &geminicli.DriveStorageInfo{Limit: 2 * 1024 * 1024 * 1024 * 1024, Usage: 1}, nil
}
func TestTierRefreshDoesNotOverwriteNewCredentials(t *testing.T) {

	admin := &tierIdentityAdmin{current: account.Record{ID: 984, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Credentials: map[string]any{"oauth_type": "google_one", "access_token": "old", "tier_id": "old"}, Extra: map[string]any{"admin_setting": "old"}}}
	gemini := account.NewGeminiAuthorization(nil, nil, tierIdentityDrive{admin}, accountprovider.GeminiAuthorizationOptions(func() geminicli.OAuthConfig { return geminicli.OAuthConfig{} }, nil))
	h := NewManagementHandler(admin, ManagementOptions{Tier: account.NewTierManagement(admin, account.AccountTierManagementOptions(gemini))})
	router := gin.New()
	router.POST("/:id/tier", h.RefreshTier)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/984/tier", nil))
	require.Equal(t, "admin-new", admin.current.Credentials["access_token"])
	require.Equal(t, "new", admin.current.Extra["admin_setting"])
}
