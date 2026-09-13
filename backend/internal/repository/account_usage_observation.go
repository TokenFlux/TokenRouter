package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// 旧用量调用仅转交唯一账号存储的条件操作，S09/S16 清理旧适配。
func (r *accountRepository) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	return r.accountData().UpdateUsageExtraIfUnchanged(ctx, v, updates)
}
func (r *accountRepository) SetUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion, reset time.Time) (bool, error) {
	return r.accountData().SetUsageRateLimitIfUnchanged(ctx, v, reset)
}
func (r *accountRepository) ClearUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion) (bool, error) {
	return r.accountData().ClearUsageRateLimitIfUnchanged(ctx, v)
}
