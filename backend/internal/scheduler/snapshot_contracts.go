package scheduler

import (
	"context"
	"time"
)

// SnapshotMetadata 是重建和事件展开所需的最小投影，不包含凭据或管理 Extra。
type SnapshotMetadata struct {
	ID              int64
	Name            string
	Platform        string
	GroupIDs        []int64
	MixedScheduling bool
}

// SnapshotAccount 将发布数据保留在 Adapter 中；核心只能查看其重建元数据。
// 同批次复用这个对象不会再次查询数据库或复制完整账号缓存。
type SnapshotAccount interface {
	SnapshotMetadata() SnapshotMetadata
}

// SnapshotGroup 仅表达生命周期判断使用的权威状态。
type SnapshotGroup struct {
	ID       int64
	Hydrated bool
	Name     string
	Platform string
	Status   string
}

func (g *SnapshotGroup) IsActive() bool { return g != nil && g.Status == "active" }

// SnapshotAccountSource 保留原分组/平台查询差异，Adapter 返回可发布的数据拥有者。
type SnapshotAccountSource interface {
	GetByID(context.Context, int64) (SnapshotAccount, error)
	GetByIDs(context.Context, []int64) ([]SnapshotAccount, error)
	ListSchedulableByPlatform(context.Context, string) ([]SnapshotAccount, error)
	ListSchedulableUngroupedByPlatform(context.Context, string) ([]SnapshotAccount, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]SnapshotAccount, error)
	ListSchedulableByPlatforms(context.Context, []string) ([]SnapshotAccount, error)
	ListSchedulableUngroupedByPlatforms(context.Context, []string) ([]SnapshotAccount, error)
	ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]SnapshotAccount, error)
}

type SnapshotGroupSource interface {
	GetByID(context.Context, int64) (*SnapshotGroup, error)
	GetByIDLite(context.Context, int64) (*SnapshotGroup, error)
	ListActive(context.Context) ([]SnapshotGroup, error)
}

// SnapshotOptions 由 app 投影，保留 nil 配置与显式关闭 fallback 的区别。
type SnapshotOptions struct {
	Simple                     bool
	DbFallbackEnabled          bool
	DbFallbackMaxQPS           int
	DbFallbackTimeoutSeconds   int
	OutboxPollIntervalSeconds  int
	FullRebuildIntervalSeconds int
	OutboxLagWarnSeconds       int
	OutboxLagRebuildSeconds    int
	OutboxLagRebuildFailures   int
	OutboxBacklogRebuildRows   int
}

// SnapshotCache 的数据对象仅由存储 Adapter 解码和发布，调度规则不读取其完整内容。
type SnapshotCache interface {
	GetSnapshot(context.Context, SchedulerBucket) ([]SnapshotAccount, bool, error)
	CaptureBucketWriteToken(context.Context, SchedulerBucket) (SchedulerBucketWriteToken, error)
	SetSnapshot(context.Context, SchedulerBucket, SchedulerBucketWriteToken, []SnapshotAccount) error
	RetireBucket(context.Context, SchedulerBucket) error
	ReopenBucket(context.Context, SchedulerBucket) (SchedulerBucketWriteToken, error)
	TryAcquireGroupLifecycleLease(context.Context, int64, time.Duration) (SchedulerGroupLifecycleLease, bool, error)
	ReleaseGroupLifecycleLease(context.Context, SchedulerGroupLifecycleLease) error
	GetAccount(context.Context, int64) (SnapshotAccount, error)
	SetAccount(context.Context, SnapshotAccount) error
	DeleteAccount(context.Context, int64) error
	UpdateLastUsed(context.Context, map[int64]time.Time) error
	AcquireBucketLease(context.Context, SchedulerBucket, time.Duration) (*BucketLease, bool, error)
	ListBuckets(context.Context) ([]SchedulerBucket, error)
	GetOutboxWatermark(context.Context) (int64, error)
	SetOutboxWatermark(context.Context, int64) error
}

// SnapshotBindings 注入错误身份与观察端口，独立于 nil 配置的历史语义。
type SnapshotBindings struct {
	AccountNotFound error
	GroupNotFound   error
	Diagnostics     Diagnostics
}
