package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	usageBillingClaimSQL         = `(?s)INSERT INTO usage_billing_dedup.*ON CONFLICT.*RETURNING id`
	usageBillingArchiveSQL       = `(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`
	usageBillingUserLockSQL      = `(?s)SELECT id\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL\s+FOR NO KEY UPDATE`
	usageBillingBalanceDeductSQL = `(?s)WITH locked_user AS.*FOR NO KEY UPDATE.*UPDATE users.*RETURNING users.balance`
	usageBillingAPIKeyQuotaSQL   = `(?s)UPDATE api_keys.*quota_used = quota_used \+ \$1.*RETURNING quota > 0`
)

func TestUsageBillingRepositoryApply_DeadlockRestartsWholeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cmd := &service.UsageBillingCommand{
		RequestID:          "req-deadlock-retry",
		RequestFingerprint: "fingerprint",
		APIKeyID:           7,
		UserID:             42,
	}
	for attempt := 1; attempt <= postgresDeadlockMaxAttempts; attempt++ {
		mock.ExpectBegin()
		mock.ExpectQuery(usageBillingClaimSQL).
			WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(attempt)))
		mock.ExpectQuery(usageBillingArchiveSQL).
			WithArgs(cmd.RequestID, cmd.APIKeyID).
			WillReturnError(sql.ErrNoRows)
		userLock := mock.ExpectQuery(usageBillingUserLockSQL).WithArgs(cmd.UserID)
		if attempt < postgresDeadlockMaxAttempts {
			userLock.WillReturnError(&pq.Error{Code: "40P01"})
			mock.ExpectRollback()
			continue
		}
		userLock.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(cmd.UserID))
		mock.ExpectCommit()
	}

	repo := &usageBillingRepository{db: db}
	result, err := repo.Apply(context.Background(), cmd)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockUsageBillingUser_ReturnsUserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingUserLockSQL).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	err = lockUsageBillingUser(context.Background(), tx, 42)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_KeepsForeignKeyCompatibleUserLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingBalanceDeductSQL).
		WithArgs(1.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(8.75, 1.25))
	mock.ExpectRollback()

	newBalance, deductedAmount, err := deductUsageBillingBalance(context.Background(), tx, 42, 1.25)
	require.NoError(t, err)
	require.InDelta(t, 8.75, newBalance, 0.000001)
	require.InDelta(t, 1.25, deductedAmount, 0.000001)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingMonetaryEffectsQuantizeBeforeSQL(t *testing.T) {
	const rawAmount = 0.000078125
	wantAmount := service.QuantizeUsageBillingAmount(rawAmount)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingBalanceDeductSQL).
		WithArgs(wantAmount, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(9999.99992187, wantAmount))
	mock.ExpectQuery(usageBillingAPIKeyQuotaSQL).
		WithArgs(wantAmount, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnRows(sqlmock.NewRows([]string{"exhausted"}).AddRow(false))
	mock.ExpectRollback()

	_, deductedAmount, err := deductUsageBillingBalance(context.Background(), tx, 42, rawAmount)
	require.NoError(t, err)
	_, err = incrementUsageBillingAPIKeyQuota(context.Background(), tx, 7, rawAmount)
	require.NoError(t, err)
	require.Equal(t, wantAmount, deductedAmount)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
