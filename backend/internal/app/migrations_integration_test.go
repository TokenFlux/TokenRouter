//go:build integration

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/stretchr/testify/require"
)

// 在隔离 PostgreSQL 上验证锁、checksum、事务回滚及并发索引重放，不触碰发布迁移。
func TestS02MigrationsLockReplayAndRollback(t *testing.T) {
	fixture := newDatabaseFixture(t)
	ctx := context.Background()
	migrations := fstest.MapFS{
		"900_s02_contract.sql":            {Data: []byte("CREATE TABLE s02_migration_contract(id bigint PRIMARY KEY, value text);")},
		"901_s02_contract_index_notx.sql": {Data: []byte("CREATE INDEX CONCURRENTLY IF NOT EXISTS s02_migration_index ON s02_migration_contract(value);")},
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- postgres.ApplyMigrations(ctx, fixture.db, migrations) })
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, fixture.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE filename LIKE '90%_s02_%'`).Scan(&count))
	require.Equal(t, 2, count)
	changed := fstest.MapFS{"900_s02_contract.sql": {Data: []byte("SELECT 1;")}}
	require.ErrorContains(t, postgres.ApplyMigrations(ctx, fixture.db, changed), "checksum")
	failed := fstest.MapFS{"902_s02_rollback.sql": {Data: []byte("CREATE TABLE s02_should_rollback(id bigint); SELECT 1/0;")}}
	require.Error(t, postgres.ApplyMigrations(ctx, fixture.db, failed))
	var rolledBack bool
	require.NoError(t, fixture.db.QueryRow(`SELECT to_regclass('s02_should_rollback') IS NULL`).Scan(&rolledBack))
	require.True(t, rolledBack)
	conn, err := fixture.db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(694208311321144027)`)
	require.NoError(t, err)
	defer func() {
		_, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(694208311321144027)`)
		require.NoError(t, err)
	}()
	lockCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	require.True(t, errors.Is(postgres.ApplyMigrations(lockCtx, fixture.db, migrations), context.DeadlineExceeded))
}
