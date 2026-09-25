// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func provideRoutingChannels(repo *routingpostgres.ChannelStore, invalidator apikey.APIKeyAuthCacheInvalidator) *routing.ChannelService {
	return routing.NewChannelService(repo, invalidator, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}

func provideChannelCatalog(calculator *billing.Calculator, prices *pricingprovider.PricingService) *routing.ChannelCatalog {
	return &routing.ChannelCatalog{Prices: calculator, NamesByProvider: prices.ListModelNamesByProvider, QoderModels: qoder.DefaultRequestModelIDs}
}
