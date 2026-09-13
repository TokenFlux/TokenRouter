// 旧入口只组合投影端口，快照、重建和 outbox 状态唯一位于 scheduler。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

var (
	ErrSchedulerCacheNotReady           = scheduler.ErrSchedulerCacheNotReady
	ErrSchedulerFallbackLimited         = scheduler.ErrSchedulerFallbackLimited
	ErrSchedulerGroupLifecycleLeaseBusy = scheduler.ErrSchedulerGroupLifecycleLeaseBusy
	ErrSchedulerBucketRebuildBusy       = scheduler.ErrSchedulerBucketRebuildBusy
)

type SchedulerSnapshotService struct {
	core   *scheduler.SnapshotService
	groups GroupRepository
}

func NewSchedulerSnapshotService(cache SchedulerCache, outbox SchedulerOutboxRepository, accounts AccountRepository, groups GroupRepository, cfg *config.Config) *SchedulerSnapshotService {
	cachePort := LegacySnapshotCachePort(cache)
	var accountsPort scheduler.SnapshotAccountSource
	if accounts != nil {
		accountsPort = legacySnapshotAccounts{AccountRepository: accounts}
	}
	var groupsPort scheduler.SnapshotGroupSource
	if groups != nil {
		base := legacySnapshotGroups{GroupRepository: groups}
		groupsPort = base
		if reader, ok := groups.(interface {
			ListActiveIDs(context.Context) ([]int64, error)
		}); ok {
			groupsPort = legacySnapshotGroupsWithIDs{legacySnapshotGroups: base, list: reader.ListActiveIDs}
		}
	}
	return &SchedulerSnapshotService{core: scheduler.NewSnapshotService(cachePort, outbox, accountsPort, groupsPort, LegacySnapshotOptions(cfg), scheduler.SnapshotBindings{AccountNotFound: ErrAccountNotFound, GroupNotFound: ErrGroupNotFound, Diagnostics: LegacySchedulerDiagnostics()}), groups: groups}
}
func LegacySnapshotOptions(cfg *config.Config) *scheduler.SnapshotOptions {
	if cfg == nil {
		return nil
	}
	v := cfg.Gateway.Scheduling
	return &scheduler.SnapshotOptions{Simple: cfg.RunMode == config.RunModeSimple, DbFallbackEnabled: v.DbFallbackEnabled, DbFallbackMaxQPS: v.DbFallbackMaxQPS, DbFallbackTimeoutSeconds: v.DbFallbackTimeoutSeconds, OutboxPollIntervalSeconds: v.OutboxPollIntervalSeconds, FullRebuildIntervalSeconds: v.FullRebuildIntervalSeconds, OutboxLagWarnSeconds: v.OutboxLagWarnSeconds, OutboxLagRebuildSeconds: v.OutboxLagRebuildSeconds, OutboxLagRebuildFailures: v.OutboxLagRebuildFailures, OutboxBacklogRebuildRows: v.OutboxBacklogRebuildRows}
}
func (s *SchedulerSnapshotService) Start() {
	if s != nil {
		s.core.Start()
	}
}
func (s *SchedulerSnapshotService) Stop() {
	if s != nil {
		s.core.Stop()
	}
}
func (s *SchedulerSnapshotService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.core.StopContext(ctx)
}
func (s *SchedulerSnapshotService) ListSchedulableAccounts(ctx context.Context, groupID *int64, platform string, forced bool) ([]Account, bool, error) {
	values, mixed, err := s.core.ListSchedulableAccounts(ctx, groupID, platform, forced)
	if err != nil {
		return nil, mixed, err
	}
	records, err := LegacySnapshotValues(values)
	return records, mixed, err
}
func (s *SchedulerSnapshotService) GetAccount(ctx context.Context, id int64) (*Account, error) {
	value, err := s.core.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return LegacySnapshotValue(value)
}

// 旧完整分组形状仅供尚未迁出的网关读取，保持原一次仓储调用。
func (s *SchedulerSnapshotService) GetGroupByID(ctx context.Context, id int64) (*Group, error) {
	if s.groups == nil {
		return nil, nil
	}
	return s.groups.GetByID(ctx, id)
}
func (s *SchedulerSnapshotService) UpdateAccountInCache(ctx context.Context, value *Account) error {
	return s.core.UpdateAccountInCache(ctx, LegacySnapshotWrap(value))
}

// WrapSchedulerSnapshot 将 app 唯一核心实例暴露给旧网关；不创建第二个运行时。
func WrapSchedulerSnapshot(core *scheduler.SnapshotService, groups GroupRepository) *SchedulerSnapshotService {
	return &SchedulerSnapshotService{core: core, groups: groups}
}

// LegacySnapshotCachePort 只处理旧缓存接口形状，生产路径直接返回新缓存。
func LegacySnapshotCachePort(cache SchedulerCache) scheduler.SnapshotCache {
	var cachePort scheduler.SnapshotCache
	if cache != nil {
		base := legacySnapshotCache{SchedulerCache: cache}
		cachePort = base
		if writer, ok := cache.(legacySnapshotAccountIDWriter); ok {
			cachePort = legacySnapshotCacheWithIDs{legacySnapshotCache: base, writer: writer}
		}
	}
	if direct, ok := cache.(interface {
		SnapshotCoreCache() scheduler.SnapshotCache
	}); ok {
		cachePort = direct.SnapshotCoreCache()
	}
	return cachePort
}
