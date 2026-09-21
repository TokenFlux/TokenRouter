//go:build integration

package repository

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// newGroupStoreFixture 只装配测试事务参与者，所有存取规则由原生存储执行。
func newGroupStoreFixture(client *dbent.Client, db postgresinfra.Executor) *routingpostgres.GroupStore {
	return routingpostgres.NewGroupStore(client, db, routingpostgres.GroupStoreOptions{
		Accounts: func(exec postgresinfra.Executor) routingpostgres.GroupLinkParticipant {
			return accountpostgres.GroupLinksInTx(exec)
		},
		Users: func(exec postgresinfra.Executor) routingpostgres.GroupAccessParticipant {
			return identitypostgres.GroupAccessDeletionInTx(exec)
		},
		Enqueue: func(ctx context.Context, exec postgresinfra.Executor, id *int64) error {
			return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, scheduler.SchedulerOutboxEventGroupChanged, nil, id, nil)
		},
	})
}
