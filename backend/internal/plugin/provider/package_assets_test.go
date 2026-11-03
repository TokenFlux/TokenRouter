package provider

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestPackageAssetLookupIsLocalAndBoundsDescriptorGrowth opens a published package and then appends bytes after acquiring its asset handle.
// The reader exposes only the accepted range, missing versions never download, and undeclared paths or smaller caller limits fail before serving content.
func TestPackageAssetLookupIsLocalAndBoundsDescriptorGrowth(t *testing.T) {
	files, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	upload, err := files.Preview(t.Context(), bytes.NewReader(packageArchive(t, "1.0.0")))
	require.NoError(t, err)
	installed, err := files.Ensure(t.Context(), uploadReference(upload))
	require.NoError(t, err)
	local, err := files.Local(t.Context(), uploadReference(upload))
	require.NoError(t, err)
	require.Equal(t, installed.Files, local.Files)
	_, err = files.Local(t.Context(), plugin.PackageRef{PluginID: "test.plugin", Version: "2.0.0"})
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	for _, name := range []string{"../plugin.json", "missing.js"} {
		_, err = files.Open(t.Context(), local, name, 8<<20)
		require.ErrorIs(t, err, plugin.ErrPackageMissing)
	}
	_, err = files.Open(t.Context(), local, "frontend/index.js", 1)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	asset, err := files.Open(t.Context(), local, "frontend/index.js", 8<<20)
	require.NoError(t, err)
	defer func() { require.NoError(t, asset.Reader.Close()) }()
	file, err := os.OpenFile(filepath.Join(local.Directory, "frontend/index.js"), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteString("unexpected growth")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	content, err := io.ReadAll(asset.Reader)
	require.NoError(t, err)
	require.Equal(t, "globalThis.fixture = true;", string(content))
	_, err = files.Open(t.Context(), local, "frontend/index.js", 8<<20)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
}

// TestPackageAssetRejectsLinkedAndEscapingFiles replaces a declared script with a symbolic link after package installation.
// Public opening rejects the link instead of following its target, and invalid version or plugin identifiers cannot select an external directory.
func TestPackageAssetRejectsLinkedAndEscapingFiles(t *testing.T) {
	files, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	upload, err := files.Preview(t.Context(), bytes.NewReader(packageArchive(t, "1.0.0")))
	require.NoError(t, err)
	local, err := files.Ensure(t.Context(), uploadReference(upload))
	require.NoError(t, err)
	name := filepath.Join(local.Directory, "frontend/index.js")
	require.NoError(t, os.Remove(name))
	require.NoError(t, os.Symlink(filepath.Join(local.Directory, "plugin.json"), name))
	_, err = files.Open(t.Context(), local, "frontend/index.js", 8<<20)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	require.NoError(t, os.RemoveAll(filepath.Join(local.Directory, "frontend")))
	require.NoError(t, os.Mkdir(filepath.Join(local.Directory, "relocated"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(local.Directory, "relocated/index.js"), []byte("globalThis.fixture = true;"), 0o600))
	require.NoError(t, os.Symlink("relocated", filepath.Join(local.Directory, "frontend")))
	_, err = files.Open(t.Context(), local, "frontend/index.js", 8<<20)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	_, err = files.Local(t.Context(), plugin.PackageRef{PluginID: "../test.plugin", Version: "1.0.0"})
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	_, err = files.Local(t.Context(), plugin.PackageRef{PluginID: "test.plugin", Version: "../1.0.0"})
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
}
