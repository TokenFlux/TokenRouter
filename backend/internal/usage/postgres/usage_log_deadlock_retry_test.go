package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/DATA-DOG/go-sqlmock"
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
	defer func() {
		_ = db.Close()
	}()

	for attempt := 1; attempt <= 3; attempt++ {
		expectation := mock.ExpectExec(usageLogBestEffortBatchSQL)
		if attempt < 3 {
			expectation.WillReturnError(&pq.Error{Code: "40P01"})
			continue
		}
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushBestEffortBatch_NonDeadlockUsesSingleFallbackImmediately(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(errors.New("batch unavailable"))
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushBestEffortBatch_DeadlockRetryExhaustedUsesSingleFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	for attempt := 1; attempt <= 3; attempt++ {
		mock.ExpectExec(usageLogBestEffortBatchSQL).WillReturnError(&pq.Error{Code: "40P01"})
	}
	mock.ExpectExec(usageLogBestEffortSingleSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	req := newUsageLogBestEffortRequestForTest()
	repo := &Store{}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})

	require.NoError(t, <-req.resultCh)
	require.NoError(t, mock.ExpectationsWereMet())
}

func newUsageLogBestEffortRequestForTest() usageLogBestEffortRequest {
	log := &usage.UsageLog{
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
