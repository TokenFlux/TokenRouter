package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/stretchr/testify/require"
)

// TestCreativeRunOutboxClaimAndComplete 校验 outbox 领取带 lease 并可由同一 token 完成。
func TestCreativeRunOutboxClaimAndComplete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery("(?s)FROM creative_run_outbox.*FOR UPDATE SKIP LOCKED.*RETURNING").
		WithArgs(sqlmock.AnyArg(), 2, int64(120)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "operation", "status", "available_at", "lease_token", "lease_until", "attempt_count", "last_error", "created_at", "updated_at"}).
			AddRow(int64(9), "crun_outbox012345", "settle", "leased", now, "worker:token", now.Add(time.Minute), 1, "", now, now))
	repo := NewCreativeRunOutboxRepository(db)
	events, err := repo.Claim(context.Background(), "worker", 2, 2*time.Minute)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, service.CreativeRunOutboxSettle, events[0].Operation)
	require.Equal(t, "worker:token", events[0].LeaseToken)
	mock.ExpectExec("UPDATE creative_run_outbox").
		WithArgs(int64(9), "worker:token").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Complete(context.Background(), 9, "worker:token"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreativeRunOutboxRejectsLostLease 校验旧 token 不能覆盖新领取者。
func TestCreativeRunOutboxRejectsLostLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("UPDATE creative_run_outbox").
		WithArgs(int64(3), "old-token").
		WillReturnResult(sqlmock.NewResult(0, 0))
	repo := NewCreativeRunOutboxRepository(db)
	require.Error(t, repo.Complete(context.Background(), 3, "old-token"))
	require.NoError(t, mock.ExpectationsWereMet())
}
