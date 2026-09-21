package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// AccountUsagePublisher 保留原账号事件名称及日志来源，调度发布实现在 S07 改绑。
func AccountUsagePublisher(events accountpostgres.AccountEvents, exec postgresinfra.Executor) billingpostgres.AccountUsageOptions {
	return billingpostgres.AccountUsageOptions{
		Changed: func(ctx context.Context, id int64) error {
			return events.Write(ctx, exec, accountpostgres.AccountChanged, &id, nil, nil)
		},
		Sync:    events.SyncOne,
		Observe: func(format string, args ...any) { logging.LegacyPrintf("repository.account", format, args...) },
	}
}
func (r *accountRepository) accountUsage() *billingpostgres.AccountUsageStore {
	if r.usage != nil {
		return r.usage
	}
	return billingpostgres.NewAccountUsageStore(r.sql, AccountUsagePublisher(AccountEventBinding{Read: r.GetByID, ReadMany: r.GetByIDs, Cache: r.schedulerCache}, r.sql))
}
