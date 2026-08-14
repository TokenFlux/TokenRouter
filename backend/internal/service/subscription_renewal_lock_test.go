package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/BrandonVee/TokenRouter/ent"
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

	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, client, nil)
	_, _, err = svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:              42,
		PlanID:              7,
		ValidityDays:        30,
		UseProvidedTemplate: true,
	})

	require.ErrorIs(t, err, lockProbeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
