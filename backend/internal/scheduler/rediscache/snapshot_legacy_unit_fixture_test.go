//go:build unit

package rediscache

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

// unit 白盒断言沿用旧账号报文，只委托新缓存与兼容 codec。
func newSchedulerCacheWithChunkSizes(rdb *redis.Client, read, write int) service.SchedulerCache {
	return &schedulerCache{NewSnapshotCache(rdb, service.LegacySchedulerCodec{}, SnapshotCacheOptions{MGetChunkSize: read, WriteChunkSize: write})}
}
func (c *schedulerCache) writeAccountIDs(ctx context.Context, values []service.Account) ([]int64, error) {
	return c.SnapshotCache.writeAccountIDs(ctx, service.LegacySnapshotWrapValues(values))
}
func (c *schedulerCache) writeSnapshotVersionAndReturnAccountIDs(ctx context.Context, bucket service.SchedulerBucket, version string, values []service.Account) ([]int64, error) {
	return c.SnapshotCache.writeSnapshotVersionAndReturnAccountIDs(ctx, bucket, version, service.LegacySnapshotWrapValues(values))
}
func marshalSchedulerCacheAccount(value service.Account) ([]byte, []byte, error) {
	return (service.LegacySchedulerCodec{}).Encode(service.LegacySnapshotWrap(&value))
}
