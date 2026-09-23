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

// provideOpenAIExecutionCredentials 复用原持久读取和两种 token 源，不提前解析影子或读取凭据。
func provideOpenAIExecutionCredentials(store *postgres.AccountStore, openai *account.OpenAITokenSource, grok *account.GrokTokenSource) *account.OpenAIExecutionCredentials {
	out := &account.OpenAIExecutionCredentials{}
	if store != nil {
		out.Parent = store.GetByID
	}
	if openai != nil {
		out.OpenAI = openai.GetAccessToken
	}
	if grok != nil {
		out.Grok = grok.GetAccessToken
	}
	return out
}
