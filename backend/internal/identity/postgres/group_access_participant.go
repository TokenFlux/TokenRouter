// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

// GroupAccessParticipant 只操作调用方已有事务中的授权记录，不提交也不失效缓存。
type GroupAccessParticipant struct {
	tx    *dbent.Tx
	store *UserStore
}

func GroupAccessInTx(tx *dbent.Tx) *GroupAccessParticipant {
	return &GroupAccessParticipant{tx: tx, store: &UserStore{client: tx.Client()}}
}
func (p *GroupAccessParticipant) AddGroupToAllowedGroups(ctx context.Context, userID, groupID int64) error {
	return p.store.AddGroupToAllowedGroups(dbent.NewTxContext(ctx, p.tx), userID, groupID)
}
