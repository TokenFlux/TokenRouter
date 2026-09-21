package postgres_test

import (
	"context"
	"errors"
	"testing"

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// TestAssignOrExtendSubscriptionSerializesWithUserRowLock 验证订阅发放在读取最新时间链前锁定用户行。
func TestAssignOrExtendSubscriptionSerializesWithUserRowLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })

	lockProbeErr := errors.New("stop after user row lock")
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*FROM "users".*FOR UPDATE`).
		WithArgs(int64(42)).
		WillReturnError(lockProbeErr)
	mock.ExpectRollback()

	svc := billing.NewSubscriptionService(lockOrderGroupReader{}, billingtestkit.SubscriptionRepositoryNoop{}, billingpostgres.NewSubscriptionMutations(client))
	_, _, err = svc.AssignOrExtendSubscription(context.Background(), &billing.AssignSubscriptionInput{
		UserID:              42,
		PlanID:              7,
		ValidityDays:        30,
		UseProvidedTemplate: true,
	})

	require.ErrorIs(t, err, lockProbeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

// lockOrderGroupReader 保留原夹具的意外读取拒绝，锁失败前不能查询分组。
type lockOrderGroupReader struct{}

func (lockOrderGroupReader) GetByIDLite(context.Context, int64) (*billing.SubscriptionPlanGroup, error) {
	panic("unexpected GetByIDLite call")
}
