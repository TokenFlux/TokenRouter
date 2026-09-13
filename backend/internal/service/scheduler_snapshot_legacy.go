// 本文件仅适配旧快照数据形状与调用，不持有调度规则、缓存或锁。
package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type legacySnapshotAccount struct{ value *Account }

func (a legacySnapshotAccount) SnapshotMetadata() scheduler.SnapshotMetadata {
	return scheduler.SnapshotMetadata{ID: a.value.ID, Name: a.value.Name, Platform: a.value.Platform, GroupIDs: slices.Clone(a.value.GroupIDs), MixedScheduling: a.value.IsMixedSchedulingEnabled()}
}
func LegacySnapshotWrap(value *Account) scheduler.SnapshotAccount {
	if value == nil {
		return nil
	}
	return legacySnapshotAccount{value: value}
}
func LegacySnapshotWrapValues(values []Account) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i := range values {
		value := values[i]
		out[i] = LegacySnapshotWrap(&value)
	}
	return out
}
func LegacySnapshotWrapPointers(values []*Account) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i, v := range values {
		out[i] = LegacySnapshotWrap(v)
	}
	return out
}
func LegacySnapshotValue(value scheduler.SnapshotAccount) (*Account, error) {
	if value == nil {
		return nil, nil
	}
	v, ok := value.(legacySnapshotAccount)
	if !ok {
		return nil, fmt.Errorf("unexpected legacy scheduler snapshot data %T", value)
	}
	return v.value, nil
}
func LegacySnapshotValues(values []scheduler.SnapshotAccount) ([]Account, error) {
	if values == nil {
		return nil, nil
	}
	out := make([]Account, 0, len(values))
	for _, v := range values {
		a, err := LegacySnapshotValue(v)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, *a)
		}
	}
	return out, nil
}

type legacySnapshotCache struct{ SchedulerCache }

func (c legacySnapshotCache) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
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
func (c legacySnapshotCache) SetSnapshot(ctx context.Context, bucket SchedulerBucket, token SchedulerBucketWriteToken, values []scheduler.SnapshotAccount) error {
	rows, err := LegacySnapshotValues(values)
	if err != nil {
		return err
	}
	return c.SchedulerCache.SetSnapshot(ctx, bucket, token, rows)
}

type legacySnapshotAccountIDWriter interface {
	SetSnapshotAndReturnAccountIDs(context.Context, SchedulerBucket, SchedulerBucketWriteToken, []Account) ([]int64, error)
	SetSnapshotByAccountIDs(context.Context, SchedulerBucket, SchedulerBucketWriteToken, []int64) error
}
type legacySnapshotCacheWithIDs struct {
	legacySnapshotCache
	writer legacySnapshotAccountIDWriter
}

func (c legacySnapshotCacheWithIDs) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket SchedulerBucket, token SchedulerBucketWriteToken, values []scheduler.SnapshotAccount) ([]int64, error) {
	rows, err := LegacySnapshotValues(values)
	if err != nil {
		return nil, err
	}
	return c.writer.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, rows)
}
func (c legacySnapshotCacheWithIDs) SetSnapshotByAccountIDs(ctx context.Context, bucket SchedulerBucket, token SchedulerBucketWriteToken, ids []int64) error {
	return c.writer.SetSnapshotByAccountIDs(ctx, bucket, token, ids)
}

type legacySnapshotAccounts struct{ AccountRepository }

func (r legacySnapshotAccounts) GetByID(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.GetByID(ctx, id)
	return LegacySnapshotWrap(v), err
}
func (r legacySnapshotAccounts) GetByIDs(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.GetByIDs(ctx, ids)
	return LegacySnapshotWrapPointers(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableByPlatform(ctx, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableUngroupedByPlatform(ctx, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableByPlatforms(ctx, platforms)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
	return LegacySnapshotWrapValues(v), err
}
func (r legacySnapshotAccounts) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, group int64, platforms []string) ([]scheduler.SnapshotAccount, error) {
	v, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatforms(ctx, group, platforms)
	return LegacySnapshotWrapValues(v), err
}

func legacySnapshotGroup(v *Group) *scheduler.SnapshotGroup {
	if v == nil {
		return nil
	}
	return &scheduler.SnapshotGroup{ID: v.ID, Name: v.Name, Platform: v.Platform, Status: v.Status, Hydrated: v.Hydrated}
}

type legacySnapshotGroups struct{ GroupRepository }

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
