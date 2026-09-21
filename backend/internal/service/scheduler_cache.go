// 调度 bucket/代际契约由 scheduler 唯一拥有；旧账号缓存接口随生产读取链迁移。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// SchedulerCache 负责调度快照与账号快照的缓存读写。
type SchedulerCache interface {
	// GetSnapshot 读取快照并返回命中与否（ready + active + 数据完整）。
	GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]*Account, bool, error)
	// CaptureBucketWriteToken 读取当前开放 epoch 且不改变退休状态；已设置墓碑的桶返回 ErrSchedulerBucketRetired。
	CaptureBucketWriteToken(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error)
	// SetSnapshot 写入快照并切换激活版本。token 必须在 DB load/任务排队前取得。
	SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, accounts []Account) error
	// RetireBucket 持久化桶的退休墓碑，并隔离所有旧 writer。
	// 退休前已取得激活版本的 reader 可以完成；新 reader 会看到 ready/active 不存在。
	RetireBucket(ctx context.Context, bucket scheduler.SchedulerBucket) error
	// ReopenBucket 是唯一允许清除墓碑的操作，并返回 RetireBucket 建立的退休代际；
	// 同一代际的重复调用保持幂等。调用方必须在同一个分组生命周期租约内完成最新权威状态检查，
	// 并串行执行 ReopenBucket 与 RetireBucket；普通重建路径不得调用 ReopenBucket。
	ReopenBucket(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error)
	// TryAcquireGroupLifecycleLease 在多实例间串行化非零分组的权威退休/重开决策。
	TryAcquireGroupLifecycleLease(ctx context.Context, groupID int64, ttl time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error)
	// ReleaseGroupLifecycleLease 仅在所有者 token 仍匹配时释放租约，避免过期持有者删除继任租约；
	// 租约缺失、过期、不匹配或已释放时返回 ErrSchedulerGroupLifecycleLeaseLost。
	ReleaseGroupLifecycleLease(ctx context.Context, lease scheduler.SchedulerGroupLifecycleLease) error
	// GetAccount 获取单账号快照。
	GetAccount(ctx context.Context, accountID int64) (*Account, error)
	// SetAccount 写入单账号快照（包含不可调度状态）。
	SetAccount(ctx context.Context, account *Account) error
	// DeleteAccount 删除单账号快照。
	DeleteAccount(ctx context.Context, accountID int64) error
	// UpdateLastUsed 批量更新账号的最后使用时间。
	UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error
	// AcquireBucketLease 返回本次持有者的释放句柄，禁止只按桶名删除锁。
	AcquireBucketLease(context.Context, scheduler.SchedulerBucket, time.Duration) (*scheduler.BucketLease, bool, error)
	// ListBuckets 返回已注册的分桶集合。
	ListBuckets(ctx context.Context) ([]scheduler.SchedulerBucket, error)
	// GetOutboxWatermark 读取 outbox 水位。
	GetOutboxWatermark(ctx context.Context) (int64, error)
	// SetOutboxWatermark 保存 outbox 水位。
	SetOutboxWatermark(ctx context.Context, id int64) error
}
