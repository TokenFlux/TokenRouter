//go:build integration

package app_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	idempotencypostgres "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
	settingspostgres "github.com/TokenFlux/TokenRouter/internal/settings/postgres"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitepostgres "github.com/TokenFlux/TokenRouter/internal/site/postgres"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type databaseFixture struct {
	db     *sql.DB
	client *dbent.Client
	dsn    string
	host   string
	port   int
}

// newDatabaseFixture 仅构造测试数据库，不装配应用 worker。
func newDatabaseFixture(t *testing.T) *databaseFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("s02_contracts"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(16)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, bootstrap.ApplyMigrations(ctx, db))
	host, err := pg.Host(ctx)
	require.NoError(t, err)
	port, err := pg.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	return &databaseFixture{db: db, client: client, dsn: dsn, host: host, port: port.Int()}
}

func TestS02StorageContracts(t *testing.T) {
	fixture := newDatabaseFixture(t)
	ctx := context.Background()
	t.Run("settings-batch-atomicity", func(t *testing.T) {
		store := settings.New(settingspostgres.NewSettingRepository(fixture.client))
		require.Same(t, store, settings.New(store), "接口投影必须保留同一设置状态")
		require.NoError(t, store.Set(ctx, "s02_existing", "before"))
		// 用临时触发器制造真实 SQL 失败，不改发布迁移或运行代码。
		_, err := fixture.db.ExecContext(ctx, `CREATE FUNCTION s02_reject_setting() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.key = 's02_reject' THEN RAISE EXCEPTION 's02 injected failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER s02_setting_failure BEFORE INSERT OR UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION s02_reject_setting();`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := fixture.db.ExecContext(ctx, `DROP TRIGGER s02_setting_failure ON settings; DROP FUNCTION s02_reject_setting();`)
			require.NoError(t, e)
		})
		err = store.SetMultiple(ctx, map[string]string{"s02_existing": "after", "s02_reject": "value"})
		require.Error(t, err)
		value, err := store.GetValue(ctx, "s02_existing")
		require.NoError(t, err)
		require.Equal(t, "before", value)
		_, err = store.Get(ctx, "s02_reject")
		require.ErrorIs(t, err, settings.ErrSettingNotFound)
	})
	t.Run("idempotency-concurrent-claim-replay-cleanup", func(t *testing.T) {
		repo := idempotencypostgres.NewIdempotencyRepository(fixture.db)
		cfg := idempotency.DefaultIdempotencyConfig()
		cfg.ObserveOnly = false
		coordinator := idempotency.NewIdempotencyCoordinator(repo, cfg)
		opts := idempotency.IdempotencyExecuteOptions{Scope: "s02.test", ActorScope: "user:1", Method: "POST", Route: "/contract", IdempotencyKey: "same-key", Payload: map[string]string{"value": "same"}, RequireKey: true}
		var effects atomic.Int32
		var wg sync.WaitGroup
		results := make(chan error, 24)
		for range 24 {
			wg.Go(func() {
				_, err := coordinator.Execute(ctx, opts, func(context.Context) (any, error) {
					effects.Add(1)
					time.Sleep(25 * time.Millisecond)
					return map[string]string{"result": "ok"}, nil
				})
				results <- err
			})
		}
		wg.Wait()
		close(results)
		for err := range results {
			require.True(t, err == nil || errors.Is(err, idempotency.ErrIdempotencyInProgress), "unexpected result: %v", err)
		}
		require.EqualValues(t, 1, effects.Load())
		replay, err := coordinator.Execute(ctx, opts, func(context.Context) (any, error) { t.Error("重放再次执行副作用"); return nil, nil })
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		opts.Payload = map[string]string{"value": "different"}
		_, err = coordinator.Execute(ctx, opts, func(context.Context) (any, error) { t.Error("冲突执行副作用"); return nil, nil })
		require.ErrorIs(t, err, idempotency.ErrIdempotencyKeyConflict)
		deleted, err := repo.DeleteExpired(ctx, time.Now().Add(48*time.Hour), 500)
		require.NoError(t, err)
		require.EqualValues(t, 1, deleted)
	})
	t.Run("announcement-transactions-and-first-read", func(t *testing.T) {
		user, err := fixture.client.User.Create().SetEmail("s02@example.test").SetPasswordHash("test-only").Save(ctx)
		require.NoError(t, err)
		repo := sitepostgres.NewAnnouncementRepository(fixture.client)
		reads := sitepostgres.NewAnnouncementReadRepository(fixture.client)
		tx, err := fixture.client.Tx(ctx)
		require.NoError(t, err)
		txCtx := dbent.NewTxContext(ctx, tx)
		rolledBack := &site.Announcement{Title: "rollback", Content: "body", Status: site.AnnouncementStatusActive, NotifyMode: site.AnnouncementNotifyModeSilent}
		require.NoError(t, repo.Create(txCtx, rolledBack))
		require.NoError(t, reads.MarkRead(txCtx, rolledBack.ID, user.ID, time.Now()))
		require.NoError(t, tx.Rollback())
		_, err = repo.GetByID(ctx, rolledBack.ID)
		require.ErrorIs(t, err, site.ErrAnnouncementNotFound)
		count, err := reads.CountByAnnouncementID(ctx, rolledBack.ID)
		require.NoError(t, err)
		require.Zero(t, count)
		announcement := &site.Announcement{Title: "visible", Content: "body", Status: site.AnnouncementStatusActive, NotifyMode: site.AnnouncementNotifyModePopup}
		require.NoError(t, repo.Create(ctx, announcement))
		first := time.Now().UTC().Truncate(time.Microsecond)
		require.NoError(t, reads.MarkRead(ctx, announcement.ID, user.ID, first))
		require.NoError(t, reads.MarkRead(ctx, announcement.ID, user.ID, first.Add(time.Hour)))
		readMap, err := reads.GetReadMapByUser(ctx, user.ID, []int64{announcement.ID})
		require.NoError(t, err)
		require.True(t, first.Equal(readMap[announcement.ID]))
		past := time.Now().Add(-time.Hour)
		announcement.EndsAt = &past
		require.NoError(t, repo.Update(ctx, announcement))
		archived, err := repo.ArchiveExpired(ctx, time.Now())
		require.NoError(t, err)
		require.EqualValues(t, 1, archived)
		stored, err := repo.GetByID(ctx, announcement.ID)
		require.NoError(t, err)
		require.Equal(t, site.AnnouncementStatusArchived, stored.Status)
	})
}
