//go:build unit

package googleforward_test

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
)

// newUpstreamHealthForTest 仅投影原测试配置，不保留旧服务或调用代理。
func newUpstreamHealthForTest(store gatewayprovider.ExecutionAccountStore, _ *googleforward.Options, cache account.TempUnschedCache, options account.HealthOptions, readers *gatewayprovider.RuntimeReaders) *accountprovider.UpstreamHealth {
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: options, Readers: readers})
}
