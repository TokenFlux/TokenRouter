//go:build integration

package account_test

import (
	"context"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 资金参与夹具仅供真实存储测试使用，与消费者保持相同构建条件。
func newAccountUsageContract(exec postgresinfra.Executor, store *accountpostgres.AccountStore, cache scheduler.SnapshotPublicationCache) *billingpostgres.AccountUsageStore {
	events := accountPublicationEvents(store, cache)
	return billingpostgres.NewAccountUsageStore(exec, billingpostgres.AccountUsageOptions{
		Changed: func(ctx context.Context, id int64) error {
			return events.Write(ctx, exec, accountpostgres.AccountChanged, &id, nil, nil)
		},
		Sync: events.SyncOne,
	})
}
