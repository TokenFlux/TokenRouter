// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type legacyConfigurationWriter interface {
	UpdateConfiguration(context.Context, *Account, acctcore.ConfigurationChange) error
}

func updateAccountConfiguration(ctx context.Context, repo AccountRepository, value *Account, change acctcore.ConfigurationChange) error {
	if writer, ok := repo.(legacyConfigurationWriter); ok {
		return writer.UpdateConfiguration(ctx, value, change)
	}
	return repo.Update(ctx, value)
}
func (r legacyAccountAdminStore) UpdateConfiguration(ctx context.Context, value *acctcore.Record, change acctcore.ConfigurationChange) error {
	v := AccountFromRecord(value)
	err := updateAccountConfiguration(ctx, r.AccountRepository, v, change)
	if value != nil && v != nil {
		*value = *AccountRecordView(v)
	}
	return err
}
