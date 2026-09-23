package creative

import (
	"context"

	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var (
	ErrCreativeQueueEmpty          = infraerrors.New(infraerrors.CategoryNotFound, "CREATIVE_QUEUE_EMPTY", "creative queue is empty")
	ErrCreativeAlreadyQueued       = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_ALREADY_QUEUED", "creative run is already queued")
	ErrCreativeLockNotAcquired     = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_LOCK_NOT_ACQUIRED", "creative run lock was not acquired")
	ErrCreativeLeaseLost           = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_LEASE_LOST", "creative run lease is no longer owned")
	ErrInvalidCreativeQueuePayload = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_QUEUE_INVALID_PAYLOAD", "invalid creative queue payload")
)

// ReservedCreativeRun 是队列保留的一次任务。
type ReservedCreativeRun struct {
	RunID      string
	LeaseToken string
}

// CreativeRunJobLock 是任务级分布式锁。
type CreativeRunJobLock interface {
	Release(ctx context.Context) error
}

// CreativeRunJobLockRefresher 是可选的锁续期能力；由具体锁实现按需提供。
type CreativeRunJobLockRefresher interface {
	Refresh(ctx context.Context, ttl time.Duration) (bool, error)
}

// CreativeRunQueue 是创作台任务的 Redis 队列抽象。
// 实现位于 internal/creative/rediscache/queue.go，语义与批量图片队列一致。
type CreativeRunQueue interface {
	Enqueue(ctx context.Context, runID string) error
	Reserve(ctx context.Context, blockTimeout time.Duration) (ReservedCreativeRun, error)
	RequeueAfter(ctx context.Context, runID, leaseToken string, delay time.Duration) error
	Ack(ctx context.Context, runID, leaseToken string) error
	Heartbeat(ctx context.Context, runID, leaseToken string) (bool, error)
	MoveDueDelayedToReady(ctx context.Context, limit int) (int, error)
	RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error)
	TryAcquireJobLock(ctx context.Context, runID string, ttl time.Duration) (CreativeRunJobLock, bool, error)
}

// CreativeTransientStore 是创作台临时 Redis 存储抽象。
// 输入载荷与输出图片本体只保存在这里，TTL 到期即失效，绝不允许落库。
type CreativeTransientStore interface {
	// SavePayload 保存任务载荷（prompt 与元数据），TTL 为配置的 transient_ttl_seconds。
	SavePayload(ctx context.Context, runID string, payload *CreativeRunPayload) error
	// LoadPayload 读取任务载荷；不存在或过期返回明确错误。
	LoadPayload(ctx context.Context, runID string) (*CreativeRunPayload, error)
	// SaveInput 保存一张源图字节，idx 从 0 开始。
	SaveInput(ctx context.Context, runID string, idx int, data []byte) error
	// LoadInputs 按数量读取全部源图字节；缺失任一张都视为失败。
	LoadInputs(ctx context.Context, runID string, count int) ([][]byte, error)
	// SaveMask 保存 mask 字节。
	SaveMask(ctx context.Context, runID string, data []byte) error
	// LoadMask 读取 mask 字节；任务无 mask 时返回明确错误。
	LoadMask(ctx context.Context, runID string) ([]byte, error)
	// SaveOutput 保存一张临时输出图片，写入时设置独立 TTL。
	SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error
	// LoadOutput 读取临时输出图片；不存在或过期返回明确错误。
	LoadOutput(ctx context.Context, runID string, index int) ([]byte, error)
	// DeleteOutput 删除单张临时输出（客户端 ack 或清理），幂等。
	DeleteOutput(ctx context.Context, runID string, index int) error
	// DeleteRunTransient 删除任务的全部临时数据（payload/inputs/mask/outputs），幂等。
	DeleteRunTransient(ctx context.Context, runID string, inputCount, outputCount int) error
}
