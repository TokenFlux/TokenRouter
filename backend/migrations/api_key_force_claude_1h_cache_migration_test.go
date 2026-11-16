package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// api_key_force_claude_1h_cache_migration_test.go verifies the SQL migration
// that adds the force_claude_1h_cache column to api_keys and extends
// cross-instance authentication snapshot invalidation. The test reads the
// embedded SQL directly to guard column defaults and trigger coverage.

// TestAPIKeyForceClaude1hCacheMigration reads migration 313 from the embedded
// migration filesystem. It verifies that api_keys receives a non-null false
// default for force_claude_1h_cache and that auth-cache invalidation watches
// edits to the new column alongside existing fields.
func TestAPIKeyForceClaude1hCacheMigration(t *testing.T) {
	content, err := FS.ReadFile("313_api_key_force_claude_1h_cache.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS force_claude_1h_cache BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "COMMENT ON COLUMN api_keys.force_claude_1h_cache")
	require.Contains(t, sql, "OLD.force_claude_1h_cache IS DISTINCT FROM NEW.force_claude_1h_cache")
	require.Contains(t, sql, "OLD.concurrency_limit IS DISTINCT FROM NEW.concurrency_limit")
	require.Contains(t, sql, "OLD.preferred_subscription_id IS DISTINCT FROM NEW.preferred_subscription_id")
}
