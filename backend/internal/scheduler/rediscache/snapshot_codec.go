package rediscache

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// SnapshotCodec 仅适配既有记录格式，Redis 键、版本、锁和发布顺序由 SnapshotCache 唯一控制。
type SnapshotCodec interface {
	Encode(scheduler.SnapshotAccount) ([]byte, []byte, error)
	Decode(any) (scheduler.SnapshotAccount, error)
	LastUsedAt(scheduler.SnapshotAccount) (*time.Time, error)
	SetLastUsedAt(scheduler.SnapshotAccount, *time.Time) error
}
