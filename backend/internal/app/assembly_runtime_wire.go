//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// 生命周期与应用根的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var runtimeAssemblyProviders = wire.NewSet(
	provideBootRuntime,
	provideAuthRuntime,
	provideMaintenanceRuntime,
	provideOpsRuntime,
	provideQueuesRuntime,
	provideJobsRuntime,
	provideCoreRuntime,
	provideRuntime,
	provideApplication,
)
