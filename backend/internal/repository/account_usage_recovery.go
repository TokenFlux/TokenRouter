package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ClearUsageErrorIfUnchanged 只转交账号存储；原查询用例不得无条件恢复已变化身份。
func (r *accountRepository) ClearUsageErrorIfUnchanged(ctx context.Context, v account.UsageRecoveryVersion) (bool, error) {
	return r.accountData().ClearUsageErrorIfUnchanged(ctx, v)
}
