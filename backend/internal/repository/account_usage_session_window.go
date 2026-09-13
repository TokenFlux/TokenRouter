package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// UpdateUsageSessionWindowEndIfUnchanged 旧入口只转交唯一账号存储。
func (r *accountRepository) UpdateUsageSessionWindowEndIfUnchanged(ctx context.Context, v account.UsageObservationVersion, observed *time.Time, end time.Time) (bool, error) {
	return r.accountData().UpdateUsageSessionWindowEndIfUnchanged(ctx, v, observed, end)
}
