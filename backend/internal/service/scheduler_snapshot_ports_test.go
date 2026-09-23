// 本文件仅适配旧快照数据形状与调用，不持有调度规则、缓存或锁。
package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type legacySnapshotCache struct{ SchedulerCache }

func (c legacySnapshotCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
	values, hit, err := c.SchedulerCache.GetSnapshot(ctx, bucket)
	return LegacySnapshotWrapPointers(values), hit, err
}
func (c legacySnapshotCache) GetAccount(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	v, err := c.SchedulerCache.GetAccount(ctx, id)
	return LegacySnapshotWrap(v), err
}
func (c legacySnapshotCache) SetAccount(ctx context.Context, v scheduler.SnapshotAccount) error {
	value, err := LegacySnapshotValue(v)
	if err != nil {
		return err
	}
	return c.SchedulerCache.SetAccount(ctx, value)
}
func (c legacySnapshotCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []scheduler.SnapshotAccount) error {
	rows, err := LegacySnapshotValues(values)
	if err != nil {
		return err
	}
	return c.SchedulerCache.SetSnapshot(ctx, bucket, token, rows)
}

type legacySnapshotAccountIDWriter interface {
	SetSnapshotAndReturnAccountIDs(context.Context, scheduler.SchedulerBucket, scheduler.SchedulerBucketWriteToken, []gatewayprovider.ExecutionAccount) ([]int64, error)
	SetSnapshotByAccountIDs(context.Context, scheduler.SchedulerBucket, scheduler.SchedulerBucketWriteToken, []int64) error
}
type legacySnapshotCacheWithIDs struct {
	legacySnapshotCache
	writer legacySnapshotAccountIDWriter
}

func (c legacySnapshotCacheWithIDs) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []scheduler.SnapshotAccount) ([]int64, error) {
	rows, err := LegacySnapshotValues(values)
	if err != nil {
		return nil, err
	}
	return c.writer.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, rows)
}
func (c legacySnapshotCacheWithIDs) SetSnapshotByAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, ids []int64) error {
	return c.writer.SetSnapshotByAccountIDs(ctx, bucket, token, ids)
}

type legacySnapshotAccounts struct {
	gatewayprovider.ExecutionAccountStore
}

func (r legacySnapshotAccounts) GetByID(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.GetByID(ctx, id)
	return LegacySnapshotWrap(v), err
}
func (r legacySnapshotAccounts) GetByIDs(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.GetByIDs(ctx, ids)
	return LegacySnapshotWrapPointers(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableByPlatform(ctx, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableUngroupedByPlatform(ctx, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableByPlatforms(ctx, platforms)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, group int64, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.ExecutionAccountStore.ListSchedulableByGroupIDAndPlatforms(ctx, group, platforms)
	return LegacySnapshotWrapValues(v), err
}

func legacySnapshotGroup(v *routing.Group) *scheduler.SnapshotGroup {
	if v == nil {
		return nil
	}
	return &scheduler.SnapshotGroup{ID: v.ID, Name: v.Name, Platform: v.Platform, Status: v.Status, Hydrated: v.Hydrated}
}

type legacySnapshotGroups struct{ routing.GroupRepository }

func (r legacySnapshotGroups) GetByID(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	v, err := r.GroupRepository.GetByID(ctx, id)
	return legacySnapshotGroup(v), err
}
func (r legacySnapshotGroups) GetByIDLite(ctx context.Context, id int64) (*scheduler.SnapshotGroup, error) {
	v, err := r.GroupRepository.GetByIDLite(ctx, id)
	return legacySnapshotGroup(v), err
}
func (r legacySnapshotGroups) ListActive(ctx context.Context) ([]scheduler.SnapshotGroup, error) {
	v, err := r.GroupRepository.ListActive(ctx)
	if v == nil {
		return nil, err
	}
	out := make([]scheduler.SnapshotGroup, len(v))
	for i := range v {
		out[i] = *legacySnapshotGroup(&v[i])
	}
	return out, err
}

type legacySnapshotGroupsWithIDs struct {
	legacySnapshotGroups
	list func(context.Context) ([]int64, error)
}

func (r legacySnapshotGroupsWithIDs) ListActiveIDs(ctx context.Context) ([]int64, error) {
	return r.list(ctx)
}
