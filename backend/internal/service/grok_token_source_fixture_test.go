//go:build unit

package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 旧网关测试暂用仓储端口投影，令牌规则仍只执行原生账号实现。
func newGrokTokenSourceForTest(repo gatewayprovider.ExecutionAccountStore, cache account.AccessTokenCache) *account.GrokTokenSource {
	return &account.GrokTokenSource{Repository: tokenSourceFixtureRepository(repo), Cache: cache, Policy: account.GrokProviderRefreshPolicy()}
}

// 仅连接已有测试刷新器，不复制锁、凭据合并或错误处理。
func bindGrokRefreshForTest(source *account.GrokTokenSource, refresh *account.OAuthRefreshAPI, executor account.OAuthRefreshExecutor) {
	source.Refresh = func(ctx context.Context, record *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
		return refresh.RefreshIfNeeded(ctx, record, executor, window)
	}
}

// newGrokCredentialRefreshForTest 只配置原生协调器，不保留旧结果或执行器包装。
func newGrokCredentialRefreshForTest(repo gatewayprovider.ExecutionAccountStore, cache account.AccessTokenCache) *account.OAuthRefreshAPI {
	return account.NewOAuthRefreshAPI(tokenSourceFixtureRepository(repo), cache, account.RefreshOptions{Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error, Platform: account.AccountRefreshPlatformPolicy()})
}
