package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestIsMissingRelationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not missing", err: nil, want: false},
		{name: "match relation does not exist", err: fakeErr(`pq: relation "ops_error_logs" does not exist`), want: true},
		{name: "match case-insensitive", err: fakeErr(`ERROR: Relation "x" Does Not Exist`), want: true},
		{name: "non-matching error", err: fakeErr("connection refused"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingRelationError(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeleteOldRowsByCTIDBatchesAndThrottles(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer func() { _ = db.Close() }()

	cutoff := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	query := `(?s)WITH batch AS MATERIALIZED.*ORDER BY created_at ASC, id ASC.*DELETE FROM ops_system_logs AS target`
	mock.ExpectExec(query).WithArgs(cutoff, 2).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(query).WithArgs(cutoff, 2).WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := deleteOldRowsByCTID(context.Background(), db, "ops_system_logs", "created_at", cutoff, 2, time.Millisecond, false)
	if err != nil {
		t.Fatalf("delete rows: %v", err)
	}
	if result.Deleted != 3 || result.Batches != 2 {
		t.Fatalf("result = %#v, want deleted=3 batches=2", result)
	}
	if result.Throttled <= 0 {
		t.Fatalf("throttled = %v, want positive duration", result.Throttled)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestTruncateOpsTableUsesStatisticsInsteadOfFullCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`(?s)SELECT COALESCE.*FROM pg_class.*to_regclass`).
		WithArgs("ops_error_logs").
		WillReturnRows(sqlmock.NewRows([]string{"estimated_rows"}).AddRow(int64(4200)))
	mock.ExpectExec(`TRUNCATE TABLE ops_error_logs`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	estimatedRows, err := truncateOpsTable(context.Background(), db, "ops_error_logs")
	if err != nil {
		t.Fatalf("truncate table: %v", err)
	}
	if estimatedRows != 4200 {
		t.Fatalf("estimated rows = %d, want 4200", estimatedRows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSleepOpsCleanupWithContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepOpsCleanupWithContext(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
