//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package repository

import (
	context "context"
	sql "database/sql"
	postgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
	time "time"
)

func transferTeamOwnership(ctx context.Context, tx *sql.Tx, teamID, fromUserID, toUserID int64, now time.Time) error {
	return postgres.TransferTeamOwnership(ctx, tx, teamID, fromUserID, toUserID, now)
}
