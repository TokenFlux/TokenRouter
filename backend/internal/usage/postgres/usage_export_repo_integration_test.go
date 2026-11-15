//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
		require.NoError(t, repo.CreateTask(ctx, task))
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
