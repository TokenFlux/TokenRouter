package app

import (
	"context"
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	acctcore "github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

// provider_events.go binds provider persistence events to scheduler outbox
// writes and current-record snapshot publication. The application installs that
// event adapter before returning the provider store, and billing usage receives
// the same publication capability while retaining its chosen SQL executor for
// event writes.

// providerRecordsReader supplies stored provider data for scheduler snapshot
// publication.
type providerRecordsReader interface {
	// GetByID reads one stored provider record for current snapshot publication.
	GetByID(context.Context, int64) (*acctcore.Record, error)
	// GetByIDs reads the requested provider records for batched snapshot publication.
	GetByIDs(context.Context, []int64) ([]*acctcore.Record, error)
}

// providerSchedulerEvents connects provider storage events to scheduler outbox
// and snapshots.
type providerSchedulerEvents struct {
	publisher scheduler.SnapshotPublisher // current snapshot publication
}

// provideProviderDeferred binds delayed provider writes to storage and the
// timing wheel. Its ten-second interval and system clock are captured at
// assembly; the deferred service owns scheduling and shutdown of its accepted
// writes.
func provideProviderDeferred(store *providerpostgres.ProviderStore, wheel *timingwheel.Wheel) *acctcore.DeferredService {
	return acctcore.NewDeferredService(store, wheel, acctcore.DeferredOptions{Interval: 10 * time.Second, Now: time.Now, Observe: func(format string, args ...any) { logging.LegacyPrintf("service.provider_deferred", format, args...) }})
}

// newProviderEvents binds current provider reads to cache publication and
// diagnostics. reader remains available for later single and batch publication
// callbacks, while event persistence uses the executor passed into each Write
// call.
func newProviderEvents(reader providerRecordsReader, cache scheduler.SnapshotCache) providerpostgres.ProviderEvents {
	return providerSchedulerEvents{publisher: scheduler.SnapshotPublisher{
		Cache: cache, Diagnostics: scheduler.Diagnostics{
			Logf: logging.LegacyPrintf, Event: logging.Event,
		},

		Read: func(ctx context.Context, id int64) (scheduler.SnapshotProvider, error) {
			value, err := reader.GetByID(ctx, id)
			return codec.WrapRecord(value), err
		},
		ReadMany: func(ctx context.Context, ids []int64) ([]scheduler.SnapshotProvider, error) {
			value, err := reader.GetByIDs(ctx, ids)
			return wrapSchedulerRecordPointers(value), err
		},
	}}
}

// Write enqueues the mapped scheduler event using the caller's selected
// executor. ID, group, and payload are passed through; transaction completion
// belongs to that executor's owner, including context-specific participants.
func (b providerSchedulerEvents) Write(ctx context.Context, exec postgresinfra.Executor, event providerpostgres.ProviderEvent, id, group *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, providerSchedulerEventName(event), id, group, payload)
}

// GroupPayload delegates provider-group event payload construction to the
// scheduler format.
func (providerSchedulerEvents) GroupPayload(ids []int64) any { return scheduler.GroupPayload(ids) }

// SyncOne asks the shared snapshot publisher to reload and publish one
// provider.
func (b providerSchedulerEvents) SyncOne(ctx context.Context, id int64) { b.publisher.Publish(ctx, id) }

// SyncMany asks the shared snapshot publisher to reload the requested provider
// IDs.
func (b providerSchedulerEvents) SyncMany(ctx context.Context, ids []int64) {
	b.publisher.PublishMany(ctx, ids)
}

// Drop delegates removal of the provider's shared scheduler snapshot entry.
func (b providerSchedulerEvents) Drop(ctx context.Context, id int64) { b.publisher.Drop(ctx, id) }

// Name returns the scheduler event identity corresponding to a provider event.
func (providerSchedulerEvents) Name(event providerpostgres.ProviderEvent) string {
	return providerSchedulerEventName(event)
}

// providerSchedulerEventName maps the four supported provider events to
// scheduler names. Any unrecognized event panics, so event writers supply one
// of the provider store's declared event values.
func providerSchedulerEventName(event providerpostgres.ProviderEvent) string {
	switch event {
	case providerpostgres.ProviderChanged:
		return scheduler.SchedulerOutboxEventProviderChanged
	case providerpostgres.ProviderGroupsChanged:
		return scheduler.SchedulerOutboxEventProviderGroupsChanged
	case providerpostgres.ProviderLastUsed:
		return scheduler.SchedulerOutboxEventProviderLastUsed
	case providerpostgres.ProviderBulkChanged:
		return scheduler.SchedulerOutboxEventProviderBulkChanged
	default:
		panic("unknown provider event")
	}
}

// providerUsageEvents binds quota-change notification and snapshot reload to
// the selected executor. Changed writes a full provider event, while Sync uses
// current store reads; the billing usage caller controls transaction and
// callback timing.
func providerUsageEvents(reader providerRecordsReader, cache scheduler.SnapshotCache, exec postgresinfra.Executor) billingpostgres.ProviderUsageOptions {
	events := newProviderEvents(reader, cache)
	return billingpostgres.ProviderUsageOptions{
		Changed: func(ctx context.Context, id int64) error {
			return events.Write(ctx, exec, providerpostgres.ProviderChanged, &id, nil, nil)
		},
		Sync: events.SyncOne,
		Observe: func(format string, args ...any) {
			logging.LegacyPrintf("repository.provider", format, args...)
		},
	}
}

// wrapSchedulerRecordPointers wraps each provider pointer for scheduler storage
// conversion. Nil input stays nil, and elements are handed to codec.WrapRecord
// under that adapter's handling of absent records.
func wrapSchedulerRecordPointers(values []*acctcore.Record) []scheduler.SnapshotProvider {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotProvider, len(values))
	for i, value := range values {
		out[i] = codec.WrapRecord(value)
	}
	return out
}

// provideProviderStore constructs shared storage with group, proxy, identity,
// and clock adapters. It installs scheduler events before returning, so later
// mutation calls have outbox and snapshot publication capabilities attached.
func provideProviderStore(client *dbent.Client, db *sql.DB, cache scheduler.SnapshotCache) *providerpostgres.ProviderStore {
	store := providerpostgres.NewProviderStore(client, db, providerpostgres.ProviderStoreOptions{
		Group: func(g *dbent.Group) *accessview.GroupConfig {
			return (*accessview.GroupConfig)(routingpostgres.GroupFromEnt(g))
		},
		OllamaIdentity: acctcore.IsOllamaCloudUsageProvider,
		Proxy:          egresspostgres.ProxyEntity, Now: time.Now, LoadLocation: time.LoadLocation,
		Observe: func(format string, args ...any) { logging.LegacyPrintf("repository.provider", format, args...) },
	})
	store.SetEvents(newProviderEvents(store, cache))
	return store
}

// provideExecutionProviderStore joins provider configuration reads with billing
// quota writes for execution.
func provideExecutionProviderStore(store *providerpostgres.ProviderStore, usage *billingpostgres.ProviderUsageStore) gatewayprovider.ExecutionProviderStore {
	return &executionProviderStore{data: store, usage: usage}
}
