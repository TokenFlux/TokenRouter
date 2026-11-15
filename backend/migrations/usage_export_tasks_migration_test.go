package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// usage_export_tasks_migration_test.go verifies the SQL migration that creates
// the usage_export_tasks persistence table and its worker and listing indexes.
// Tests check column defaults and index clauses directly from the embedded SQL
// file while guarding against accidental mutations on usage_logs.

// TestUsageExportTasksMigrationDefinesTableAndIndexes loads migration 312 from
// the embedded migration filesystem. It verifies that the task table, row
// counters, artifact metadata columns, and four operational indexes exist
// without altering or scanning usage_logs.
func TestUsageExportTasksMigrationDefinesTableAndIndexes(t *testing.T) {
	content, err := FS.ReadFile("312_usage_export_tasks.sql")
	require.NoError(t, err)

	sql := strings.ToUpper(string(content))
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS USAGE_EXPORT_TASKS")
	require.Contains(t, sql, "SCOPE VARCHAR(16) NOT NULL")
	require.Contains(t, sql, "STATUS VARCHAR(20) NOT NULL")
	require.Contains(t, sql, "FILTERS JSONB NOT NULL")
	require.Contains(t, sql, "CREATED_BY BIGINT NOT NULL")
	require.Contains(t, sql, "TOTAL_ROWS BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "EXPORTED_ROWS BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "FILE_SIZE_BYTES BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "EXPIRES_AT TIMESTAMPTZ")
	require.Contains(t, sql, "IDX_USAGE_EXPORT_TASKS_STATUS_CREATED_AT")
	require.Contains(t, sql, "IDX_USAGE_EXPORT_TASKS_SCOPE_CREATOR_CREATED_AT")
	require.Contains(t, sql, "IDX_USAGE_EXPORT_TASKS_SCOPE_CREATED_AT")
	require.Contains(t, sql, "IDX_USAGE_EXPORT_TASKS_STATUS_EXPIRES_AT")
	require.NotContains(t, sql, "ALTER TABLE USAGE_LOGS")
	require.NotContains(t, sql, "UPDATE USAGE_LOGS")
}
