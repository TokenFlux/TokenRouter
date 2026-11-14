package plugin

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// data_test.go checks persistent file management against installation and process state.
// Storage callbacks are controlled independently from host KV, exposing ordering,
// retry, and stopped-owner decisions without starting an executable or external database.

// dataFixture supplies the management observations needed by persistent data tests.
type dataFixture struct {
	DataStorage          // methods unused by individual scenarios
	removed     []string // local owner purge attempts
	removeError error    // injected filesystem cleanup failure
	backups     int      // accepted offline snapshot attempts
	path        string   // persistent directory supplied to processes
}

// Directory returns the fixture's stable path for native process initialization.
func (f *dataFixture) Directory(context.Context, string) (string, error) {
	return f.path, nil
}

// Remove records each cleanup attempt and returns the configured filesystem outcome.
func (f *dataFixture) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return f.removeError
}

// Backup counts snapshots admitted after management has checked stopped-owner policy.
func (f *dataFixture) Backup(context.Context, string) (*DataArchive, error) {
	f.backups++
	return &DataArchive{Reader: io.NopCloser(strings.NewReader(""))}, nil
}

// TestDataDirectoryReachesNativeProcess starts one accepted native contribution.
// Its immutable process specification receives the durable path separately from disposable state, and ordinary uninstall retains it.
func TestDataDirectoryReachesNativeProcess(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 1)
	files := &dataFixture{path: "/fixture/persistent/test.plugin"}
	manager.data = files
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", true)))
	require.NoError(t, manager.Sync(t.Context(), "test.plugin"))
	require.Equal(t, files.path, processes.started[0].DataDir)
	require.NotEqual(t, processes.started[0].StateDir, processes.started[0].DataDir)
	require.NoError(t, service.Uninstall(t.Context(), "test.plugin", false))
	require.Empty(t, files.removed)
}

// TestDataPurgeRetriesAfterCommittedUninstall injects a local deletion failure after host records commit.
// Retrying the explicit purge removes remaining files without resurrecting installation intent or changing the retained ledger policy.
func TestDataPurgeRetriesAfterCommittedUninstall(t *testing.T) {
	manager, service, store, _, _ := managerFixture(t, 1)
	files := &dataFixture{removeError: errors.New("fixture disk failure")}
	manager.data = files
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", false)))
	require.ErrorIs(t, service.Uninstall(t.Context(), "test.plugin", true), files.removeError)
	_, err := store.Get(t.Context(), "test.plugin")
	require.ErrorIs(t, err, ErrPluginNotFound)
	files.removeError = nil
	require.NoError(t, service.PurgeData(t.Context(), "test.plugin"))
	require.Equal(t, []string{"test.plugin", "test.plugin"}, files.removed)
	require.Equal(t, []bool{true, true}, store.purge)
}

// TestDataBackupRequiresDisabledIntentAndCompletedDrain varies desired state and callback completion.
// Enabled or incompletely drained owners never reach snapshot creation; a stopped owner can produce a snapshot afterward.
func TestDataBackupRequiresDisabledIntentAndCompletedDrain(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 1)
	files := &dataFixture{}
	manager.data = files
	item := managerInstallation(t, "test.plugin", true)
	require.NoError(t, store.Save(t.Context(), item))
	_, err := service.BackupData(t.Context(), item.PluginID)
	require.ErrorIs(t, err, ErrDataRequiresStop)
	require.Zero(t, files.backups)
	item.Enabled = false
	require.NoError(t, store.Save(t.Context(), item))
	processes.stopError = errors.New("fixture callback still active")
	_, err = service.BackupData(t.Context(), item.PluginID)
	require.ErrorIs(t, err, processes.stopError)
	require.Zero(t, files.backups)
	processes.stopError = nil
	archive, err := service.BackupData(t.Context(), item.PluginID)
	require.NoError(t, err)
	require.NoError(t, archive.Reader.Close())
	require.Equal(t, 1, files.backups)
	require.ErrorIs(t, service.PurgeData(t.Context(), item.PluginID), ErrPluginAlreadyInstalled)
	require.Empty(t, files.removed)
}
