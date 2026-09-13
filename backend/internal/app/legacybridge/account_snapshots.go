// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type AccountRecordsReader interface {
	GetByID(context.Context, int64) (*account.Record, error)
	GetByIDs(context.Context, []int64) ([]*account.Record, error)
}

// NewAccountEvents 保留事件接口，只调用新 writer 与发布器。
func NewAccountEvents(reader AccountRecordsReader, cache scheduler.SnapshotCache) accountpostgres.AccountEvents {
	return accountSchedulerEvents{publisher: scheduler.SnapshotPublisher{
		Cache: cache, Diagnostics: service.LegacySchedulerDiagnostics(),
		Read: func(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
			v, err := reader.GetByID(ctx, id)
			return service.LegacySnapshotWrap(service.AccountFromRecord(v)), err
		},
		ReadMany: func(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
			v, err := reader.GetByIDs(ctx, ids)
			return wrapSchedulerRecordPointers(v), err
		},
	}}
}

type accountSchedulerEvents struct{ publisher scheduler.SnapshotPublisher }

func (b accountSchedulerEvents) Write(ctx context.Context, exec postgresinfra.Executor, event accountpostgres.AccountEvent, id, group *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, accountSchedulerEventName(event), id, group, payload)
}
func (accountSchedulerEvents) GroupPayload(ids []int64) any            { return scheduler.GroupPayload(ids) }
func (b accountSchedulerEvents) SyncOne(ctx context.Context, id int64) { b.publisher.Publish(ctx, id) }
func (b accountSchedulerEvents) SyncMany(ctx context.Context, ids []int64) {
	b.publisher.PublishMany(ctx, ids)
}
func (b accountSchedulerEvents) Drop(ctx context.Context, id int64) { b.publisher.Drop(ctx, id) }
func accountSchedulerEventName(event accountpostgres.AccountEvent) string {
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

// AccountUsageEvents 保留原累计入口的事件和尽力发布顺序。
func AccountUsageEvents(reader AccountRecordsReader, cache scheduler.SnapshotCache, exec postgresinfra.Executor) billingpostgres.AccountUsageOptions {
	events := NewAccountEvents(reader, cache)
	return billingpostgres.AccountUsageOptions{Changed: func(ctx context.Context, id int64) error {
		return events.Write(ctx, exec, accountpostgres.AccountChanged, &id, nil, nil)
	}, Sync: events.SyncOne, Observe: func(format string, args ...any) { logging.LegacyPrintf("repository.account", format, args...) }}
}

// Name 保留账号 Adapter 的事件名称查询入口。
func (accountSchedulerEvents) Name(event accountpostgres.AccountEvent) string {
	return accountSchedulerEventName(event)
}
