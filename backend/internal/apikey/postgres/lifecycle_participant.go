// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

type LifecycleParticipant struct {
	store *KeyStore
	tx    *dbent.Tx
}

func (r *KeyStore) LifecycleInTx(tx *dbent.Tx) *LifecycleParticipant {
	return &LifecycleParticipant{store: &KeyStore{client: tx.Client(), sql: tx.Client(), usageTotals: r.usageTotals}, tx: tx}
}
func (p *LifecycleParticipant) DeleteWithAudit(ctx context.Context, id int64) error {
	return p.store.KeyDeleteWithTombstone(ctx, p.tx.Client(), id, fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano()))
}
func (p *LifecycleParticipant) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldID, newID int64) (int64, error) {
	return p.store.UpdateGroupIDByUserAndGroup(dbent.NewTxContext(ctx, p.tx), userID, oldID, newID)
}
