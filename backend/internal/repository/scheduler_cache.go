// 旧缓存入口仅转换账号数据形状，Redis 与发布实现唯一位于 scheduler/rediscache。
package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

type schedulerCache struct{ *schedulerredis.SnapshotCache }

func NewSchedulerCache(rdb *redis.Client) service.SchedulerCache {
	return &schedulerCache{schedulerredis.NewSnapshotCache(rdb, codec.AccountCodec{})}
}
func (c *schedulerCache) SnapshotCoreCache() scheduler.SnapshotCache { return c.SnapshotCache }
func (c *schedulerCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]*service.Account, bool, error) {
	values, hit, err := c.SnapshotCache.GetSnapshot(ctx, bucket)
	if err != nil {
		return nil, hit, err
	}
	if values == nil {
		return nil, hit, nil
	}
	out := make([]*service.Account, len(values))
	for i, v := range values {
		out[i], err = service.LegacySnapshotValue(v)
		if err != nil {
			return nil, false, err
		}
	}
	return out, hit, nil
}
func (c *schedulerCache) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	v, err := c.SnapshotCache.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return service.LegacySnapshotValue(v)
}
func (c *schedulerCache) SetAccount(ctx context.Context, v *service.Account) error {
	return c.SnapshotCache.SetAccount(ctx, service.LegacySnapshotWrap(v))
}
func (c *schedulerCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []service.Account) error {
	return c.SnapshotCache.SetSnapshot(ctx, bucket, token, service.LegacySnapshotWrapValues(values))
}
func (c *schedulerCache) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []service.Account) ([]int64, error) {
	return c.SnapshotCache.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, service.LegacySnapshotWrapValues(values))
}

// WrapSchedulerCache 保留旧报文接口，使用 app 唯一 Redis 快照实例。
func WrapSchedulerCache(core *schedulerredis.SnapshotCache) service.SchedulerCache {
	return &schedulerCache{core}
}
