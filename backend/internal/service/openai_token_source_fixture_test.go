package service

import (
	"log/slog"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 旧消费者夹具仅投影尚未迁完的仓储；令牌算法和指标使用原生实现。
func newOpenAITokenSourceForTest(repo gatewayprovider.ExecutionAccountStore, cache account.AccessTokenCache, _ *account.OpenAIAuthorization) *account.OpenAITokenSource {
	source := &account.OpenAITokenSource{
		Repository: gatewaytestkit.TokenRepository(repo), Cache: cache,
		Metrics: &account.OpenAITokenMetricsStore{}, Policy: account.OpenAIProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn,
	}
	if repo != nil {
		source.SetError = repo.SetError
	}
	return source
}
