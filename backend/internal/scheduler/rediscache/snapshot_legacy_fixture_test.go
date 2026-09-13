// 旧缓存入口仅转换账号数据形状，Redis 与发布实现唯一位于 scheduler/rediscache。
package rediscache

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

type schedulerCache struct{ *SnapshotCache }

func NewSchedulerCache(rdb *redis.Client) service.SchedulerCache {
	return &schedulerCache{NewSnapshotCache(rdb, service.LegacySchedulerCodec{})}
}
func (c *schedulerCache) SnapshotCoreCache() scheduler.SnapshotCache { return c.SnapshotCache }
func (c *schedulerCache) GetSnapshot(ctx context.Context, bucket service.SchedulerBucket) ([]*service.Account, bool, error) {
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
func (c *schedulerCache) SetSnapshot(ctx context.Context, bucket service.SchedulerBucket, token service.SchedulerBucketWriteToken, values []service.Account) error {
	return c.SnapshotCache.SetSnapshot(ctx, bucket, token, service.LegacySnapshotWrapValues(values))
}
func (c *schedulerCache) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket service.SchedulerBucket, token service.SchedulerBucketWriteToken, values []service.Account) ([]int64, error) {
	return c.SnapshotCache.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, service.LegacySnapshotWrapValues(values))
}

func buildSchedulerMetadataAccount(value service.Account) service.Account {
	return (service.LegacySchedulerCodec{}).Metadata(value)
}

func filterSchedulerCredentials(value map[string]any) map[string]any {
	return buildSchedulerMetadataAccount(service.Account{Credentials: value}).Credentials
}
