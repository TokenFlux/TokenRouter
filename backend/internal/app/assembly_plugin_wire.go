//go:build wireinject

package app

import "github.com/google/wire"

// assembly_plugin_wire.go registers the process collection and its lifecycle
// owner with Wire. Application readiness requires the lifecycle registration,
// ensuring plugin shutdown remains ordered before the shared host dependencies
// that future plugin callback capabilities consume during admitted work.

// pluginAssemblyProviders binds plugin process construction and lifecycle registration into the application dependency graph without starting either during construction.
var pluginAssemblyProviders = wire.NewSet(providePluginProcesses, providePluginRuntime)
