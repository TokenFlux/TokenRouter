package provider

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// package_repair_scenario_test.go covers package_files.go, package_assets.go, and
// plugin/service.go through actual upload, asset, and repair operations. A local
// installation store replaces only persistence; package integrity and management
// decisions use production implementations without launching a plugin process.

// repairInstallationStore retains one installation for package repair scenarios.
type repairInstallationStore struct {
	plugin.InstallationStore                      // unused listing and deletion methods
	item                     *plugin.Installation // accepted installation record
}

// Get returns a decoded installation copy or the normal missing-record result.
// Test callers can mutate their returned record without changing stored state.
// Only Save publishes new installation intent in this fixture.
func (s *repairInstallationStore) Get(_ context.Context, id string) (*plugin.Installation, error) {
	if s.item == nil || s.item.PluginID != id {
		return nil, plugin.ErrPluginNotFound
	}
	return copyRepairInstallation(s.item)
}

// Save retains an independently decoded copy of the supplied installation record.
// Later mutations of the caller's containers cannot alter stored intent, matching
// the persistence boundary used by management operations.
func (s *repairInstallationStore) Save(_ context.Context, item *plugin.Installation) error {
	copied, err := copyRepairInstallation(item)
	if err != nil {
		return err
	}
	s.item = copied
	return nil
}

// copyRepairInstallation duplicates nested declaration and configuration containers through JSON.
// The returned record belongs to the caller, so mutations cannot leak between
// the service's working snapshot and this fixture's stored installation.
func copyRepairInstallation(item *plugin.Installation) (*plugin.Installation, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var copied plugin.Installation
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, err
	}
	return &copied, nil
}

// TestPackageRepairRestoresSameSizeCorruption damages an installed browser asset
// without changing its size. A fresh upload of the original package must restore
// its contents and preserve installation intent through the public repair API.
func TestPackageRepairRestoresSameSizeCorruption(t *testing.T) {
	archive := packageArchive(t, "1.0.0")
	packages, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	processes := NewProcesses(plugin.RuntimeOptions{}, nil, nil)
	manager := plugin.NewManager(
		plugin.ManagerOptions{Enabled: true, MaxRunning: 1, HostVersion: "0.6.0"},
		&repairInstallationStore{}, packages, processes, plugin.NewConfiguration(nil, false), nil,
	)
	// Cleanup gets its own deadline because the test context is canceled before
	// cleanup callbacks run, while lifecycle shutdown must still finish its work.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, manager.Stop(ctx))
	})
	service := plugin.NewService(manager, packages, nil, nil)
	upload, err := service.Preview(t.Context(), bytes.NewReader(archive))
	require.NoError(t, err)
	initial, err := service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: upload.Token, Enable: true}, 1)
	require.NoError(t, err)
	local, err := packages.Local(t.Context(), uploadReference(upload))
	require.NoError(t, err)
	filename := filepath.Join(local.Directory, "frontend/index.js")
	original, err := os.ReadFile(filename)
	require.NoError(t, err)
	damaged := bytes.Clone(original)
	damaged[0] ^= 1
	require.NoError(t, os.WriteFile(filename, damaged, 0o644))
	asset, err := packages.Open(t.Context(), local, "frontend/index.js", 8<<20)
	require.NoError(t, err)
	served, err := io.ReadAll(asset.Reader)
	require.NoError(t, err)
	require.NoError(t, asset.Reader.Close())
	digest := sha512.Sum384(served)
	require.NotEqual(t, asset.Metadata.SRI, "sha384-"+base64.StdEncoding.EncodeToString(digest[:]))
	ref := uploadReference(upload)
	ref.UploadToken = ""
	_, err = packages.Ensure(t.Context(), ref)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	fresh, err := service.Preview(t.Context(), bytes.NewReader(archive))
	require.NoError(t, err)
	repaired, err := service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: fresh.Token}, 2)
	require.NoError(t, err)
	require.True(t, repaired.Repaired)
	require.True(t, repaired.Installation.Enabled)
	require.Equal(t, initial.Installation.ManifestSHA256, repaired.Installation.ManifestSHA256)
	restored, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Equal(t, original, restored)
	_, err = service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: fresh.Token}, 3)
	require.ErrorIs(t, err, plugin.ErrPluginAlreadyInstalled)
}

// TestForcedSameVersionPackagesRetainIndependentRollbackBytes installs three different builds with one unchanged manifest and version.
// Legacy version directories remain usable, only the current and previous archives survive, and rollback serves the previous build's actual bytes.
func TestForcedSameVersionPackagesRetainIndependentRollbackBytes(t *testing.T) {
	packages, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	processes := NewProcesses(plugin.RuntimeOptions{}, nil, nil)
	manager := plugin.NewManager(plugin.ManagerOptions{Enabled: true, MaxRunning: 1, HostVersion: "0.6.0"},
		&repairInstallationStore{}, packages, processes, plugin.NewConfiguration(nil, false), nil)
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	service := plugin.NewService(manager, packages, nil, nil)
	first, err := packages.Preview(t.Context(), bytes.NewReader(packageArchive(t, "1.0.0")))
	require.NoError(t, err)
	original, err := packages.Ensure(t.Context(), uploadReference(first))
	require.NoError(t, err)
	legacy := filepath.Join(packages.root, "installed", "test.plugin", "1.0.0")
	require.NoError(t, os.Rename(original.Directory, legacy))
	_, err = service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: first.Token, Enable: true}, 1)
	require.NoError(t, err)
	for _, script := range []string{"globalThis.fixture = 2;", "globalThis.fixture = 3;"} {
		directory := filepath.Join(t.TempDir(), "package")
		_, err := pkgfmt.Extract(bytes.NewReader(packageArchive(t, "1.0.0")), directory, pkgfmt.Options{Platform: "any"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(directory, "frontend/index.js"), []byte(script), 0o644))
		archive, err := pkgfmt.Pack(directory, filepath.Join(t.TempDir(), "package.tar.gz"), pkgfmt.Options{Platform: "any"})
		require.NoError(t, err)
		data, err := os.ReadFile(archive.Path)
		require.NoError(t, err)
		upload, err := packages.Preview(t.Context(), bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, first.Snapshot.ManifestSHA256, upload.Snapshot.ManifestSHA256)
		_, err = service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceUpload, UploadToken: upload.Token, Force: true}, 1)
		require.NoError(t, err)
		loaded, ok := manager.Snapshot("test.plugin")
		require.True(t, ok)
		actual, err := os.ReadFile(filepath.Join(loaded.Package.Directory, "frontend/index.js"))
		require.NoError(t, err)
		require.Equal(t, script, string(actual))
	}
	entries, err := os.ReadDir(filepath.Dir(legacy))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoDirExists(t, legacy)
	result, err := service.Rollback(t.Context(), "test.plugin", 1)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", result.Installation.Version)
	require.Equal(t, "1.0.0", result.Installation.Previous.Version)
	require.NoError(t, manager.Restart(t.Context(), "test.plugin"))
	loaded, ok := manager.Snapshot("test.plugin")
	require.True(t, ok)
	actual, err := os.ReadFile(filepath.Join(loaded.Package.Directory, "frontend/index.js"))
	require.NoError(t, err)
	require.Equal(t, "globalThis.fixture = 2;", string(actual))
}
