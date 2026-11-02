//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// source_removal_scenario_integration_test.go verifies failed-update recovery
// across service_versions.go, catalog_indexes.go, and installations.go. PostgreSQL
// applies real foreign-key changes while a supervisor fixture deletes the accepted
// source after candidate persistence and before reporting a failed startup.
//
// The scenario enters production administration and reconciliation, preserving
// their plugin and source locking boundaries. Only package and child-process
// operations use substitutes, allowing the source deletion to occur at a precise
// point without starting executable code. Recovery must restore the original
// current and previous snapshots, detach the removed source, and retain an older
// snapshot's independent source relationship when that registration still exists.

// sourceRemovalProcesses exposes startup failure after the accepted source is deleted.
type sourceRemovalProcesses struct {
	state plugin.Status // current synthetic lifecycle observation
	// deleteSource runs only when the candidate starts, after the production service has persisted its replacement installation.
	deleteSource func(context.Context) error
}

// sourceRemovalPackages avoids filesystem effects while exercising the production management ordering.
type sourceRemovalPackages struct{}

// sourceRemovalResolver retains the target already reviewed before its source disappears.
type sourceRemovalResolver struct {
	target plugin.VersionSnapshot // independently accepted candidate release
}

// TestUpdateRestoresHistoryAfterSourceRemoval deletes a source after the new installation is saved and then rejects candidate startup.
// Recovery restores the entire earlier history, detaches the deleted source, and keeps the previous version's surviving source reference.
func TestUpdateRestoresHistoryAfterSourceRemoval(t *testing.T) {
	store, _, _ := persistenceFixture(t)
	source := &plugin.IndexSource{Name: "Current", URL: "https://example.invalid/current.json", Enabled: true}
	priorSource := &plugin.IndexSource{Name: "Previous", URL: "https://example.invalid/previous.json", Enabled: true}
	require.NoError(t, store.SaveIndex(t.Context(), source))
	require.NoError(t, store.SaveIndex(t.Context(), priorSource))
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","backend":{"executable":"bin/plugin"}}`)
	declaration, err := manifest.Parse(raw)
	require.NoError(t, err)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	original := &plugin.Installation{
		PluginID: "test.plugin",
		Enabled:  true,
		VersionSnapshot: plugin.VersionSnapshot{
			Version:        "1.0.0",
			Source:         plugin.SourceIndex,
			IndexID:        &source.ID,
			Manifest:       declaration,
			ManifestSHA256: strings.Repeat("a", 64),
			Artifacts: map[string]index.Artifact{
				platform: {URL: "https://example.invalid/v1.tar.gz", SHA256: strings.Repeat("b", 64), Size: 1},
			},
			Config: map[string]any{},
		},
	}
	previous := original.VersionSnapshot
	previous.Version = "0.9.0"
	previous.IndexID = &priorSource.ID
	previousManifest := *declaration
	previousManifest.Version = previous.Version
	previous.Manifest = &previousManifest
	previous.Config = map[string]any{"removed": "previous-value"}
	previous.SecretConfig = "previous-encrypted-secret"
	original.Previous = &previous
	require.NoError(t, store.Save(t.Context(), original))
	candidate := original.VersionSnapshot
	nextManifest := *declaration
	nextManifest.Version = "2.0.0"
	candidate.Manifest = &nextManifest
	candidate.Version = "2.0.0"
	candidate.ManifestSHA256 = strings.Repeat("c", 64)
	candidate.Artifacts = map[string]index.Artifact{
		platform: {URL: "https://example.invalid/v2.tar.gz", SHA256: strings.Repeat("d", 64), Size: 1},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := plugin.NewCatalogService(plugin.CatalogOptions{Enabled: true}, store, store, nil, logger)
	t.Cleanup(func() { require.NoError(t, catalog.Stop(context.Background())) })
	processes := &sourceRemovalProcesses{
		deleteSource: func(ctx context.Context) error { return catalog.DeleteIndex(ctx, source.ID) },
	}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Enabled: true, MaxRunning: 1, HostVersion: "0.5.4", StateRoot: t.TempDir(),
	}, store, sourceRemovalPackages{}, processes, plugin.NewConfiguration(nil, false), logger)
	require.NoError(t, manager.Sync(t.Context(), original.PluginID))
	service := plugin.NewService(manager, nil, sourceRemovalResolver{target: candidate}, nil)
	// Candidate replacement deletes its source after persistence, forcing recovery
	// to resolve the stale current association without discarding the older history.
	_, err = service.Update(t.Context(), original.PluginID, plugin.UpdateInput{Version: "2.0.0"}, 0)
	require.ErrorIs(t, err, plugin.ErrUpdateFailed)
	metadata := apperror.FromError(err).Metadata
	require.Empty(t, metadata["rollback_error"])
	require.Equal(t, "1.0.0", metadata["restored_version"])
	actual, err := store.Get(t.Context(), original.PluginID)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", actual.Version)
	require.Equal(t, "0.9.0", actual.Previous.Version)
	require.Nil(t, actual.IndexID)
	require.NotNil(t, actual.Previous.IndexID)
	require.Equal(t, priorSource.ID, *actual.Previous.IndexID)
	require.Equal(t, original.Artifacts, actual.Artifacts)
	require.Equal(t, original.Previous.Config, actual.Previous.Config)
	require.Equal(t, original.Previous.SecretConfig, actual.Previous.SecretConfig)
	require.Equal(t, plugin.StateRunning, manager.Status(original.PluginID).State)
}

// Replace deletes the catalog only after version two has been persisted.
// The operation then reports failed readiness, forcing the production update service
// to save its earlier complete installation snapshot through PostgreSQL.
func (p *sourceRemovalProcesses) Replace(ctx context.Context, spec plugin.ProcessSpec) error {
	if spec.Version == "2.0.0" {
		if err := p.deleteSource(ctx); err != nil {
			return err
		}
		p.state = plugin.Status{State: plugin.StateFailed, LastError: "candidate Init failed"}
		return nil
	}
	p.state = plugin.Status{State: plugin.StateRunning}
	return nil
}

// Acquire rejects unused calls because this test owns no RPC connections.
func (p *sourceRemovalProcesses) Acquire(context.Context, string) (*plugin.Lease, error) {
	return nil, plugin.ErrPluginUnavailable
}

// Stop records completed synthetic draining without any child process resources.
func (p *sourceRemovalProcesses) Stop(context.Context, string) error {
	p.state = plugin.Status{State: plugin.StateStopped}
	return nil
}

// ApplyConfig accepts unused configuration notifications without altering the scenario state.
func (p *sourceRemovalProcesses) ApplyConfig(context.Context, plugin.ProcessSpec) error {
	return nil
}

// Status returns the fixture observation used by readiness and restoration checks.
func (p *sourceRemovalProcesses) Status(string) plugin.Status { return p.state }

// StopAll completes cleanup because the fixture never launches native children.
func (p *sourceRemovalProcesses) StopAll(context.Context) error { return nil }

// Ensure returns accepted release metadata without creating an executable directory.
func (sourceRemovalPackages) Ensure(_ context.Context, ref plugin.PackageRef) (plugin.Package, error) {
	return plugin.Package{Executable: "/unused", ManifestSHA256: ref.ManifestSHA256}, nil
}

// Prune accepts retained generations because no managed files exist here.
func (sourceRemovalPackages) Prune(context.Context, string, ...plugin.PackageRef) error { return nil }

// Remove accepts cleanup because this fixture owns no filesystem state.
func (sourceRemovalPackages) Remove(context.Context, string) error { return nil }

// Resolve supplies the reviewed candidate before deletion occurs during startup.
func (r sourceRemovalResolver) Resolve(context.Context, plugin.InstallInput) (plugin.VersionSnapshot, error) {
	return r.target, nil
}
