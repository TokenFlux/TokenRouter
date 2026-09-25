// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// GroupAccessDeletion 沿用分组删除事务，按原顺序清理两类用户授权。
type GroupAccessDeletion struct{ exec postgresinfra.Executor }

func GroupAccessDeletionInTx(exec postgresinfra.Executor) GroupAccessDeletion {
	return GroupAccessDeletion{exec: exec}
}
func (p GroupAccessDeletion) Delete(ctx context.Context, id int64) error {
	if _, err := p.exec.ExecContext(ctx, "DELETE FROM user_allowed_groups WHERE group_id = $1", id); err != nil {
		return err
	}
	if _, err := p.exec.ExecContext(ctx, "DELETE FROM user_disabled_public_groups WHERE group_id = $1", id); err != nil {
		return err
	}
	return nil
}
