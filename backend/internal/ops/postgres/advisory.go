package postgres

import (
	"context"
	"database/sql"

	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// Advisory 复用原 SQL 连接上的技术锁，降级策略由任务决定。
type Advisory struct{ db *sql.DB }

func NewAdvisory(db *sql.DB) ops.AdvisoryLocker {
	if db == nil {
		return nil
	}
	return &Advisory{db}
}
func (a *Advisory) Acquire(ctx context.Context, key string) (func(), bool) {
	return infra.TryAcquireDBAdvisoryLock(ctx, a.db, infra.HashAdvisoryLockID(key))
}
