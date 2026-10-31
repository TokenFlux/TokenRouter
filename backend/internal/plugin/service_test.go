package plugin

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDisablePublishesIntentEvenWhenDrainFails saves a disabled installation before its process reports unfinished shutdown work.
// Both persistence and UI snapshots remain disabled, while native request admission stays blocked for a later administrative retry.
func TestDisablePublishesIntentEvenWhenDrainFails(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 1)
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", true)))
	require.NoError(t, manager.Sync(t.Context(), "test.plugin"))
	processes.stopError = errors.New("drain still pending")
	_, err := service.Disable(t.Context(), "test.plugin", 1)
	require.ErrorContains(t, err, "drain still pending")
	stored, err := store.Get(t.Context(), "test.plugin")
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	loaded, found := manager.Snapshot("test.plugin")
	require.True(t, found)
	require.False(t, loaded.Installation.Enabled)
	_, err = manager.Acquire(t.Context(), "test.plugin")
	require.ErrorIs(t, err, ErrPluginUnavailable)
}

// TestUninstallRetainsInstallationUntilCallbacksDrain blocks supervisor completion during a purging uninstall.
// New admission closes immediately, and the installation remains until the callback barrier confirms actual completion.
func TestUninstallRetainsInstallationUntilCallbacksDrain(t *testing.T) {
	manager, service, store, processes, packages := managerFixture(t, 1)
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", true)))
	require.NoError(t, manager.Sync(t.Context(), "test.plugin"))
	entered := make(chan struct{})
	processes.stopEntered = entered
	release := make(chan struct{})
	processes.stopRelease = release
	result := make(chan error, 1)
	go func() { result <- service.Uninstall(t.Context(), "test.plugin", true) }()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("uninstall failed before draining: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not start")
	}
	_, err := manager.Acquire(t.Context(), "test.plugin")
	require.ErrorIs(t, err, ErrPluginUnavailable)
	_, err = store.Get(t.Context(), "test.plugin")
	require.NoError(t, err)
	// Release the admitted callback barrier before asserting deletion, so the test observes the same drain-before-purge ordering as production.
	close(release)
	require.NoError(t, <-result)
	_, err = store.Get(t.Context(), "test.plugin")
	require.ErrorIs(t, err, ErrPluginNotFound)
	require.Equal(t, []bool{true}, store.purge)
	require.Equal(t, []string{"test.plugin"}, packages.removed)
}

// TestUninstallDrainFailureSupportsRetry injects unfinished host work into the first uninstall attempt.
// Installation and local files survive that failure, and a later successful drain permits one transactional deletion.
func TestUninstallDrainFailureSupportsRetry(t *testing.T) {
	manager, service, store, processes, packages := managerFixture(t, 1)
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", true)))
	require.NoError(t, manager.Sync(t.Context(), "test.plugin"))
	processes.stopError = errors.New("callback still running")
	require.Error(t, service.Uninstall(t.Context(), "test.plugin", true))
	_, err := store.Get(t.Context(), "test.plugin")
	require.NoError(t, err)
	require.Empty(t, store.deleted)
	require.Empty(t, packages.removed)
	processes.stopError = nil
	require.NoError(t, service.Uninstall(t.Context(), "test.plugin", true))
	require.Len(t, store.deleted, 1)
}

// TestConcurrentEnableThenDisableUsesLatestRecord holds enable inside process replacement while a disable request queues.
// The later mutation reads the saved enabled record and ultimately leaves no running generation.
func TestConcurrentEnableThenDisableUsesLatestRecord(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 1)
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", false)))
	processes.replaceEntered = make(chan struct{})
	processes.replaceRelease = make(chan struct{})
	enabled := make(chan error, 1)
	go func() {
		_, err := service.Enable(t.Context(), "test.plugin", 1)
		enabled <- err
	}()
	select {
	case <-processes.replaceEntered:
	case err := <-enabled:
		t.Fatalf("enable failed before replacement: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not start")
	}
	disabled := make(chan error, 1)
	go func() {
		_, err := service.Disable(t.Context(), "test.plugin", 2)
		disabled <- err
	}()
	close(processes.replaceRelease)
	require.NoError(t, <-enabled)
	require.NoError(t, <-disabled)
	item, err := store.Get(t.Context(), "test.plugin")
	require.NoError(t, err)
	require.False(t, item.Enabled)
	require.Equal(t, int64(2), *item.UpdatedBy)
	require.Equal(t, StateStopped, manager.Status("test.plugin").State)
}
