package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// newExecutionReadersFixture 直接绑定原生端口，保留原替身、搜索注册表及静态流预算。
func newExecutionReadersFixture(repo settings.Repository, _ *config.Config) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settings.New(repo)
	}
	value := gatewaytestkit.RuntimeReaders(repo)
	return value
}
