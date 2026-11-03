//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// TestSaveCoordinatesSourceRemoval exercises both orders of saving and deleting a referenced source on independent PostgreSQL connections.
// Current and previous relationships become NULL, while accepted snapshots and administrative changes survive without foreign-key failures.
func TestSaveCoordinatesSourceRemoval(t *testing.T) {
	for _, deletionFirst := range []bool{false, true} {
		name := "save_before_delete"
		if deletionFirst {
			name = "delete_before_save"
		}
		t.Run(name, func(t *testing.T) {
			store, client, db := persistenceFixture(t)
			source := &plugin.IndexSource{Name: "Custom", URL: "https://example.invalid/index.json", Enabled: true}
			require.NoError(t, store.SaveIndex(t.Context(), source))
			item := storedInstallation(t)
			item.Source = plugin.SourceIndex
			item.IndexID = &source.ID
			previous := item.VersionSnapshot
			previous.Version = "0.9.0"
			previous.Config = map[string]any{"removed": "retained"}
			item.Previous = &previous
			require.NoError(t, store.Save(t.Context(), item))
			item.Enabled = false
			tx, err := client.Tx(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx.Rollback() })
			txContext := ent.NewTxContext(t.Context(), tx)
			queryPrefix := "DELETE FROM plugin_indexes"
			if deletionFirst {
				require.NoError(t, store.DeleteIndex(txContext, source.ID))
				queryPrefix = "INSERT INTO plugin_installations"
			} else {
				require.NoError(t, store.Save(txContext, item))
			}
			// Bound the competing statement even if database lock observation fails, so
			// cleanup can cancel and join its goroutine before closing the shared pool.
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			finished := make(chan struct{})
			var operationErr error
			// Release the held transaction before waiting during cleanup, so an assertion
			// failure cannot strand the other connection behind this test's own row lock.
			t.Cleanup(func() {
				_ = tx.Rollback()
				cancel()
				<-finished
			})
			// Run the opposing operation on a separate pooled connection and observe its
			// actual PostgreSQL lock wait before allowing the first transaction to commit.
			go func() {
				defer close(finished)
				if deletionFirst {
					operationErr = store.Save(ctx, item)
				} else {
					operationErr = store.DeleteIndex(ctx, source.ID)
				}
			}()
			require.Eventually(t, func() bool {
				var waiting bool
				err := db.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, queryPrefix+"%").Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond)
			// Commit only after the competing statement is blocked, proving source locks
			// survive Save's return until the surrounding transaction releases its ownership.
			require.NoError(t, tx.Commit())
			<-finished
			require.NoError(t, operationErr)
			saved, err := store.Get(t.Context(), item.PluginID)
			require.NoError(t, err)
			require.Nil(t, saved.IndexID)
			require.Nil(t, saved.Previous.IndexID)
			require.Equal(t, plugin.SourceIndex, saved.Source)
			require.Equal(t, plugin.SourceIndex, saved.Previous.Source)
			require.Equal(t, item.Config, saved.Config)
			require.Equal(t, item.Previous.Config, saved.Previous.Config)
			require.Equal(t, item.SecretConfig, saved.SecretConfig)
			require.False(t, saved.Enabled)
		})
	}
}

// TestInstallationSnapshotsAndTransactionOwnership saves complete rollback configuration and joins an existing Ent transaction.
// PostgreSQL normalization preserves identity digests, and rolling back the outer transaction restores the previously saved installation.
func TestInstallationSnapshotsAndTransactionOwnership(t *testing.T) {
	store, client, _ := persistenceFixture(t)
	item := storedInstallation(t)
	previous := item.VersionSnapshot
	previous.Version = "0.9.0"
	previous.Config = map[string]any{"removed_field": "keep-for-rollback"}
	previous.SecretConfig = "encrypted-previous"
	item.Previous = &previous
	require.NoError(t, store.Save(t.Context(), item))
	read, err := store.Get(t.Context(), item.PluginID)
	require.NoError(t, err)
	require.Equal(t, "encrypted-previous", read.Previous.SecretConfig)
	require.Equal(t, "keep-for-rollback", read.Previous.Config["removed_field"])
	require.Equal(t, item.ManifestSHA256, read.ManifestSHA256)
	require.JSONEq(t, string(item.ManifestJSON), string(read.ManifestJSON))
	require.False(t, read.InstalledAt.IsZero())
	tx, err := client.Tx(t.Context())
	require.NoError(t, err)
	txContext := ent.NewTxContext(t.Context(), tx)
	read.Enabled = false
	require.NoError(t, store.Save(txContext, read))
	require.NoError(t, tx.Rollback())
	read, err = store.Get(t.Context(), item.PluginID)
	require.NoError(t, err)
	require.True(t, read.Enabled)
}

// TestPurgeIsAtomicAndLedgerAlwaysSurvives injects a KV delete failure after installation deletion inside the transaction.
// Both records remain after rollback; retry deletes installation and KV while retaining the financial ledger and replay key.
func TestPurgeIsAtomicAndLedgerAlwaysSurvives(t *testing.T) {
	store, _, db := persistenceFixture(t)
	item := storedInstallation(t)
	require.NoError(t, store.Save(t.Context(), item))
	_, err := db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value) VALUES('test.plugin','data','key','{}');
 INSERT INTO plugin_ledger(plugin_id,idempotency_key,kind,user_id,amount,request,result,reason) VALUES('test.plugin','original','wallet_credit',1,1,'{}','{}','fixture');
 CREATE FUNCTION reject_plugin_purge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected purge failure'; END $$;
 CREATE TRIGGER reject_plugin_purge BEFORE DELETE ON plugin_kv FOR EACH ROW EXECUTE FUNCTION reject_plugin_purge();`)
	require.NoError(t, err)
	require.Error(t, store.Delete(t.Context(), item.PluginID, true))
	_, err = store.Get(t.Context(), item.PluginID)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_kv").Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.ExecContext(t.Context(), "DROP TRIGGER reject_plugin_purge ON plugin_kv")
	require.NoError(t, err)
	require.NoError(t, store.Delete(t.Context(), item.PluginID, true))
	_, err = store.Get(t.Context(), item.PluginID)
	require.ErrorIs(t, err, plugin.ErrPluginNotFound)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_kv").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger").Scan(&count))
	require.Equal(t, 1, count)
}

// TestPluginMigrationRepeatAndFutureManifestRead reruns migration 305 against its already migrated schema and stores a future declaration.
// Repetition preserves retained KV, and unknown future fields remain available without breaking installation listing.
func TestPluginMigrationRepeatAndFutureManifestRead(t *testing.T) {
	store, _, db := persistenceFixture(t)
	script, err := migrations.FS.ReadFile("305_plugins.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), string(script))
	require.NoError(t, err)
	item := storedInstallation(t)
	var declaration map[string]any
	require.NoError(t, json.Unmarshal(item.ManifestJSON, &declaration))
	declaration["minHostVersion"] = "9.0.0"
	declaration["providers"] = []any{map[string]any{"type": "future"}}
	item.ManifestJSON, err = json.Marshal(declaration)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), item))
	items, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Contains(t, string(items[0].ManifestJSON), "providers")
	require.Equal(t, "9.0.0", items[0].Manifest.MinHostVersion)
	_, err = db.ExecContext(t.Context(), `INSERT INTO plugin_kv(plugin_id,collection,key,value) VALUES('test.plugin','saved','row','{"value":1}')`)
	require.NoError(t, err)
	require.NoError(t, store.Delete(t.Context(), item.PluginID, false))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_kv").Scan(&count))
	require.Equal(t, 1, count)
}

// TestVersionExceptionPersistence retains distinct administrator decisions for current and previous packages.
// Repeating migration 307 preserves existing rows, and subsequent reads reconstruct each flag without copying it across snapshots.
func TestVersionExceptionPersistence(t *testing.T) {
	store, _, db := persistenceFixture(t)
	item := storedInstallation(t)
	previous := item.VersionSnapshot
	item.Previous = &previous
	item.IgnoreHostVersion = true
	require.NoError(t, store.Save(t.Context(), item))
	script, err := migrations.FS.ReadFile("307_plugin_install_overrides.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), string(script))
	require.NoError(t, err)
	read, err := store.Get(t.Context(), item.PluginID)
	require.NoError(t, err)
	require.True(t, read.IgnoreHostVersion)
	require.False(t, read.Previous.IgnoreHostVersion)
	current := read.VersionSnapshot
	read.VersionSnapshot = *read.Previous
	read.Previous = &current
	require.NoError(t, store.Save(t.Context(), read))
	items, err := store.List(t.Context())
	require.NoError(t, err)
	require.False(t, items[0].IgnoreHostVersion)
	require.True(t, items[0].Previous.IgnoreHostVersion)
}
