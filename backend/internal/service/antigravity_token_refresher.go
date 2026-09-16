// 旧刷新契约仅投影，统一协调器与 CAS 沿用 S06 实例。
package service

import (
	"context"
	"fmt"
	"log"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// AntigravityTokenRefresher 实现 TokenRefresher 接口
type AntigravityTokenRefresher struct {
	antigravityOAuthService *AntigravityOAuthService
}

func NewAntigravityTokenRefresher(antigravityOAuthService *AntigravityOAuthService) *AntigravityTokenRefresher {
	return &AntigravityTokenRefresher{
		antigravityOAuthService: antigravityOAuthService,
	}
}
func (r *AntigravityTokenRefresher) rules() *acctcore.AntigravityRefreshRules {
	rules := &acctcore.AntigravityRefreshRules{Printf: func(f string, args ...any) { _, _ = fmt.Printf(f, args...) }, Logf: log.Printf}
	if r.antigravityOAuthService != nil {
		rules.RefreshAccountToken = r.antigravityOAuthService.AntigravityAuthorization.RefreshAccountToken
		rules.BuildAccountCredentials = r.antigravityOAuthService.BuildAccountCredentials
	}
	return rules
}
func (r *AntigravityTokenRefresher) CacheKey(a *Account) string { return AntigravityTokenCacheKey(a) }
func (r *AntigravityTokenRefresher) CanRefresh(a *Account) bool {
	return r.rules().CanRefresh(AccountRecordView(a))
}
func (r *AntigravityTokenRefresher) NeedsRefresh(a *Account, window time.Duration) bool {
	return r.rules().NeedsRefresh(AccountRecordView(a), window)
}
func (r *AntigravityTokenRefresher) Refresh(ctx context.Context, a *Account) (map[string]any, error) {
	return r.rules().Refresh(ctx, AccountRecordView(a))
}
func antigravityForceTokenRefreshExtra(reason string) map[string]any {
	return acctcore.AntigravityForceTokenRefreshExtra(reason)
}
