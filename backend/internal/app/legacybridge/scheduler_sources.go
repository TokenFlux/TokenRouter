// 调度器直接读取新账号和路由存储；旧完整缓存报文仅在此投影，S11/S15 退出。
package legacybridge

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type SchedulerAccountSource struct{ *accountpostgres.AccountStore }

func (r SchedulerAccountSource) GetByID(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.GetByID(ctx, id)
	return service.LegacySnapshotWrap(service.AccountFromRecord(v)), err
}
func (r SchedulerAccountSource) GetByIDs(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.GetByIDs(ctx, ids)
	return wrapSchedulerRecordPointers(v), err
}
func (r SchedulerAccountSource) ListSchedulableByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableByPlatform(ctx, platform)
	return wrapSchedulerRecords(v), err
}
func (r SchedulerAccountSource) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableUngroupedByPlatform(ctx, platform)
	return wrapSchedulerRecords(v), err
}
func (r SchedulerAccountSource) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableByPlatforms(ctx, platforms)
	return wrapSchedulerRecords(v), err
}
func (r SchedulerAccountSource) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return wrapSchedulerRecords(v), err
}
func (r SchedulerAccountSource) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
	return wrapSchedulerRecords(v), err
}
func (r SchedulerAccountSource) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, group int64, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountStore.ListSchedulableByGroupIDAndPlatforms(ctx, group, platforms)
	return wrapSchedulerRecords(v), err
}

func legacySnapshotGroup(v *routing.Group) *scheduler.SnapshotGroup {
	if v == nil {
		return nil
	}
	return &scheduler.SnapshotGroup{ID: v.ID, Name: v.Name, Platform: v.Platform, Status: v.Status, Hydrated: v.Hydrated}
}

type SchedulerGroupSource struct{ *routingpostgres.GroupStore }

func (r SchedulerGroupSource) GetByID(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	v, err := r.GroupStore.GetByID(ctx, id)
	return legacySnapshotGroup(v), err
}
func (r SchedulerGroupSource) GetByIDLite(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	v, err := r.GroupStore.GetByIDLite(ctx, id)
	return legacySnapshotGroup(v), err
}
func (r SchedulerGroupSource) ListActive(ctx context.Context) ([]scheduler.SnapshotGroup, error) {
	v, err := r.GroupStore.ListActive(ctx)
	if v == nil {
		return nil, err
	}
	out := make([]scheduler.SnapshotGroup, len(v))
	for i := range v {
		out[i] = *legacySnapshotGroup(&v[i])
	}
	return out, err
}

// ListActiveIDs 保留全量重建优先查询 ID 的既有路径。
func (r SchedulerGroupSource) ListActiveIDs(ctx context.Context) ([]int64, error) {
	return r.GroupStore.ListActiveIDs(ctx)
}
func wrapSchedulerRecordPointers(v []*account.Record) []scheduler.SnapshotAccount {
	if v == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(v))
	for i, value := range v {
		out[i] = service.LegacySnapshotWrap(service.AccountFromRecord(value))
	}
	return out
}
func wrapSchedulerRecords(v []account.Record) []scheduler.SnapshotAccount {
	if v == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(v))
	for i := range v {
		out[i] = service.LegacySnapshotWrap(service.AccountFromRecord(&v[i]))
	}
	return out
}
