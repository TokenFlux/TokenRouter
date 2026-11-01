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
	local, err := packages.Local(t.Context(), "test.plugin", "1.0.0")
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
