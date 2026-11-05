//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
)

// Provider last-used writes reach scheduler cache through the durable outbox
// replay path. The scenario preloads a cache entry, runs the application
// snapshot service, updates provider activity, and waits until Redis exposes
// the persisted Unix-second timestamp.

// The integration aliases use production snapshot assembly and event
// publication when exercising durable provider outbox replay.
var (
	NewSnapshotForTest       = provideSchedulerSnapshot
	NewProviderEventsForTest = newProviderEvents
)

// TestSchedulerSnapshotOutboxReplay starts snapshot replay over persistent
// provider storage and Redis. It checks an UpdateLastUsed event eventually
// updates the cached activity timestamp to the stored Unix-second value, then
// cleanup stops replay before closing its dependencies.
func TestSchedulerSnapshotOutboxReplay(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := t.Context()
	container, err := rediscontainer.Run(ctx, "redis:8.4-alpine")
	// Register any created container before startup errors can stop this test.
	// Later client cleanups run first because testing reverses registration order.
	if container != nil {
		t.Cleanup(func() {
			// Setup or test cancellation must not cancel Docker removal. Start the
			// bounded cleanup context when this callback runs.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := container.Terminate(cleanupCtx); err != nil {
				t.Errorf("terminate Redis container: %v", err)
			}
		})
	}
	require.NoError(t, err)
	address, err := container.Endpoint(ctx, "")
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	_, err = f.db.ExecContext(ctx, "TRUNCATE scheduler_outbox")
	require.NoError(t, err)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	cache := schedulerredis.NewSnapshotCache(rdb, codec.ProviderCodec{})
	store.SetEvents(NewProviderEventsForTest(store, nil))
	outbox := schedulerpostgres.NewSchedulerOutboxRepository(f.db)
	cfg := &config.Config{}
	cfg.Scheduling.Snapshot.OutboxPollIntervalSeconds = 1
	cfg.Scheduling.Snapshot.DbFallbackEnabled = true
	value := &provider.Record{Name: "outbox-replay-" + time.Now().Format("150405.000000"), Platform: provider.PlatformOpenAI, Type: provider.ProviderTypeAPIKey, Status: provider.StatusActive, Schedulable: true, Concurrency: 3, Priority: 1, Credentials: map[string]any{}, Extra: map[string]any{}}
	require.NoError(t, store.Create(ctx, value))
	require.NoError(t, cache.SetProvider(ctx, codec.WrapRecord(value)))
	groups := routingpostgres.NewGroupStore(f.client, f.db, routingpostgres.GroupStoreOptions{})
	runtime := NewSnapshotForTest(cache, outbox, store, groups, cfg)
	runtime.Start()
	// Runtime cleanup is registered after Redis cleanup. Its stop therefore
	// runs first, allowing replay work to finish while its cache and database remain usable.
	t.Cleanup(runtime.Stop)
	require.NoError(t, store.UpdateLastUsed(ctx, value.ID))
	updated, err := store.GetByID(ctx, value.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.LastUsedAt)
	expected := updated.LastUsedAt.Unix()
	// Last-used writes rely on outbox propagation rather than direct cache sync.
	// Wait for replay to expose the persisted timestamp before concluding the assembly path is connected.
	require.Eventually(t, func() bool {
		cached, err := cache.GetProvider(ctx, value.ID)
		if err != nil || cached == nil {
			return false
		}
		record, err := codec.RecordValue(cached)
		return err == nil && record != nil && record.LastUsedAt != nil && record.LastUsedAt.Unix() == expected
	}, 5*time.Second, 100*time.Millisecond)
}
