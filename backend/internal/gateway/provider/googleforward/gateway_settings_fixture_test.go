package googleforward_test

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// newExecutionReadersFixture 直接绑定原生端口，保留原替身和设置读取时点。
func newExecutionReadersFixture(repo settings.Repository) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settings.New(repo)
	}
	value := gatewaytestkit.RuntimeReaders(repo)
	return value
}
