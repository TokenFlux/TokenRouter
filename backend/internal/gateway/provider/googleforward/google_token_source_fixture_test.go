package googleforward_test

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// 旧执行链夹具直接使用原生源，技术依赖按原零配置构造注入。
func newGeminiTokenSourceForTest() *account.GeminiTokenSource {
	return &account.GeminiTokenSource{Options: account.GeminiTokenOptions{

		Debug: slog.Debug,
		Warn:  slog.Warn,

		Vertex: func(ctx context.Context, value *account.Record) (string, error) {
			return provider.VertexServiceAccountAccessToken(ctx, nil, value)
		},
	}}
}

func newAntigravityTokenSourceForTest(cache account.AccessTokenCache) *account.AntigravityTokenSource {
	return &account.AntigravityTokenSource{Options: account.AntigravityTokenOptions{
		Cache: cache, Debug: slog.Debug, Warn: slog.Warn,
	}}
}
