//go:build wireinject

package app

import (
	"github.com/google/wire"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// assembly_plugin_wire.go registers plugin storage, administration, process ownership,
// and lifecycle reconciliation with Wire. HTTP handlers and background startup
// share the same manager, while log reads use a narrow core interface. Lifecycle
// registration keeps shutdown ordered before dependencies used by admitted callbacks.

// pluginAssemblyProviders binds plugin process construction and lifecycle registration into the application dependency graph without starting either during construction.
var pluginAssemblyProviders = wire.NewSet(
	postgres.New, providePluginConfiguration, providePluginSource, providePluginGrants, providePluginWrites, providePluginWallet, providePluginSubscriptions, providePluginQueries, providePluginEmails, providePluginCaptcha, providePluginKV, providePluginRedeemCodes, providePluginHostServices,
	providePluginProcesses, providePluginPackages, providePluginCatalog, providePluginManager, providePluginService, httpapi.NewHandler, providePluginPublicHandler, providePluginRouteMount,
	wire.Bind(new(plugin.LogReader), new(*provider.Processes)), providePluginRuntime,
)
