//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// assembly_runtime_wire.go registers subsystem lifecycle and application
// constructors with Wire.
// Wire readiness values require lifecycle hooks to be registered before
// Application is returned. The runtime set collects the subsystem readiness
// constructors and the application constructor, so Application.Run starts
// background work after resources and their cleanup hooks are assembled.

// runtimeAssemblyProviders supplies subsystem lifecycle registrations and the
// application constructors that collect them for startup and shutdown.
var runtimeAssemblyProviders = wire.NewSet(
	provideBStats,
	provideBStatsRuntime,
	provideModelCatalogRuntime,
	provideSettingsRuntime,
	provideAuthRuntime,
	provideMaintenanceRuntime,
	provideOpsRuntime,
	provideQueuesRuntime,
	provideJobsRuntime,
	provideCoreRuntime,
	provideRuntime,
	provideApplication,
)
