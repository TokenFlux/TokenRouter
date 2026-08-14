package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	usageLogBestEffortBatchSQL  = `(?s)^\s*WITH input .*INSERT INTO usage_logs`
	usageLogBestEffortSingleSQL = `(?s)^\s*INSERT INTO usage_logs`
)

func TestFlushBestEffortBatch_RetriesDeadlockBeforeFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for attempt := 1; attempt <= postgresDeadlockMaxAttempts; attempt++ {
		expectation := mock.ExpectExec(usageLogBestEffortBatchSQL)
		if attempt < postgresDeadlockMaxAttempts {
			expectation.WillReturnError(&pq.Error{Code: "40P01"})
			continue
		}
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}

	req := newUsageLogBestEffortRequestForTest()
	repo := &usageLogRepository{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushBestEffortBatch_NonDeadlockUsesSingleFallbackImmediately(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(errors.New("batch unavailable"))
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &usageLogRepository{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushBestEffortBatch_DeadlockRetryExhaustedUsesSingleFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for attempt := 1; attempt <= postgresDeadlockMaxAttempts; attempt++ {
		mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(&pq.Error{Code: "40P01"})
	}
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &usageLogRepository{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func newUsageLogBestEffortRequestForTest() usageLogBestEffortRequest {
	log := &service.UsageLog{
		UserID:        1,
		BillingUserID: 1,
		APIKeyID:      2,
		AccountID:     3,
		RequestID:     "req-best-effort-deadlock",
		Model:         "gpt-5",
		InputTokens:   10,
		OutputTokens:  5,
		TotalCost:     1,
		ActualCost:    1,
		CreatedAt:     time.Now().UTC(),
	}
	return usageLogBestEffortRequest{
		prepared: prepareUsageLogInsert(log),
		apiKeyID: log.APIKeyID,
		resultCh: make(chan error, 1),
	}
}

func TestWaitPostgresDeadlockRetry_RespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitPostgresDeadlockRetry(ctx, 0), context.Canceled)
}
