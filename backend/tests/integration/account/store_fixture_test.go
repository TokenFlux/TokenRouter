package account_test

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// 原存储契约只绑定真实 outbox writer；没有缓存时不引入额外读取。
func newAccountStoreContract(client *dbent.Client, exec postgresinfra.Executor, _ any) *accountpostgres.AccountStore {
	return accountpostgres.NewAccountStore(client, exec, accountpostgres.AccountStoreOptions{
		OllamaIdentity: account.IsOllamaCloudUsageAccount,
		Proxy:          egresspostgres.ProxyEntity,
		Events:         accountEventsFixture{},
	})
}

type accountEventsFixture struct{}

func (accountEventsFixture) Name(event accountpostgres.AccountEvent) string {
	switch event {
	case accountpostgres.AccountChanged:
		return scheduler.SchedulerOutboxEventAccountChanged
	case accountpostgres.AccountGroupsChanged:
		return scheduler.SchedulerOutboxEventAccountGroupsChanged
	case accountpostgres.AccountLastUsed:
		return scheduler.SchedulerOutboxEventAccountLastUsed
	case accountpostgres.AccountBulkChanged:
		return scheduler.SchedulerOutboxEventAccountBulkChanged
	default:
		panic("未知账号事件")
	}
}

func (f accountEventsFixture) Write(ctx context.Context, exec postgresinfra.Executor, event accountpostgres.AccountEvent, id, group *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, f.Name(event), id, group, payload)
}

func (accountEventsFixture) GroupPayload(ids []int64) any      { return scheduler.GroupPayload(ids) }
func (accountEventsFixture) SyncOne(context.Context, int64)    {}
func (accountEventsFixture) SyncMany(context.Context, []int64) {}
func (accountEventsFixture) Drop(context.Context, int64)       {}

func normalizeJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
