package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
)

// provideGrokTokens 让请求与手动查询共用原生缓存及刷新协调器。
func provideGrokTokens(store *postgres.AccountStore, cache account.AccessTokenCache, authorization *account.GrokAuthorization, refresh *account.OAuthRefreshAPI) *account.GrokTokenSource {
	executor := account.NewGrokTokenRefresher(authorization)
	return &account.GrokTokenSource{
		Cache:      cache,
		Repository: store,
		Policy:     account.GrokProviderRefreshPolicy(),
		Refresh: func(ctx context.Context, record *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, record, executor, window)
		},
	}
}
