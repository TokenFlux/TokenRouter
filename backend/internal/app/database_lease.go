package app

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// databaseAdvisoryLease 只绑定已有连接来源；是否回退与锁身份仍由各调用方决定。
func databaseAdvisoryLease(db *sql.DB) func(context.Context, string) (func(), bool) {
	if db == nil {
		return nil
	}
	return func(ctx context.Context, key string) (func(), bool) {
		return postgres.TryAcquireDBAdvisoryLock(ctx, db, postgres.HashAdvisoryLockID(key))
	}
}
