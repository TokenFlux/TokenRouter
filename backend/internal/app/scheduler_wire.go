//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/google/wire"
)

// schedulerProviders 构造唯一生产实例；Start/Stop 由既有生命周期绑定执行。
var schedulerProviders = wire.NewSet(provideSchedulerSharedState, provideUpstreamHealth, provideSchedulerDiagnosticsHTTP, provideSchedulerCache,
	wire.Bind(new(scheduler.SnapshotCache), new(*schedulerredis.SnapshotCache)),
	provideSchedulerSnapshot,
	provideConcurrencyCache, provideConcurrency, provideSessionCache, provideMessageQueue,
	schedulerredis.NewRPMCache, schedulerredis.NewUserRPMCache, schedulerredis.NewUserMsgQueueCache,
	schedulerpostgres.NewSchedulerOutboxRepository)
