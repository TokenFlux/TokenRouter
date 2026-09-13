// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	qoder "github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
	time "time"
)

func provideRoutingChannels(repo *routingpostgres.ChannelStore, invalidator service.APIKeyAuthCacheInvalidator) *routing.ChannelService {
	return routing.NewChannelService(repo, invalidator, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}
func provideLegacyChannels(core *routing.ChannelService) *service.ChannelService {
	return service.WrapChannelService(core)
}
func provideChannelCatalog(calculator *billing.Calculator, prices *service.PricingService) *routing.ChannelCatalog {
	return &routing.ChannelCatalog{Prices: calculator, NamesByProvider: prices.ListModelNamesByProvider, QoderModels: qoder.DefaultRequestModelIDs}
}

func provideBillingCalculatorCore(legacy *service.BillingService) *billing.Calculator {
	return legacy.Calculator
}
