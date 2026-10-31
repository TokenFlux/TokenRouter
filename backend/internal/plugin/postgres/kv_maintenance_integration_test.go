//go:build integration

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKVCleanupSkipsLockedExpiredRows holds one expired row in a separate transaction while maintenance runs.
// Cleanup returns without deleting the locked record, then removes it after the owning transaction releases its lock, preserving mutation coordination.
func TestKVCleanupSkipsLockedExpiredRows(t *testing.T) {
	store, _, db := persistenceFixture(t)
	_, err := db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value,expires_at)
VALUES('test.kv','data','locked','1',statement_timestamp()-INTERVAL '1 second')`)
	require.NoError(t, err)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var version int64
	require.NoError(t, tx.QueryRowContext(t.Context(), "SELECT version FROM plugin_kv FOR UPDATE").Scan(&version))
	count, err := store.KVExpire(t.Context(), 1000)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, tx.Rollback())
	count, err = store.KVExpire(t.Context(), 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}
