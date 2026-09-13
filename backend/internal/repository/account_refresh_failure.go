package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ApplyOAuthRefreshFailure 只转交新账号存储的条件写入，旧后台服务不再用无条件健康更新。
func (r *accountRepository) ApplyOAuthRefreshFailure(ctx context.Context, version account.RefreshFailureVersion, failure account.RefreshFailure) (bool, error) {
	return r.accountData().ApplyOAuthRefreshFailure(ctx, version, failure)
}

func (r *accountRepository) ClearAntigravityRefreshRequest(ctx context.Context, version account.CredentialVersion) (bool, error) {
	return r.accountData().ClearAntigravityRefreshRequest(ctx, version)
}
