package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ApplyManagedRecoveryStep 委托唯一账号存储，不在旧入口复制条件或提交规则。
func (r *accountRepository) ApplyManagedRecoveryStep(ctx context.Context, step account.ManagedRecoveryStep, v account.ManagedRecoveryVersion) (bool, error) {
	return r.accountData().ApplyManagedRecoveryStep(ctx, step, v)
}
