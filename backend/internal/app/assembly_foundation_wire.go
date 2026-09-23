//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// 基础设施与底层存储的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var foundationAssemblyProviders = wire.NewSet(
	provideCalendar,
	cacheProviders,
	provideEnt,
	provideRedis,
	storageProviders,
	moduleStorageProviders,
)
