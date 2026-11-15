//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// assembly_usage_wire.go registers usage storage, reporting, dashboards,
// aggregation, cleanup, export, and HTTP constructors with Wire.
// Usage records feed user reports, administrator reports, background CSV
// exports, and cached dashboards. The application passes shared aggregation
// settings into dashboard and background aggregation constructors, so those
// consumers make decisions from the same preaggregation configuration.

// usageAssemblyProviders supplies usage reporting, dashboards, storage,
// aggregation, cleanup, export, and HTTP constructors to the application graph.
var usageAssemblyProviders = wire.NewSet(
	provideUsageDashboardCache,
	providePublicUsage,
	provideUsageKeys,
	provideUsageUsers,
	provideUsageHTTP,
	provideUsageSettings,
	provideAdminUsageHTTP,
	provideDashboardHTTP,
	provideUsageOptions,
	provideUsageStore,
	provideUsageRepository,
	provideUsageService,
	provideUsageAggregationRepository,
	provideUsageCleanupRepository,
	provideUsageExportRepository,
	provideUsageExportArtifactStore,
	provideUsageAggregation,
	provideUsageCleanup,
	provideUsageExport,
	provideUsageDashboard,
)
