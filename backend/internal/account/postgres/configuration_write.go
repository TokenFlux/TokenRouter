// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// UpdateConfiguration 与普通更新共享一次事务和同连接 outbox，不独立发布成功事件。
func (r *AccountStore) UpdateConfiguration(ctx context.Context, value *acctcore.Record, change acctcore.ConfigurationChange) error {
	return r.updateAccount(ctx, value, &change)
}
func (r *AccountStore) lockConfigurationRecord(ctx context.Context, client *dbent.Client, id int64) (*acctcore.Record, error) {
	rows, err := client.QueryContext(ctx, "SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE", id)
	if err != nil {
		return nil, err
	}
	found := rows.Next()
	scanErr := rows.Err()
	if found {
		var locked int64
		scanErr = rows.Scan(&locked)
	}
	closeErr := rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if !found {
		return nil, acctcore.ErrAccountNotFound
	}
	entity, err := client.Account.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.recordFromEntity(entity), nil
}
