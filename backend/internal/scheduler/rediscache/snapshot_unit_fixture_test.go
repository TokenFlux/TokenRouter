//go:build unit

package rediscache

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// unit 白盒断言沿用旧账号报文，只委托新缓存与兼容 codec。
func newSchedulerCacheWithChunkSizes(rdb *redis.Client, read, write int) *schedulerCache {
	return &schedulerCache{NewSnapshotCache(rdb, codec.AccountCodec{}, SnapshotCacheOptions{MGetChunkSize: read, WriteChunkSize: write})}
}
func (c *schedulerCache) writeAccountIDs(ctx context.Context, values []accountcore.Record) ([]int64, error) {
	return c.SnapshotCache.writeAccountIDs(ctx, snapshotRecords(values))
}
func (c *schedulerCache) writeSnapshotVersionAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, version string, values []accountcore.Record) ([]int64, error) {
	return c.SnapshotCache.writeSnapshotVersionAndReturnAccountIDs(ctx, bucket, version, snapshotRecords(values))
}
func marshalSchedulerCacheAccount(value accountcore.Record) ([]byte, []byte, error) {
	return (codec.AccountCodec{}).Encode(codec.WrapRecord(&value))
}
