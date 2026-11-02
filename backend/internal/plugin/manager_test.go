package plugin

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// delayedInstallationSource pauses initial identity discovery before ordinary management can proceed.
type delayedInstallationSource struct {
	*memoryInstallations               // durable fixture installation storage
	entered              chan struct{} // source discovery arrival notification
	release              chan struct{} // source discovery completion barrier
}

// TestManagerReconcilesTaskRegistrationWithInstallationState publishes one declared task and then disables, re-enables, and removes its installation.
// Future occurrences follow manager admission, suspension retains task metadata, and uninstall clears the in-memory registration after process draining succeeds.
func TestManagerReconcilesTaskRegistrationWithInstallationState(t *testing.T) {
	manager, service, store, _, _ := managerFixture(t, 2)
	item := managerInstallation(t, "test.tasks", true)
	item.Manifest.Backend.Tasks = []manifest.Task{{Name: "tick", Schedule: "@every 1m"}}
	require.NoError(t, store.Save(t.Context(), item))
	require.NoError(t, manager.Sync(t.Context(), item.PluginID))
	states := manager.TaskStatuses(item.PluginID)
	require.Len(t, states, 1)
	require.False(t, states[0].NextRunAt.IsZero())
	_, err := service.Disable(t.Context(), item.PluginID, 1)
	require.NoError(t, err)
	states = manager.TaskStatuses(item.PluginID)
	require.Len(t, states, 1)
	require.True(t, states[0].NextRunAt.IsZero())
	_, err = service.Enable(t.Context(), item.PluginID, 1)
	require.NoError(t, err)
	require.False(t, manager.TaskStatuses(item.PluginID)[0].NextRunAt.IsZero())
	require.NoError(t, service.Uninstall(t.Context(), item.PluginID, false))
	require.Empty(t, manager.TaskStatuses(item.PluginID))
}

// TestAssetReferenceUsesAcceptedReleaseMetadata resolves icons without publishing package snapshots or starting a process.
// Current and previous declarations remain distinct, while an unknown version or mismatched manifest identity cannot authorize another package path.
func TestAssetReferenceUsesAcceptedReleaseMetadata(t *testing.T) {
	manager, _, store, _, _ := managerFixture(t, 1)
	item := managerInstallation(t, "test.icons", false)
	item.Manifest.Icon = "art/current.svg"
	previous := item.VersionSnapshot
	previous.Version = "0.9.0"
	declaration := *previous.Manifest
	declaration.Version = previous.Version
	declaration.Icon = "art/previous.png"
	previous.Manifest = &declaration
	item.Previous = &previous
	require.NoError(t, store.Save(t.Context(), item))
	icon, err := manager.AssetReference(t.Context(), item.PluginID, "1.0.0")
	require.NoError(t, err)
	require.Equal(t, "art/current.svg", icon.Icon)
	icon, err = manager.AssetReference(t.Context(), item.PluginID, "0.9.0")
	require.NoError(t, err)
	require.Equal(t, "art/previous.png", icon.Icon)
	_, err = manager.AssetReference(t.Context(), item.PluginID, "2.0.0")
	require.ErrorIs(t, err, ErrPluginNotFound)
	item.Previous.Manifest.ID = "different.plugin"
	require.NoError(t, store.Save(t.Context(), item))
	_, err = manager.AssetReference(t.Context(), item.PluginID, "0.9.0")
	require.ErrorIs(t, err, ErrPluginNotFound)
}

// TestManagerStartupSharesOneProcessLimit restores enabled, disabled, and development installations in deterministic order.
// Development records consume the same native capacity, while disabled records remain visible without starting.
func TestManagerStartupSharesOneProcessLimit(t *testing.T) {
	manager, _, store, processes, _ := managerFixture(t, 1)
	first := managerInstallation(t, "test.alpha", true)
	first.Source = SourceDev
	first.LocalDirectory = t.TempDir()
	require.NoError(t, store.Save(t.Context(), first))
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.zulu", true)))
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.disabled", false)))
	manager.SyncAll(t.Context())
	require.Len(t, processes.started, 1)
	require.Equal(t, "test.alpha", processes.started[0].PluginID)
	require.False(t, processes.started[0].VerifyBinary)
	require.Equal(t, StateFailed, manager.Status("test.zulu").State)
	require.Contains(t, manager.Status("test.zulu").LastError, "MAX_RUNNING_EXCEEDED")
	require.Equal(t, StateStopped, manager.Status("test.disabled").State)
	require.NoError(t, manager.Sync(t.Context(), "test.alpha"))
	require.Len(t, processes.started, 1)
}

// TestManagerRetriesUnappliedConfiguration reconciles the same persisted values after the runtime rejects their first delivery.
// Publishing desired configuration must not suppress a later attempt to apply it, and successful retry reopens ordinary request admission.
func TestManagerRetriesUnappliedConfiguration(t *testing.T) {
	manager, _, store, processes, _ := managerFixture(t, 1)
	item := managerInstallation(t, "test.config", true)
	require.NoError(t, store.Save(t.Context(), item))
	require.NoError(t, manager.Sync(t.Context(), item.PluginID))
	failure := errors.New("fixture configuration delivery failed")
	processes.configError = failure
	require.ErrorIs(t, manager.Sync(t.Context(), item.PluginID), failure)
	processes.configError = nil
	require.NoError(t, manager.Sync(t.Context(), item.PluginID))
	require.Equal(t, 2, processes.configCalls)
	require.Len(t, processes.started, 1)
	lease, err := manager.Acquire(t.Context(), item.PluginID)
	require.NoError(t, err)
	lease.Release()
}

// TestManagerShutdownClosesAdmission leaves a loaded plugin enabled before stopping the host owner.
// Later lease acquisition and restart cannot create another running process generation after terminal shutdown.
func TestManagerShutdownClosesAdmission(t *testing.T) {
	manager, _, store, processes, _ := managerFixture(t, 1)
	require.NoError(t, store.Save(t.Context(), managerInstallation(t, "test.plugin", true)))
	require.NoError(t, manager.Sync(t.Context(), "test.plugin"))
	require.NoError(t, manager.Stop(context.Background()))
	_, err := manager.Acquire(t.Context(), "test.plugin")
	require.ErrorIs(t, err, ErrPluginUnavailable)
	require.ErrorIs(t, manager.Restart(t.Context(), "test.plugin"), ErrPluginsDisabled)
	require.Len(t, processes.started, 1)
}

// Load blocks identity discovery until released or the manager cancels initialization.
func (s *delayedInstallationSource) Load(ctx context.Context) error {
	close(s.entered)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestManagerWaitsForDevelopmentIdentityDiscovery holds source initialization while a mutation attempts to acquire its plugin lock.
// The mutation cannot run before discovery completes, and its canceled context leaves installation and package state untouched.
func TestManagerWaitsForDevelopmentIdentityDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, _, store, _, packages := managerFixture(t, 1)
		source := &delayedInstallationSource{memoryInstallations: store, entered: make(chan struct{}), release: make(chan struct{})}
		manager.store = source
		require.NoError(t, manager.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
		<-source.entered
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- manager.Sync(ctx, "test.plugin") }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("mutation passed incomplete identity discovery: %v", err)
		default:
		}
		require.Empty(t, packages.removed)
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)
		close(source.release)
	})
}
