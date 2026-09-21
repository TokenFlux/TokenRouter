package app

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// provideGeminiTokens 组合原 project 查询、Vertex 交换及凭据字段持久化。
func provideGeminiTokens(store *postgres.AccountStore, cache account.AccessTokenCache, authorization *account.GeminiAuthorization, refresh *account.OAuthRefreshAPI) *account.GeminiTokenSource {
	executor := &account.GeminiTokenRefresher{Authorization: authorization, Key: provider.GeminiTokenCacheKey}
	return &account.GeminiTokenSource{Options: account.GeminiTokenOptions{
		Cache: cache, Repository: store, Policy: account.GeminiProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn, Logf: log.Printf,
		Project: authorization.FetchProject, ResolveProxy: authorization.Options.ResolveProxy,
		Vertex: func(ctx context.Context, value *account.Record) (string, error) {
			return provider.VertexServiceAccountAccessToken(ctx, cache, value)
		},
		Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
			_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Refresh: func(ctx context.Context, value *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, value, executor, window)
		},
	}}
}

// provideAntigravityTokens 绑定唯一回填状态、原冷却缓存与刷新协调器。
func provideAntigravityTokens(store *postgres.AccountStore, cache account.AccessTokenCache, authorization *account.AntigravityAuthorization, refresh *account.OAuthRefreshAPI, cooldown account.TempUnschedCache) *account.AntigravityTokenSource {
	executor := &account.AntigravityRefreshRules{
		RefreshAccountToken:     authorization.RefreshAccountToken,
		BuildAccountCredentials: authorization.BuildAccountCredentials,
		Printf:                  func(format string, args ...any) { _, _ = fmt.Printf(format, args...) },
		Logf:                    log.Printf,
	}
	return &account.AntigravityTokenSource{Options: account.AntigravityTokenOptions{
		Cache: cache, Repository: store, Policy: account.AntigravityProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn, TempUnschedCache: cooldown,
		SetTempUnschedulable: store.SetTempUnschedulable,
		FillProject:          authorization.FillProjectID,
		Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
			_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Refresh: func(ctx context.Context, value *account.Record, window time.Duration) (*account.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, value, executor, window)
		},
	}}
}
