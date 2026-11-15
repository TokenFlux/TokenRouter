//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestExportStoreIntegrationSkipLockedClaimAndStaleReclaim runs concurrent
// ClaimNextPendingTask workers and stale-task recovery against PostgreSQL.
// Workers claim distinct pending rows via SKIP LOCKED, and stale running rows
// reset exported_rows to zero when reclaimed.
func TestExportStoreIntegrationSkipLockedClaimAndStaleReclaim(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE TABLE usage_export_tasks RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	repo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	for i := range 2 {
		task := &usage.UsageExportTask{
			Scope:     usage.UsageExportScopeUser,
			Status:    usage.UsageExportStatusPending,
			CreatedBy: int64(10 + i),
			Filters: usage.UsageExportFilters{
				StartTime: start,
				EndTime:   end,
				Timezone:  "UTC",
			},
		}
		require.NoError(t, repo.CreateTask(ctx, task, 2))
	}

	var (
		wg      sync.WaitGroup
		claimed = make([]*usage.UsageExportTask, 2)
		errs    = make([]error, 2)
	)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			claimed[idx], errs[idx] = repo.ClaimNextPendingTask(ctx, 1800)
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.NotNil(t, claimed[0])
	require.NotNil(t, claimed[1])
	require.NotEqual(t, claimed[0].ID, claimed[1].ID)

	// Simulate partial progress followed by worker abandonment past the stale threshold.
	require.NoError(t, repo.UpdateTaskProgress(ctx, claimed[0].ID, 100, 40))
	_, err = integrationDB.ExecContext(
		ctx,
		"UPDATE usage_export_tasks SET started_at = NOW() - interval '2 hours' WHERE id = $1",
		claimed[0].ID,
	)
	require.NoError(t, err)

	reclaimed, err := repo.ClaimNextPendingTask(ctx, 1800)
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	require.Equal(t, claimed[0].ID, reclaimed.ID)
	require.Equal(t, int64(0), reclaimed.ExportedRows)
}

// TestExportStoreIntegrationConcurrentQuotaEnforcement runs concurrent CreateTask
// calls across independent ExportStore instances against PostgreSQL. Transaction
// advisory locks serialize per-actor admission so concurrent requests neither
// exceed maxActive from one existing task nor race from zero existing tasks.
func TestExportStoreIntegrationConcurrentQuotaEnforcement(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE TABLE usage_export_tasks RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filters := usage.UsageExportFilters{
		StartTime: start,
		EndTime:   end,
		Timezone:  "UTC",
	}

	seedRepo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
	seedTask := &usage.UsageExportTask{
		Scope:     usage.UsageExportScopeUser,
		Status:    usage.UsageExportStatusPending,
		CreatedBy: 42,
		Filters:   filters,
	}
	require.NoError(t, seedRepo.CreateTask(ctx, seedTask, 2))

	const contenders = 6
	barrier := make(chan struct{})
	errs := make([]error, contenders)
	var wg sync.WaitGroup
	for i := range contenders {
		// Construct separate ExportStore instances so PostgreSQL advisory locks,
		// rather than any in-process struct mutex, enforce the active quota.
		workerRepo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
		wg.Add(1)
		go func(idx int, r *ExportStore) {
			defer wg.Done()
			<-barrier
			errs[idx] = r.CreateTask(ctx, &usage.UsageExportTask{
				Scope:     usage.UsageExportScopeUser,
				Status:    usage.UsageExportStatusPending,
				CreatedBy: 42,
				Filters:   filters,
			}, 2)
		}(i, workerRepo)
	}
	close(barrier)
	wg.Wait()

	var inserted, rejected int
	for _, createErr := range errs {
		switch {
		case createErr == nil:
			inserted++
		case errors.Is(createErr, usage.ErrExportActiveLimitExceeded):
			rejected++
		default:
			t.Fatalf("unexpected CreateTask error: %v", createErr)
		}
	}
	require.Equal(t, 1, inserted)
	require.Equal(t, contenders-1, rejected)

	activeForUser42, err := seedRepo.CountActiveTasks(ctx, usage.UsageExportScopeUser, 42)
	require.NoError(t, err)
	require.Equal(t, int64(2), activeForUser42)

	// Also verify concurrent creation starting from zero existing rows for user 99.
	zeroBarrier := make(chan struct{})
	zeroErrs := make([]error, contenders)
	for i := range contenders {
		workerRepo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
		wg.Add(1)
		go func(idx int, r *ExportStore) {
			defer wg.Done()
			<-zeroBarrier
			zeroErrs[idx] = r.CreateTask(ctx, &usage.UsageExportTask{
				Scope:     usage.UsageExportScopeUser,
				Status:    usage.UsageExportStatusPending,
				CreatedBy: 99,
				Filters:   filters,
			}, 2)
		}(i, workerRepo)
	}
	close(zeroBarrier)
	wg.Wait()

	var zeroInserted, zeroRejected int
	for _, createErr := range zeroErrs {
		switch {
		case createErr == nil:
			zeroInserted++
		case errors.Is(createErr, usage.ErrExportActiveLimitExceeded):
			zeroRejected++
		default:
			t.Fatalf("unexpected CreateTask error from empty state: %v", createErr)
		}
	}
	require.Equal(t, 2, zeroInserted)
	require.Equal(t, contenders-2, zeroRejected)

	activeForUser99, err := seedRepo.CountActiveTasks(ctx, usage.UsageExportScopeUser, 99)
	require.NoError(t, err)
	require.Equal(t, int64(2), activeForUser99)
}

// TestExportStoreIntegrationListTaskPageConsistentSnapshot races concurrent
// task creation and completion transitions against ListTaskPage readers on
// PostgreSQL. REPEATABLE READ read-only snapshots ensure Items, WatchedItems,
// and ActiveIDs always reflect the exact same point-in-time task statuses.
func TestExportStoreIntegrationListTaskPageConsistentSnapshot(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE TABLE usage_export_tasks RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	repo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
	start := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	filters := usage.UsageExportFilters{
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Timezone:  "UTC",
	}
	uid := int64(77)

	const totalTasks = 40
	createdIDs := make([]int64, totalTasks)
	for i := range totalTasks {
		task := &usage.UsageExportTask{
			Scope:     usage.UsageExportScopeUser,
			Status:    usage.UsageExportStatusRunning,
			CreatedBy: uid,
			Filters:   filters,
		}
		require.NoError(t, repo.CreateTask(ctx, task, 0))
		createdIDs[i] = task.ID
	}

	stopReaders := make(chan struct{})
	var readerWG sync.WaitGroup
	errCh := make(chan error, 8)

	for range 4 {
		readerRepo := NewExportStoreWithSQL(integrationEntClient, integrationDB)
		readerWG.Add(1)
		go func(r *ExportStore) {
			defer readerWG.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				page, listErr := r.ListTaskPage(
					ctx,
					usage.UsageExportListFilter{Scope: usage.UsageExportScopeUser, CreatedBy: &uid},
					pagination.PaginationParams{Page: 1, PageSize: 10},
					createdIDs[:5],
				)
				if listErr != nil {
					errCh <- listErr
					return
				}
				activeSet := make(map[int64]struct{}, len(page.ActiveIDs))
				for _, id := range page.ActiveIDs {
					activeSet[id] = struct{}{}
				}
				checkItem := func(item usage.UsageExportTask) bool {
					isActive := item.Status == usage.UsageExportStatusPending || item.Status == usage.UsageExportStatusRunning
					_, inActive := activeSet[item.ID]
					return isActive == inActive
				}
				for _, item := range page.Items {
					if !checkItem(item) {
						errCh <- errors.New("split snapshot between page items and active_ids")
						return
					}
				}
				for _, item := range page.WatchedItems {
					if !checkItem(item) {
						errCh <- errors.New("split snapshot between watched_items and active_ids")
						return
					}
				}
			}
		}(readerRepo)
	}

	for _, id := range createdIDs {
		_, markErr := repo.MarkTaskSucceeded(ctx, id, 10, "export.csv", "user/export.csv", 64, start.Add(24*time.Hour))
		require.NoError(t, markErr)
	}
	close(stopReaders)
	readerWG.Wait()
	close(errCh)

	for readErr := range errCh {
		require.NoError(t, readErr)
	}
}
