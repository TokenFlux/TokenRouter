//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPlatformQuotaCalendarOnPostgreSQL 验证注入日界、旧窗口累计及同连接参与，不使用生产数据库。
func TestPlatformQuotaCalendarOnPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("s16_calendar"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, postgresinfra.ApplyMigrations(ctx, db, migrations.FS))
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	calendar := timezone.NewCalendar(location)
	store := billingpostgres.NewUserPlatformQuotaRepository(client, calendar)
	for _, day := range []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, location),
		time.Date(2026, 11, 1, 0, 0, 0, 0, location),
	} {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			user, err := client.User.Create().SetEmail(fmt.Sprintf("calendar-%d@example.test", day.Unix())).SetPasswordHash("fixture").Save(ctx)
			require.NoError(t, err)
			before := day.Add(-time.Minute)
			require.NoError(t, store.IncrementUsageWithReset(ctx, user.ID, "openai", 3, before))
			require.NoError(t, store.IncrementUsageWithReset(ctx, user.ID, "openai", 2, day.Add(time.Hour)))
			value, err := store.GetByUserPlatform(ctx, user.ID, "openai")
			require.NoError(t, err)
			require.NotNil(t, value)
			require.Equal(t, 2.0, value.DailyUsageUSD)
			require.Equal(t, 5.0, value.WeeklyUsageUSD)
			require.Equal(t, 5.0, value.MonthlyUsageUSD)
			require.NotNil(t, value.DailyWindowStart)
			require.True(t, day.Equal(*value.DailyWindowStart))
			require.NotNil(t, value.WeeklyWindowStart)
			require.True(t, day.AddDate(0, 0, -6).Equal(*value.WeeklyWindowStart))
			require.NotNil(t, value.MonthlyWindowStart)
			require.True(t, before.Equal(*value.MonthlyWindowStart))

			// 日历迁移不改变事务参与：外层回滚必须撤销本次平台消费写入。
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			txCtx := ent.NewTxContext(ctx, tx)
			require.NoError(t, store.IncrementUsageWithReset(txCtx, user.ID, "openai", 7, day.Add(2*time.Hour)))
			inside, err := store.GetByUserPlatform(txCtx, user.ID, "openai")
			require.NoError(t, err)
			require.Equal(t, 9.0, inside.DailyUsageUSD)
			require.NoError(t, tx.Rollback())
			after, err := store.GetByUserPlatform(ctx, user.ID, "openai")
			require.NoError(t, err)
			require.Equal(t, 2.0, after.DailyUsageUSD)
			require.Equal(t, 5.0, after.MonthlyUsageUSD)
		})
	}
}
