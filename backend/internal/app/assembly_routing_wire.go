//go:build wireinject

package app

import (
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/google/wire"
)

// routing 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var routingAssemblyProviders = wire.NewSet(
	wire.Bind(new(creativeprovider.ExecutionGroups), new(*routingpostgres.GroupStore)),
	provideRoutingSettings,
	provideAdminModelCatalog,
	provideRoutingModelList,
	provideRequestableCatalogue,
	provideMarketplace,
	provideMarketplaceHTTP,
	provideGroupCapacity,
	provideGroupProbeRunner,
	provideRoutingGroupHTTP,
	provideRoutingGroupStore,
	provideGroupReader,
	provideRoutingGroupAdmin,
	providePricingConfigService,
	providePricingCatalog,
	routingpostgres.NewPricingConfigStore,
	routinghttp.NewPricingHandler,
)
