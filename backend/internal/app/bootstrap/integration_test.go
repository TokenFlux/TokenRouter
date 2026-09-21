//go:build integration

package bootstrap

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var bootstrapFixture struct {
	once      sync.Once
	container *tcpostgres.PostgresContainer
	client    *dbent.Client
	err       error
}

// TestMain 只在测试确实申请数据库时构造容器，并在本包全部测试结束后释放。
func TestMain(m *testing.M) {
	code := m.Run()
	if bootstrapFixture.client != nil {
		_ = bootstrapFixture.client.Close()
	}
	if bootstrapFixture.container != nil {
		_ = bootstrapFixture.container.Terminate(context.Background())
	}
	os.Exit(code)
}

func testEntTx(t *testing.T) *dbent.Tx {
	t.Helper()
	bootstrapFixture.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := InitTimezone("UTC"); err != nil {
			bootstrapFixture.err = err
			return
		}
		pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("s02_bootstrap"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
		if err != nil {
			bootstrapFixture.err = err
			return
		}
		bootstrapFixture.container = pg
		dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
		if err != nil {
			bootstrapFixture.err = err
			return
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			bootstrapFixture.err = err
			return
		}
		if err = ApplyMigrations(ctx, db); err != nil {
			_ = db.Close()
			bootstrapFixture.err = err
			return
		}
		bootstrapFixture.client = dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	})
	require.NoError(t, bootstrapFixture.err)
	tx, err := bootstrapFixture.client.Tx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}
