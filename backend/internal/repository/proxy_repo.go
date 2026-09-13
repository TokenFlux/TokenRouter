// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type proxyRepository = egresspostgres.ProxyStore

const proxyAccountOutboxChunkSize = 500

// NewProxyRepository 保留旧构造形状；同连接参与者由装配明确绑定。
func NewProxyRepository(client *dbent.Client, db *sql.DB) service.ProxyRepository {
	return newProxyRepositoryWithSQL(client, db)
}
func newProxyRepositoryWithSQL(client *dbent.Client, db sqlExecutor) *proxyRepository {
	return egresspostgres.NewProxyStore(client, db, egresspostgres.ProxyStoreOptions{
		Accounts: func(exec postgresinfra.Executor) egresspostgres.ProxyAccountParticipant {
			return accountpostgres.ProxyChangesInTx(exec)
		},
		Enqueue: func(ctx context.Context, exec postgresinfra.Executor, payload any) error {
			return enqueueSchedulerOutbox(ctx, exec, service.SchedulerOutboxEventAccountBulkChanged, nil, nil, payload)
		},
	})
}
