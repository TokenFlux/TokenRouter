//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// usage 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
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
	provideUsageAggregation,
	provideUsageCleanup,
	provideUsageDashboard,
)
