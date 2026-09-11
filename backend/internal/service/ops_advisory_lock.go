package service

import (
	"context"
	"database/sql"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// 旧作用域保留命名，锁的技术实现由 infra 提供，S08/S14 清理。
func hashAdvisoryLockID(key string) int64 { return postgresinfra.HashAdvisoryLockID(key) }
func tryAcquireDBAdvisoryLock(ctx context.Context, db *sql.DB, id int64) (func(), bool) {
	return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, id)
}
func tryAcquireDBAdvisoryLockWithError(ctx context.Context, db *sql.DB, id int64) (func(), bool, error) {
	return postgresinfra.TryAcquireDBAdvisoryLockWithError(ctx, db, id)
}
