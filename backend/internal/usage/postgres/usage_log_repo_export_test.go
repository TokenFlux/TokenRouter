package postgres

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestStoreExportBatchReaderKeysetCursorAndCachedOwnedTeamLookup runs
// CountExportLogs and multi-page NextBatch reads against sqlmock. Queries apply
// half-open [StartTime, EndTime) bounds, advance descending (created_at, id)
// keyset cursors without SQL OFFSET, and query owned teams only once across batches.
func TestStoreExportBatchReaderKeysetCursorAndCachedOwnedTeamLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	store := &Store{sql: db}
	uid := int64(19)
	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	filters := usage.UsageExportFilters{
		StartTime:        start,
		EndTime:          end,
		Timezone:         "Asia/Shanghai",
		UserID:           &uid,
		IncludeOwnedTeam: true,
	}

	// CountExportLogs resolves owned team once for the initial count query.
	mock.ExpectQuery(`FROM team_memberships tm`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(88)))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM \(`).
		WithArgs(uid, int64(88), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))

	count, err := store.CountExportLogs(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	// NewExportBatchReader resolves owned team on batch 1 and reuses it on batch 2.
	reader := store.NewExportBatchReader(usage.UsageExportScopeUser)
	cols := strings.Split(usageLogSelectColumns, ", ")

	rowTime := start.Add(30 * time.Minute)
	mock.ExpectQuery(`FROM team_memberships tm`).
		WithArgs(uid).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(88)))
	mock.ExpectQuery(`ORDER BY created_at DESC, id DESC LIMIT \$5`).
		WithArgs(uid, int64(88), start, end, 1).
		WillReturnRows(sqlmock.NewRows(cols).AddRow(makeMockUsageLogRow(501, uid, rowTime)...))

	batch1, err := reader.NextBatch(context.Background(), filters, nil, 1)
	require.NoError(t, err)
	require.Len(t, batch1, 1)
	require.Equal(t, int64(501), batch1[0].ID)

	cursor := &usage.UsageExportCursor{CreatedAt: batch1[0].CreatedAt, ID: batch1[0].ID}
	mock.ExpectQuery(`\(created_at < \$5 OR \(created_at = \$5 AND id < \$6\)\) ORDER BY created_at DESC, id DESC LIMIT \$7`).
		WithArgs(uid, int64(88), start, end, cursor.CreatedAt, cursor.ID, 1).
		WillReturnRows(sqlmock.NewRows(cols))

	batch2, err := reader.NextBatch(context.Background(), filters, cursor, 1)
	require.NoError(t, err)
	require.Empty(t, batch2)
}

// makeMockUsageLogRow builds a 73-column row matching usageLogSelectColumns for sqlmock tests.
func makeMockUsageLogRow(id, userID int64, createdAt time.Time) []driver.Value {
	return []driver.Value{
		id, userID, userID, nil, int64(3), int64(4), "req_test", "gpt-5.4", "gpt-5.4", nil,
		nil, nil, 10, 20, 0, 0, 0, 0,
		0, 0.0, 0, 0.0, 0.001, 0.002, 0.0, 0.0, 0.003, 0.003, 0.0, 0.003,
		nil, 1.0, nil, int16(0), int16(usage.RequestTypeStream), true, false, nil, nil, nil,
		nil, 0, 0, 0, nil, nil, nil, nil, nil, 0,
		nil, nil, nil, nil, nil, nil, false, false, nil, nil,
		nil, nil, nil, nil, nil, createdAt, nil, false, "openai", nil,
		nil, usage.SourceUser, nil,
	}
}
