package account_test

import (
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"

	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// 代理身份改变仍由同连接账号参与者清理快照，不另开提交。
func newProxyStoreContract(client *dbent.Client, exec postgresinfra.Executor) *egresspostgres.ProxyStore {
	return egresspostgres.NewProxyStore(client, exec, egresspostgres.ProxyStoreOptions{
		Accounts: func(tx postgresinfra.Executor) egresspostgres.ProxyAccountParticipant {
			return accountpostgres.ProxyChangesInTx(tx)
		},
		Enqueue: func(ctx context.Context, tx postgresinfra.Executor, payload any) error {
			return schedulerpostgres.EnqueueSchedulerChange(ctx, tx, scheduler.SchedulerOutboxEventAccountBulkChanged, nil, nil, payload)
		},
	})
}
