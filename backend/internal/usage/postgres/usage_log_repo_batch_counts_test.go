package postgres

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestBatchUserCountsUsesExplicitBoundsAndPreservesZeroRows verifies the counter query independently of charge-only reporting.
// The SQL uses both range endpoints without a positive-cost predicate and retains zero observations for requested actors with no matching usage.
func TestBatchUserCountsUsesExplicitBoundsAndPreservesZeroRows(t *testing.T) {
	db, mock := newSQLMock(t)
	store := &Store{sql: db}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(`(?s)SELECT user_id,COUNT\(\*\),.*FROM usage_logs WHERE user_id=ANY\(\$1\) AND created_at >= \$2 AND created_at < \$3 GROUP BY user_id`).WithArgs("{1,2}", start, end).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "requests", "tokens"}).AddRow(1, 3, 24))
	values, err := store.GetBatchUserUsageCounts(t.Context(), []int64{1, 1, -1, 2}, start, end)
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.Equal(t, int64(3), values[1].Requests)
	require.Equal(t, int64(24), values[1].Tokens)
	require.Equal(t, int64(2), values[2].UserID)
	require.Zero(t, values[2].Requests)
	require.NoError(t, mock.ExpectationsWereMet())
}
