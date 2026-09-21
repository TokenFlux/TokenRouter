package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
)

// provideOpenAITokens 绑定同一刷新协调器、缓存和指标，运行阻断端口在网关构造时接入。
func provideOpenAITokens(store *postgres.AccountStore, cache account.AccessTokenCache, authorization *account.OpenAIAuthorization, refresh *account.OAuthRefreshAPI) *account.OpenAITokenSource {
	executor := &account.OpenAITokenRefresher{Authorization: authorization}
	return &account.OpenAITokenSource{
		Cache:      cache,
		Repository: store,
		SetError:   store.SetError,
		Metrics:    &account.OpenAITokenMetricsStore{},
		Policy:     account.OpenAIProviderRefreshPolicy(),
		Debug:      slog.Debug,
		Warn:       slog.Warn,
		Refresh: func(ctx context.Context, record *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, record, executor, window)
		},
	}
}
