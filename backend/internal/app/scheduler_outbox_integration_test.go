//go:build integration

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/config"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// TestSchedulerSnapshotOutboxReplay 保留旧回放断言，全部端口使用生产原生实现。
func TestSchedulerSnapshotOutboxReplay(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := t.Context()
	container, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	address, err := container.Endpoint(ctx, "")
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	_, err = f.db.ExecContext(ctx, "TRUNCATE scheduler_outbox")
	require.NoError(t, err)
	store := accountpostgres.NewAccountStore(f.client, f.db, accountpostgres.AccountStoreOptions{})
	cache := schedulerredis.NewSnapshotCache(rdb, codec.AccountCodec{})
	store.SetEvents(app.NewS16AccountEvents(store, nil))
	outbox := schedulerpostgres.NewSchedulerOutboxRepository(f.db)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.OutboxPollIntervalSeconds = 1
	cfg.Gateway.Scheduling.DbFallbackEnabled = true
	value := &account.Record{Name: "outbox-replay-" + time.Now().Format("150405.000000"), Platform: account.PlatformOpenAI, Type: account.AccountTypeAPIKey, Status: account.StatusActive, Schedulable: true, Concurrency: 3, Priority: 1, Credentials: map[string]any{}, Extra: map[string]any{}}
	require.NoError(t, store.Create(ctx, value))
	require.NoError(t, cache.SetAccount(ctx, codec.WrapRecord(value)))
	groups := routingpostgres.NewGroupStore(f.client, f.db, routingpostgres.GroupStoreOptions{})
	runtime := app.NewS16Snapshot(cache, outbox, store, groups, cfg)
	runtime.Start()
	t.Cleanup(runtime.Stop)
	require.NoError(t, store.UpdateLastUsed(ctx, value.ID))
	updated, err := store.GetByID(ctx, value.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.LastUsedAt)
	expected := updated.LastUsedAt.Unix()
	require.Eventually(t, func() bool {
		cached, err := cache.GetAccount(ctx, value.ID)
		if err != nil || cached == nil {
			return false
		}
		record, err := codec.RecordValue(cached)
		return err == nil && record != nil && record.LastUsedAt != nil && record.LastUsedAt.Unix() == expected
	}, 5*time.Second, 100*time.Millisecond)
}
