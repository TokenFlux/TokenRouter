package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// newExecutionAvailabilityForTest 显式传递同一测试仓储和渠道，不从执行服务反查依赖。
func newExecutionAvailabilityForTest(store gatewayprovider.ExecutionAccountStore, channels *routing.ChannelService, cfg *config.Config) *gatewayModelAvailability {
	var source gatewayprovider.AvailabilityAccounts
	if store != nil {
		source = gatewaytestkit.AvailabilityStore{Source: store}
	}
	simple := cfg != nil && cfg.RunMode == config.RunModeSimple
	general := gatewayprovider.NewModelAvailability(source, channels, simple, false)
	compatible := gatewayprovider.NewModelAvailability(source, channels, simple, true)
	return &gatewayModelAvailability{
		Messages:   routing.ModelAvailabilityDiagnoserFunc(general.DiagnoseGeneral),
		Compatible: routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatible),
		Resolved:   routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatibleRouting),
	}
}
