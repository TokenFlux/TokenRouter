// 旧入口只组合投影端口，快照、重建和 outbox 状态唯一位于 scheduler。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// NewSchedulerSnapshotService 暂供尚未迁出的测试装配原生核心，不创建旧运行时。
func NewSchedulerSnapshotService(cache SchedulerCache, outbox scheduler.SchedulerOutboxRepository, accounts gatewayprovider.ExecutionAccountStore, groups routing.GroupRepository, cfg *config.Config) *scheduler.SnapshotService {
	cachePort := LegacySnapshotCachePort(cache)
	var accountsPort scheduler.SnapshotAccountSource
	if accounts != nil {
		accountsPort = legacySnapshotAccounts{ExecutionAccountStore: accounts}
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
	return scheduler.NewSnapshotService(cachePort, outbox, accountsPort, groupsPort, LegacySnapshotOptions(cfg), scheduler.SnapshotBindings{AccountNotFound: account.ErrAccountNotFound, GroupNotFound: routing.ErrGroupNotFound, Diagnostics: scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}})
}
func LegacySnapshotOptions(cfg *config.Config) *scheduler.SnapshotOptions {
	if cfg == nil {
		return nil
	}
	v := cfg.Gateway.Scheduling
	return &scheduler.SnapshotOptions{Simple: cfg.RunMode == config.RunModeSimple, DbFallbackEnabled: v.DbFallbackEnabled, DbFallbackMaxQPS: v.DbFallbackMaxQPS, DbFallbackTimeoutSeconds: v.DbFallbackTimeoutSeconds, OutboxPollIntervalSeconds: v.OutboxPollIntervalSeconds, FullRebuildIntervalSeconds: v.FullRebuildIntervalSeconds, OutboxLagWarnSeconds: v.OutboxLagWarnSeconds, OutboxLagRebuildSeconds: v.OutboxLagRebuildSeconds, OutboxLagRebuildFailures: v.OutboxLagRebuildFailures, OutboxBacklogRebuildRows: v.OutboxBacklogRebuildRows}
}
