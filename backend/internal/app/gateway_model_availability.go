package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// gatewayModelAvailability 固定三种诊断意图，共用原生存储和渠道实例，不读取执行服务。
type gatewayModelAvailability struct {
	Messages   routing.ModelAvailabilityDiagnoser
	Compatible routing.ModelAvailabilityDiagnoser
	Resolved   routing.ModelAvailabilityDiagnoser
}

func provideGatewayModelAvailability(store *postgres.AccountStore, channels *routing.ChannelService, cfg *config.Config) *gatewayModelAvailability {
	simple := cfg != nil && cfg.RunMode == config.RunModeSimple
	var source gatewayprovider.AvailabilityAccounts
	if store != nil {
		source = store
	}
	general := gatewayprovider.NewModelAvailability(source, channels, simple, false)
	compatible := gatewayprovider.NewModelAvailability(source, channels, simple, true)
	return &gatewayModelAvailability{
		Messages:   routing.ModelAvailabilityDiagnoserFunc(general.DiagnoseGeneral),
		Compatible: routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatible),
		Resolved:   routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatibleRouting),
	}
}
