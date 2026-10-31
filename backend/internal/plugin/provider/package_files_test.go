package provider

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestPackagesRecoverIndexedArchiveAndRequireUploadRepair installs the same static archive through both source types.
// Indexed installations recover missing files over verified HTTPS, while uploaded installations require a fresh matching token after restart.
func TestPackagesRecoverIndexedArchiveAndRequireUploadRepair(t *testing.T) {
	archive := packageArchive(t, "1.0.0+build.7")
	packages, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	upload, err := packages.Preview(t.Context(), bytes.NewReader(archive))
	require.NoError(t, err)
	ref := uploadReference(upload)
	installed, err := packages.Ensure(t.Context(), ref)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(installed.Directory, ".tr-installed"))
	require.True(t, strings.HasPrefix(installed.Files["frontend/index.js"].SRI, "sha384-"))
	require.NoError(t, os.RemoveAll(installed.Directory))
	ref.UploadToken = ""
	_, err = packages.Ensure(t.Context(), ref)
	require.ErrorIs(t, err, plugin.ErrPackageMissing)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	packages.download.client.Transport = server.Client().Transport
	ref.Source = plugin.SourceIndex
	ref.Artifact.URL = server.URL + "/package.tar.gz"
	recovered, err := packages.Ensure(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, installed.ArchiveSHA256, recovered.ArchiveSHA256)
	require.NoError(t, os.Remove(filepath.Join(recovered.Directory, "frontend/index.js")))
	recovered, err = packages.Ensure(t.Context(), ref)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(recovered.Directory, "frontend/index.js"))
	ref.Artifact.SHA256 = strings.Repeat("0", 64)
	_, err = packages.Ensure(t.Context(), ref)
	require.ErrorIs(t, err, plugin.ErrChecksumMismatch)
	entries, err := os.ReadDir(filepath.Join(packages.root, "staging"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "uploads", entries[0].Name())
}

// TestPackageFilesRejectEscapesAndRetainPrevious rejects invalid identity components before any path mutation.
// Pruning keeps current and rollback versions, and removing managed storage never follows unrelated development directory paths.
func TestPackageFilesRejectEscapesAndRetainPrevious(t *testing.T) {
	packages, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	require.Error(t, packages.Remove(t.Context(), "../outside"))
	require.Error(t, packages.Prune(t.Context(), "../outside"))
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		require.NoError(t, os.MkdirAll(filepath.Join(packages.root, "installed", "test.plugin", version), 0o700))
	}
	require.NoError(t, packages.Prune(context.Background(), "test.plugin", "2.0.0", "3.0.0"))
	_, err = os.Stat(filepath.Join(packages.root, "installed", "test.plugin", "1.0.0"))
	require.True(t, os.IsNotExist(err))
	require.DirExists(t, filepath.Join(packages.root, "installed", "test.plugin", "2.0.0"))
	require.DirExists(t, filepath.Join(packages.root, "installed", "test.plugin", "3.0.0"))
}
