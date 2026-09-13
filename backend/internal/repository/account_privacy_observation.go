// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

// UpdatePrivacyModeIfUnchanged 只转交原兼容仓储消费者，条件 SQL 唯一归账号存储。
func (r *accountRepository) UpdatePrivacyModeIfUnchanged(ctx context.Context, v account.UsageObservationVersion, mode string) (bool, error) {
	return r.accountData().UpdatePrivacyModeIfUnchanged(ctx, v, mode)
}
