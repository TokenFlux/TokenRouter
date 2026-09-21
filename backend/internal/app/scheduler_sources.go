package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

type schedulerAccountSource struct{ *accountpostgres.AccountStore }

func (r schedulerAccountSource) GetByID(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.GetByID(ctx, id)
	return codec.WrapRecord(value), err
}

func (r schedulerAccountSource) GetByIDs(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.GetByIDs(ctx, ids)
	return wrapSchedulerRecordPointers(value), err
}

func (r schedulerAccountSource) ListSchedulableByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableByPlatform(ctx, platform)
	return wrapSchedulerRecords(value), err
}

func (r schedulerAccountSource) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableUngroupedByPlatform(ctx, platform)
	return wrapSchedulerRecords(value), err
}

func (r schedulerAccountSource) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableByPlatforms(ctx, platforms)
	return wrapSchedulerRecords(value), err
}

func (r schedulerAccountSource) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return wrapSchedulerRecords(value), err
}

func (r schedulerAccountSource) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
	return wrapSchedulerRecords(value), err
}

func (r schedulerAccountSource) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, group int64, platforms []string) ([]scheduler.SnapshotAccount, error) {
	value, err := r.AccountStore.ListSchedulableByGroupIDAndPlatforms(ctx, group, platforms)
	return wrapSchedulerRecords(value), err
}

func schedulerSnapshotGroup(value *routing.Group) *scheduler.SnapshotGroup {
	if value == nil {
		return nil
	}
	return &scheduler.SnapshotGroup{ID: value.ID, Name: value.Name, Platform: value.Platform, Status: value.Status, Hydrated: value.Hydrated}
}

type schedulerGroupSource struct{ *routingpostgres.GroupStore }

func (r schedulerGroupSource) GetByID(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	value, err := r.GroupStore.GetByID(ctx, id)
	return schedulerSnapshotGroup(value), err
}

func (r schedulerGroupSource) GetByIDLite(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	value, err := r.GroupStore.GetByIDLite(ctx, id)
	return schedulerSnapshotGroup(value), err
}

func (r schedulerGroupSource) ListActive(ctx context.Context) ([]scheduler.SnapshotGroup, error) {
	value, err := r.GroupStore.ListActive(ctx)
	if value == nil {
		return nil, err
	}
	out := make([]scheduler.SnapshotGroup, len(value))
	for i := range value {
		out[i] = *schedulerSnapshotGroup(&value[i])
	}
	return out, err
}

func (r schedulerGroupSource) ListActiveIDs(ctx context.Context) ([]int64, error) {
	return r.GroupStore.ListActiveIDs(ctx)
}

func wrapSchedulerRecords(values []account.Record) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i := range values {
		out[i] = codec.WrapRecord(&values[i])
	}
	return out
}
