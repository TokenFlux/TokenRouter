package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// provideClaudeTokens 保留 OAuth 与 Vertex 两条获取路径的原缓存和调用时点。
func provideClaudeTokens(store *postgres.AccountStore, cache account.AccessTokenCache, authorization *account.ClaudeAuthorization, refresh *account.OAuthRefreshAPI) *account.ClaudeTokenSource {
	executor := &account.ClaudeTokenRefresher{Authorization: authorization}
	return &account.ClaudeTokenSource{Options: account.ClaudeTokenOptions{
		Cache: cache, Repository: store, Policy: account.ClaudeProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn,
		Vertex: func(ctx context.Context, value *account.Record) (string, error) {
			return provider.VertexServiceAccountAccessToken(ctx, cache, value)
		},
		Refresh: func(ctx context.Context, value *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, value, executor, window)
		},
	}}
}

// provideMessageCredentials 固定复用原 Claude/Vertex 源，其他平台保持存量凭据读取。
func provideMessageCredentials(claude *account.ClaudeTokenSource) *account.MessageCredentialSource {
	result := &account.MessageCredentialSource{}
	if claude != nil {
		result.Claude = claude.GetAccessToken
	}
	return result
}
