// 测试只解包受控账号记录，Redis 行为由生产 SnapshotCache 执行。
package rediscache

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

type schedulerCache struct{ *SnapshotCache }

func NewSchedulerCache(rdb *redis.Client) *schedulerCache {
	return &schedulerCache{NewSnapshotCache(rdb, codec.AccountCodec{})}
}
func (c *schedulerCache) SnapshotCoreCache() scheduler.SnapshotCache { return c.SnapshotCache }
func (c *schedulerCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]*accountcore.Record, bool, error) {
	values, hit, err := c.SnapshotCache.GetSnapshot(ctx, bucket)
	if err != nil {
		return nil, hit, err
	}
	if values == nil {
		return nil, hit, nil
	}
	out := make([]*accountcore.Record, len(values))
	for i, v := range values {
		out[i], err = codec.RecordValue(v)
		if err != nil {
			return nil, false, err
		}
	}
	return out, hit, nil
}
func (c *schedulerCache) GetAccount(ctx context.Context, id int64) (*accountcore.Record, error) {
	v, err := c.SnapshotCache.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return codec.RecordValue(v)
}
func (c *schedulerCache) SetAccount(ctx context.Context, v *accountcore.Record) error {
	return c.SnapshotCache.SetAccount(ctx, codec.WrapRecord(v))
}
func (c *schedulerCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []accountcore.Record) error {
	return c.SnapshotCache.SetSnapshot(ctx, bucket, token, snapshotRecords(values))
}
func (c *schedulerCache) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, values []accountcore.Record) ([]int64, error) {
	return c.SnapshotCache.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, snapshotRecords(values))
}

func buildSchedulerMetadataAccount(value accountcore.Record) accountcore.Record {
	return (codec.AccountCodec{}).Metadata(value)
}

func snapshotRecords(values []accountcore.Record) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i := range values {
		out[i] = codec.WrapRecord(&values[i])
	}
	return out
}

func filterSchedulerCredentials(value map[string]any) map[string]any {
	return buildSchedulerMetadataAccount(accountcore.Record{Credentials: value}).Credentials
}
