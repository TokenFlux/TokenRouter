//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 旧网关测试暂用仓储端口投影，令牌规则仍只执行原生账号实现。
func newGrokTokenSourceForTest(repo gatewayprovider.ExecutionAccountStore, cache account.AccessTokenCache) *account.GrokTokenSource {
	return &account.GrokTokenSource{Repository: tokenSourceFixtureRepository(repo), Cache: cache, Policy: account.GrokProviderRefreshPolicy()}
}
