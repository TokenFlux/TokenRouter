package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 旧成功刷新入口只转交账号所属的条件清理。
func (r *accountRepository) ClearRefreshCooldownIfUnchanged(ctx context.Context, v account.RefreshCooldownVersion) (bool, error) {
	return r.accountData().ClearRefreshCooldownIfUnchanged(ctx, v)
}
