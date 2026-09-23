//go:build wireinject

package app

import (
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/server"

	"github.com/google/wire"
)

// HTTP 汇总与全局入口的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var httpAssemblyProviders = wire.NewSet(
	nativeHTTPProviders,
	server.ProviderSet,
	provideHTTPOptions,
	provideHTTPRouteMount,
	provideAuthRouteMount,
	provideUserRouteMount,
	provideAdminRouteMount,
	provideGatewayRouteMount,
	providePaymentRouteMount,
	provideForwardedSettings,
	providePanelSettings,
	serverhttp.NewPanelSettingsHandler,
	provideRouterRuntime,
)
