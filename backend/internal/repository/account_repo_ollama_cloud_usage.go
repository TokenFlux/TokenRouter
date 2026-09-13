// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

func (r *accountRepository) ListOllamaCloudUsageGroupAccounts(ctx context.Context, accounts []*service.Account) ([]service.Account, error) {
	values := make([]*acctcore.Record, len(accounts))
	for i := range accounts {
		values[i] = service.AccountRecordView(accounts[i])
	}
	v, err := r.accountData().ListOllamaCloudUsageGroupAccounts(ctx, values)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) SaveOllamaCloudUsageSession(ctx context.Context, account *service.Account, ciphertext string, autoRefresh bool) error {
	return r.accountData().SaveOllamaCloudUsageSession(ctx, service.AccountRecordView(account), ciphertext, autoRefresh)
}

func (r *accountRepository) DeleteOllamaCloudUsageSession(ctx context.Context, account *service.Account) error {
	return r.accountData().DeleteOllamaCloudUsageSession(ctx, service.AccountRecordView(account))
}

func (r *accountRepository) SetOllamaCloudUsageAutoRefresh(ctx context.Context, account *service.Account, enabled bool) error {
	return r.accountData().SetOllamaCloudUsageAutoRefresh(ctx, service.AccountRecordView(account), enabled)
}

func (r *accountRepository) UpdateOllamaCloudUsageSnapshot(ctx context.Context, account *service.Account, snapshot *service.OllamaCloudUsageSnapshot) error {
	return r.accountData().UpdateOllamaCloudUsageSnapshot(ctx, service.AccountRecordView(account), snapshot)
}

func (r *accountRepository) DisableOllamaCloudUsageAutoRefresh(ctx context.Context, account *service.Account) error {
	return r.accountData().DisableOllamaCloudUsageAutoRefresh(ctx, service.AccountRecordView(account))
}

func (r *accountRepository) ListDueOllamaCloudUsageAccounts(
	ctx context.Context,
	now time.Time,
	debounce, maxWait time.Duration,
	limit int,
) ([]service.Account, error) {
	v, err := r.accountData().ListDueOllamaCloudUsageAccounts(ctx, now, debounce, maxWait, limit)
	return service.AccountsFromRecords(v), err
}
