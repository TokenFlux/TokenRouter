package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// UpdateOAuthCredentialsIfUnchanged 过渡绑定唯一条件写入与原凭据/outbox 方法；不新增事务 key。
func (r *accountRepository) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version account.CredentialVersion, credentials map[string]any) (bool, error) {
	return r.accountData().UpdateOAuthCredentialsIfUnchanged(ctx, version, credentials)
}
