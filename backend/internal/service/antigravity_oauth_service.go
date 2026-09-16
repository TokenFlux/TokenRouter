// 旧 Antigravity 授权入口只投影原代理与账号，所有状态和规则由 account 拥有。
package service

import (
	"context"
	"fmt"
	"log/slog"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

type AntigravityOAuthService struct {
	*accountcore.AntigravityAuthorization
	sessionStore *accountcore.AntigravityAuthorizationSessions
	proxyRepo    ProxyRepository
}

func antigravityAuthorizationOptions(proxyRepo ProxyRepository) accountcore.AntigravityAuthorizationOptions {
	return accountcore.AntigravityAuthorizationOptions{
		NewClient: func(proxy string) (accountcore.AntigravityAuthorizationClient, error) {
			return antigravity.NewClient(proxy)
		},
		ResolveProxy: func(ctx context.Context, id int64) (string, bool) {
			proxy, err := proxyRepo.GetByID(ctx, id)
			if err != nil || proxy == nil {
				return "", false
			}
			return proxy.URL(), true
		},
		GenerateState: antigravity.GenerateState, GenerateCodeVerifier: antigravity.GenerateCodeVerifier, GenerateSessionID: antigravity.GenerateSessionID, GenerateCodeChallenge: antigravity.GenerateCodeChallenge, BuildAuthorizationURL: antigravity.BuildAuthorizationURL, IsConnectionError: antigravity.IsConnectionError, Printf: func(format string, args ...any) { _, _ = fmt.Printf(format, args...) }, Warn: slog.Warn, Info: slog.Info}
}
func NewAntigravityOAuthService(proxyRepo ProxyRepository) *AntigravityOAuthService {
	core := accountcore.NewAntigravityAuthorization(antigravityAuthorizationOptions(proxyRepo))
	return &AntigravityOAuthService{AntigravityAuthorization: core, sessionStore: core.Store, proxyRepo: proxyRepo}
}
func (s *AntigravityOAuthService) Stop() { _ = s.StopContext(context.Background()) }
func (s *AntigravityOAuthService) RefreshAccountToken(ctx context.Context, a *Account) (*AntigravityTokenInfo, error) {
	return s.AntigravityAuthorization.RefreshAccountToken(ctx, AccountRecordView(a))
}
func (s *AntigravityOAuthService) FillProjectID(ctx context.Context, a *Account, token string) (string, error) {
	return s.AntigravityAuthorization.FillProjectID(ctx, AccountRecordView(a), token)
}

type AntigravityAuthURLResult = accountcore.AntigravityAuthURLResult
type AntigravityExchangeCodeInput = accountcore.AntigravityExchangeCodeInput
type AntigravityTokenInfo = accountcore.AntigravityTokenInfo

func resolveDefaultTierID(raw map[string]any) string {
	return accountcore.ResolveAntigravityDefaultTierID(raw)
}
