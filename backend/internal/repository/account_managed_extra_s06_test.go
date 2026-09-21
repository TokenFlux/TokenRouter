package repository

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// 原 SQL 契约测试只投影到新存储的事务内操作，生产不保留第二份合并算法。
func lockAndMergeAccountManagedExtra(ctx context.Context, client *dbent.Client, account *service.Account) (map[string]any, error) {
	repo := &accountRepository{client: client}
	return repo.accountData().LockAndMergeAccountManagedExtra(ctx, client, service.AccountRecordView(account))
}
