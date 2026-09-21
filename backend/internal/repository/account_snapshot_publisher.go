// 未迁仓储的旧入口只投影账号形状，发布规则由 scheduler 唯一实现。
package repository

import (
	"context"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func PublishAccountSnapshot(ctx context.Context, id int64, read func(context.Context, int64) (*service.Account, error), cache service.SchedulerCache) {
	var port scheduler.SnapshotPublicationCache
	if cache != nil {
		port = service.LegacySnapshotCachePort(cache)
	}
	scheduler.SnapshotPublisher{Cache: port, Diagnostics: scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}, Read: func(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
		v, err := read(ctx, id)
		return service.LegacySnapshotWrap(v), err
	}}.Publish(ctx, id)
}
func PublishAccountSnapshots(ctx context.Context, ids []int64, read func(context.Context, []int64) ([]*service.Account, error), cache service.SchedulerCache) {
	var port scheduler.SnapshotPublicationCache
	if cache != nil {
		port = service.LegacySnapshotCachePort(cache)
	}
	scheduler.SnapshotPublisher{Cache: port, Diagnostics: scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}, ReadMany: func(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
		v, err := read(ctx, ids)
		return service.LegacySnapshotWrapPointers(v), err
	}}.PublishMany(ctx, ids)
}
func DropAccountSnapshot(ctx context.Context, id int64, cache service.SchedulerCache) {
	var port scheduler.SnapshotPublicationCache
	if cache != nil {
		port = service.LegacySnapshotCachePort(cache)
	}
	scheduler.SnapshotPublisher{Cache: port, Diagnostics: scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}}.Drop(ctx, id)
}
