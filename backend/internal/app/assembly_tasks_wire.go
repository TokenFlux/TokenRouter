//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// 创作与批量任务的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var tasksAssemblyProviders = wire.NewSet(
	provideS13TaskActivity,
	provideS13CreativePublic,
	provideCreativeWorkerRuntime,
	provideBatchPricing,
	provideS13BatchPublic,
	provideS13BatchDownload,
	provideS13BatchCleanup,
	provideBatchCleanupRuntime,
	provideS13BatchRuntime,
	provideS13BatchRegistry,
	provideS13CreativeHTTP,
	provideS13BatchHTTP,
	provideCreativeRuntimeSettings,
	provideCreativeSettingsHTTP,
)
