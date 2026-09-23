package account_test

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

// 原存储契约只绑定真实 outbox writer；没有缓存时不引入额外读取。
func newAccountStoreContract(client *dbent.Client, exec postgresinfra.Executor, cache scheduler.SnapshotPublicationCache) *accountpostgres.AccountStore {
	store := accountpostgres.NewAccountStore(client, exec, accountpostgres.AccountStoreOptions{
		Group: func(g *dbent.Group) *accessview.GroupConfig {
			return (*accessview.GroupConfig)(routingpostgres.GroupFromEnt(g))
		},
		OllamaIdentity: account.IsOllamaCloudUsageAccount,
		Proxy:          egresspostgres.ProxyEntity,
		Events:         accountEventsFixture{},
	})
	store.SetEvents(accountPublicationEvents(store, cache))
	return store
}

type accountEventsFixture struct{ publisher scheduler.SnapshotPublisher }

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

func (accountEventsFixture) GroupPayload(ids []int64) any            { return scheduler.GroupPayload(ids) }
func (f accountEventsFixture) SyncOne(ctx context.Context, id int64) { f.publisher.Publish(ctx, id) }
func (f accountEventsFixture) SyncMany(ctx context.Context, ids []int64) {
	f.publisher.PublishMany(ctx, ids)
}
func (f accountEventsFixture) Drop(ctx context.Context, id int64) { f.publisher.Drop(ctx, id) }

// 夹具只连接实际 outbox 与发布实现，不复制锁、编码或事件合并规则。
func accountPublicationEvents(store *accountpostgres.AccountStore, cache scheduler.SnapshotPublicationCache) accountEventsFixture {
	return accountEventsFixture{publisher: scheduler.SnapshotPublisher{
		Cache: cache,
		Read: func(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
			v, err := store.GetByID(ctx, id)
			return codec.WrapRecord(v), err
		},
		ReadMany: func(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
			values, err := store.GetByIDs(ctx, ids)
			if values == nil {
				return nil, err
			}
			out := make([]scheduler.SnapshotAccount, len(values))
			for i, value := range values {
				out[i] = codec.WrapRecord(value)
			}
			return out, err
		},
	}}
}

func normalizeJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
