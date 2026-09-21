// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// UpdateConfiguration 只投影本次配置意图；事务、锁和资金字段保护由唯一新存储执行。
func (r *accountRepository) UpdateConfiguration(ctx context.Context, value *service.Account, change acctcore.ConfigurationChange) error {
	v := service.AccountRecordView(value)
	err := r.accountData().UpdateConfiguration(ctx, v, change)
	service.ApplyAccountRecord(value, v)
	return err
}
