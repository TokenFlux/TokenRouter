package plugin

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

// delayedInstallationSource pauses initial identity discovery before ordinary management can proceed.
type delayedInstallationSource struct {
	*memoryInstallations               // durable fixture installation storage
	entered              chan struct{} // source discovery arrival notification
	release              chan struct{} // source discovery completion barrier
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
