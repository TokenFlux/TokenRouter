package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

type accountRecordsReader interface {
	GetByID(context.Context, int64) (*account.Record, error)
	GetByIDs(context.Context, []int64) ([]*account.Record, error)
}

// newAccountEvents 将账号写入事件接入唯一 scheduler outbox 与快照发布器。
func newAccountEvents(reader accountRecordsReader, cache scheduler.SnapshotCache) accountpostgres.AccountEvents {
	return accountSchedulerEvents{publisher: scheduler.SnapshotPublisher{
		Cache: cache, Diagnostics: scheduler.Diagnostics{
			Logf: logging.LegacyPrintf, Event: logging.Event},

		Read: func(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
			value, err := reader.GetByID(ctx, id)
			return codec.WrapRecord(value), err
		},
		ReadMany: func(ctx context.Context, ids []int64) ([]scheduler.SnapshotAccount, error) {
			value, err := reader.GetByIDs(ctx, ids)
			return wrapSchedulerRecordPointers(value), err
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
func (accountSchedulerEvents) Name(event accountpostgres.AccountEvent) string {
	return accountSchedulerEventName(event)
}

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

// accountUsageEvents 保留账号累计入口原有事件与尽力发布顺序。
func accountUsageEvents(reader accountRecordsReader, cache scheduler.SnapshotCache, exec postgresinfra.Executor) billingpostgres.AccountUsageOptions {
	events := newAccountEvents(reader, cache)
	return billingpostgres.AccountUsageOptions{
		Changed: func(ctx context.Context, id int64) error {
			return events.Write(ctx, exec, accountpostgres.AccountChanged, &id, nil, nil)
		},
		Sync: events.SyncOne,
		Observe: func(format string, args ...any) {
			logging.LegacyPrintf("repository.account", format, args...)
		},
	}
}

func wrapSchedulerRecordPointers(values []*account.Record) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i, value := range values {
		out[i] = codec.WrapRecord(value)
	}
	return out
}
