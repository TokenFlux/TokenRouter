// 本文件为未迁仓储保留同连接 outbox 转接；编码和 SQL 唯一位于 scheduler/postgres。
package repository

import (
	"context"
	"database/sql"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

func NewSchedulerOutboxRepository(db *sql.DB) scheduler.SchedulerOutboxRepository {
	return schedulerpostgres.NewSchedulerOutboxRepository(db)
}
func enqueueSchedulerOutbox(ctx context.Context, exec sqlExecutor, eventType string, accountID, groupID *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, eventType, accountID, groupID, payload)
}
func EnqueueAccountQuotaChangedInTx(ctx context.Context, tx *sql.Tx, id int64) error {
	return schedulerpostgres.EnqueueAccountQuotaChangedInTx(ctx, tx, id)
}
func EnqueueSchedulerChange(ctx context.Context, exec postgresinfra.Executor, eventType string, accountID, groupID *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, eventType, accountID, groupID, payload)
}
