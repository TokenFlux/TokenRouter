package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// gatewayModelAvailability 固定三种诊断意图，共用账号存储和分组映射读取实例，不读取执行服务。
type gatewayModelAvailability struct {
	Messages   routing.ModelAvailabilityDiagnoser
	Compatible routing.ModelAvailabilityDiagnoser
	Resolved   routing.ModelAvailabilityDiagnoser
}

func provideGatewayModelAvailability(store *postgres.AccountStore, modelConfigs *routing.PricingConfigService, cfg *config.Config) *gatewayModelAvailability {
	simple := cfg != nil && cfg.RunMode == config.RunModeSimple
	var source gatewayprovider.AvailabilityAccounts
	if store != nil {
		source = store
	}
	general := gatewayprovider.NewModelAvailability(source, modelConfigs, simple, false)
	compatible := gatewayprovider.NewModelAvailability(source, modelConfigs, simple, true)
	return &gatewayModelAvailability{
		Messages:   routing.ModelAvailabilityDiagnoserFunc(general.DiagnoseGeneral),
		Compatible: routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatible),
		Resolved:   routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatibleRouting),
	}
}
