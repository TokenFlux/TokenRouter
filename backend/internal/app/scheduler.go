package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func provideSchedulerCache(rdb *redis.Client, cfg *config.Config) *schedulerredis.SnapshotCache {
	options := schedulerredis.SnapshotCacheOptions{}
	if cfg != nil {
		options.MGetChunkSize = cfg.Gateway.Scheduling.SnapshotMGetChunkSize
		options.WriteChunkSize = cfg.Gateway.Scheduling.SnapshotWriteChunkSize
	}
	return schedulerredis.NewSnapshotCache(rdb, codec.AccountCodec{}, options)
}
func provideLegacySchedulerCache(cache *schedulerredis.SnapshotCache) service.SchedulerCache {
	return repository.WrapSchedulerCache(cache)
}
func provideSchedulerSnapshot(cache scheduler.SnapshotCache, outbox scheduler.SchedulerOutboxRepository, accounts *accountpostgres.AccountStore, groups *routingpostgres.GroupStore, cfg *config.Config) *scheduler.SnapshotService {
	var options *scheduler.SnapshotOptions
	if cfg != nil {
		v := cfg.Gateway.Scheduling
		options = &scheduler.SnapshotOptions{Simple: cfg.RunMode == config.RunModeSimple,
			DbFallbackEnabled: v.DbFallbackEnabled, DbFallbackMaxQPS: v.DbFallbackMaxQPS, DbFallbackTimeoutSeconds: v.DbFallbackTimeoutSeconds,
			OutboxPollIntervalSeconds: v.OutboxPollIntervalSeconds, FullRebuildIntervalSeconds: v.FullRebuildIntervalSeconds, OutboxLagWarnSeconds: v.OutboxLagWarnSeconds,
			OutboxLagRebuildSeconds: v.OutboxLagRebuildSeconds, OutboxLagRebuildFailures: v.OutboxLagRebuildFailures, OutboxBacklogRebuildRows: v.OutboxBacklogRebuildRows}
	}
	return scheduler.NewSnapshotService(cache, outbox, schedulerAccountSource{AccountStore: accounts}, schedulerGroupSource{GroupStore: groups}, options,
		scheduler.SnapshotBindings{AccountNotFound: account.ErrAccountNotFound, GroupNotFound: routing.ErrGroupNotFound, Diagnostics: scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event},
		})
}
func provideLegacySchedulerSnapshot(core *scheduler.SnapshotService, groups routing.GroupRepository) *service.SchedulerSnapshotService {
	return service.WrapSchedulerSnapshot(core, groups)
}
func provideConcurrencyCache(rdb *redis.Client, cfg *config.Config) scheduler.ConcurrencyCache {
	ttl := int(cfg.Gateway.Scheduling.StickySessionWaitTimeout.Seconds())
	if cfg.Gateway.Scheduling.FallbackWaitTimeout > cfg.Gateway.Scheduling.StickySessionWaitTimeout {
		ttl = int(cfg.Gateway.Scheduling.FallbackWaitTimeout.Seconds())
	}
	if ttl <= 0 {
		ttl = cfg.Gateway.ConcurrencySlotTTLMinutes * 60
	}
	return schedulerredis.NewConcurrencyCache(rdb, cfg.Gateway.ConcurrencySlotTTLMinutes, ttl)
}
func provideConcurrency(cache scheduler.ConcurrencyCache, cfg *config.Config) *scheduler.ConcurrencyService {
	core := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	// 保留启动旧进程槽清理的必要 I/O，与后台周期启动分开。
	if err := core.CleanupStaleProcessSlots(context.Background()); err != nil {
		logging.LegacyPrintf("service.concurrency", "Warning: startup cleanup stale process slots failed: %v", err)
	}
	if cfg != nil {
		core.SetAccountLoadBatchCacheTTL(time.Duration(cfg.Gateway.Scheduling.LoadBatchCacheTTLMS) * time.Millisecond)
	}
	return core
}

func provideSessionCache(rdb *redis.Client, cfg *config.Config) scheduler.SessionLimitCache {
	minutes := 5
	if cfg != nil && cfg.Gateway.SessionIdleTimeoutMinutes > 0 {
		minutes = cfg.Gateway.SessionIdleTimeoutMinutes
	}
	return schedulerredis.NewSessionLimitCache(rdb, minutes)
}
func provideMessageQueue(cache scheduler.UserMsgQueueCache, rpm scheduler.RPMCache, cfg *config.Config) *scheduler.UserMessageQueueService {
	v := cfg.Gateway.UserMessageQueue
	return scheduler.NewUserMessageQueueService(cache, rpm, &scheduler.MessageQueueOptions{LockTTLMs: v.LockTTLMs, MinDelayMs: v.MinDelayMs, MaxDelayMs: v.MaxDelayMs}, scheduler.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
}
