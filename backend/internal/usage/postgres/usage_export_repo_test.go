package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// newUsageExportEntRepo creates an Ent-backed ExportStore over an isolated
// in-memory SQLite database for task lifecycle and pagination assertions.
func newUsageExportEntRepo(t *testing.T) *ExportStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	return NewExportStoreWithSQL(client, db)
}

// TestExportStoreEntLifecycleAndCancellationGuard exercises Ent-backed export
// task creation, scoped listing, active counting, and progress persistence. It
// confirms that MarkTaskSucceeded refuses to overwrite a concurrently canceled
// task and that MarkTaskExpired clears FilePath while keeping FileName.
func TestExportStoreEntLifecycleAndCancellationGuard(t *testing.T) {
	repo := newUsageExportEntRepo(t)
	ctx := context.Background()

	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Hour)
	task1 := &usage.UsageExportTask{
		Scope:     usage.UsageExportScopeUser,
		Status:    usage.UsageExportStatusPending,
		CreatedBy: 42,
		Filters: usage.UsageExportFilters{
			StartTime: start,
			EndTime:   end,
			Timezone:  "Asia/Shanghai",
			Locale:    usage.UsageExportLocaleZH,
		},
	}
	require.NoError(t, repo.CreateTask(ctx, task1, 2))
	require.NotZero(t, task1.ID)

	task2 := &usage.UsageExportTask{
		Scope:     usage.UsageExportScopeUser,
		Status:    usage.UsageExportStatusRunning,
		CreatedBy: 42,
		Filters:   task1.Filters,
	}
	require.NoError(t, repo.CreateTask(ctx, task2, 2))

	overQuotaTask := &usage.UsageExportTask{
		Scope:     usage.UsageExportScopeUser,
		Status:    usage.UsageExportStatusPending,
		CreatedBy: 42,
		Filters:   task1.Filters,
	}
	require.ErrorIs(t, repo.CreateTask(ctx, overQuotaTask, 2), usage.ErrExportActiveLimitExceeded)

	active, err := repo.CountActiveTasks(ctx, usage.UsageExportScopeUser, 42)
	require.NoError(t, err)
	require.Equal(t, int64(2), active)

	require.NoError(t, repo.UpdateTaskProgress(ctx, task2.ID, 500, 250))
	loaded2, err := repo.GetTask(ctx, task2.ID)
	require.NoError(t, err)
	require.Equal(t, int64(500), loaded2.TotalRows)
	require.Equal(t, int64(250), loaded2.ExportedRows)

	// Requeue task2 on simulated shutdown, then restore running status before finalize assertions.
	require.NoError(t, repo.RequeueTask(ctx, task2.ID))
	requeued2, err := repo.GetTask(ctx, task2.ID)
	require.NoError(t, err)
	require.Equal(t, usage.UsageExportStatusPending, requeued2.Status)
	require.Equal(t, int64(0), requeued2.ExportedRows)

	// Cancel task1 and verify MarkTaskSucceeded refuses to overwrite canceled state.
	canceled, err := repo.CancelTask(ctx, task1.ID, 42)
	require.NoError(t, err)
	require.True(t, canceled)

	committedAfterCancel, err := repo.MarkTaskSucceeded(
		ctx,
		task1.ID,
		10,
		"usage_export.csv",
		"user/usage-export-1.csv",
		128,
		start.Add(24*time.Hour),
	)
	require.NoError(t, err)
	require.False(t, committedAfterCancel)

	// Finalize task2 and sweep it once its ExpiresAt passes.
	expAt := start.Add(24 * time.Hour)
	committed2, err := repo.MarkTaskSucceeded(
		ctx,
		task2.ID,
		500,
		"usage_export_2.csv",
		"user/usage-export-2.csv",
		4096,
		expAt,
	)
	require.NoError(t, err)
	require.True(t, committed2)

	expiredList, err := repo.ListExpiredSucceededTasks(ctx, expAt.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, expiredList, 1)
	require.Equal(t, task2.ID, expiredList[0].ID)

	require.NoError(t, repo.MarkTaskExpired(ctx, task2.ID))
	expiredTask, err := repo.GetTask(ctx, task2.ID)
	require.NoError(t, err)
	require.Equal(t, usage.UsageExportStatusExpired, expiredTask.Status)
	require.Nil(t, expiredTask.FilePath)
	require.NotNil(t, expiredTask.FileName)

	uid := int64(42)
	pageRes, err := repo.ListTaskPage(ctx, usage.UsageExportListFilter{
		Scope:     usage.UsageExportScopeUser,
		CreatedBy: &uid,
	}, pagination.PaginationParams{Page: 1, PageSize: 1}, []int64{task1.ID, task2.ID})
	require.NoError(t, err)
	require.Equal(t, int64(2), pageRes.Pagination.Total)
	require.Len(t, pageRes.Items, 1)
	require.Empty(t, pageRes.ActiveIDs)
	require.Zero(t, pageRes.ActiveCount)
	require.Len(t, pageRes.WatchedItems, 1)

	_, missingErr := repo.GetTask(ctx, 99999)
	require.ErrorIs(t, missingErr, usage.ErrExportTaskNotFound)
}

// TestExportStoreClaimNextPendingTaskUsesSkipLockedAndHandlesEmptyQueue runs
// ClaimNextPendingTask against sqlmock for both populated and empty task queues.
// The query uses FOR UPDATE SKIP LOCKED, decodes persisted filter JSON, and
// returns (nil, nil) when no eligible row is available.
func TestExportStoreClaimNextPendingTaskUsesSkipLockedAndHandlesEmptyQueue(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	repo := NewExportStoreWithSQL(nil, db)
	now := time.Date(2026, 4, 16, 8, 0, 0, 0, time.UTC)
	filtersBytes, err := json.Marshal(usage.UsageExportFilters{
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
		Timezone:  "UTC",
	})
	require.NoError(t, err)

	columns := []string{
		"id", "scope", "status", "filters", "created_by", "total_rows", "exported_rows",
		"file_name", "file_path", "file_size_bytes", "error_message",
		"canceled_by", "canceled_at", "started_at", "finished_at", "expires_at",
		"created_at", "updated_at",
	}
	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(usage.UsageExportStatusPending, usage.UsageExportStatusRunning, int64(1800), usage.UsageExportStatusRunning).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			int64(7), usage.UsageExportScopeAdmin, usage.UsageExportStatusRunning, filtersBytes, int64(1), int64(0), int64(0),
			nil, nil, int64(0), nil,
			nil, nil, now, nil, nil,
			now, now,
		))

	claimed, err := repo.ClaimNextPendingTask(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, int64(7), claimed.ID)
	require.Equal(t, "UTC", claimed.Filters.Timezone)

	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(usage.UsageExportStatusPending, usage.UsageExportStatusRunning, int64(600), usage.UsageExportStatusRunning).
		WillReturnRows(sqlmock.NewRows(columns))

	empty, err := repo.ClaimNextPendingTask(context.Background(), 600)
	require.NoError(t, err)
	require.Nil(t, empty)
}
