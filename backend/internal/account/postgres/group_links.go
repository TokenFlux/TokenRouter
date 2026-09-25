// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"database/sql"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/lib/pq"
)

// GroupLinks 只在调用方连接维护账号关联，不拥有提交或失效。
type GroupLinks struct{ exec postgresinfra.Executor }

func GroupLinksInTx(exec postgresinfra.Executor) GroupLinks { return GroupLinks{exec: exec} }
func (p GroupLinks) Clear(ctx context.Context, groupID int64) (sql.Result, error) {
	return p.exec.ExecContext(ctx, "DELETE FROM account_groups WHERE group_id = $1", groupID)
}
func (p GroupLinks) Bind(ctx context.Context, groupID int64, accountIDs []int64) error {
	_, err := p.exec.ExecContext(
		ctx,
		`INSERT INTO account_groups (account_id, group_id, created_at)
		 SELECT unnest($1::bigint[]), $2, NOW()
		 ON CONFLICT (account_id, group_id) DO NOTHING`,
		pq.Array(accountIDs),
		groupID,
	)
	return err
}
func (p GroupLinks) Copy(ctx context.Context, targetID, sourceGroupID int64, oauthOnly bool) (sql.Result, error) {
	return p.exec.ExecContext(
		ctx,
		`INSERT INTO account_groups (account_id, group_id, created_at)
		 SELECT ag.account_id, $2, NOW()
		 FROM account_groups ag
		 JOIN accounts a ON a.id = ag.account_id
		 WHERE ag.group_id = $1
		   AND a.deleted_at IS NULL
		   AND (NOT $3 OR a.type <> $4)
		 ON CONFLICT (account_id, group_id) DO NOTHING`,
		sourceGroupID,
		targetID,
		oauthOnly,
		"apikey",
	)
}
