package provider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// TestPackagesRecoverIndexedArchiveAndRequireUploadRepair installs the same static archive through both source types.
// Indexed installations recover missing or corrupted files over verified HTTPS,
// while uploaded installations require a fresh matching token after restart.
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
	assetPath := filepath.Join(recovered.Directory, "frontend/index.js")
	original, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	damaged := bytes.Clone(original)
	damaged[0] ^= 1
	require.NoError(t, os.WriteFile(assetPath, damaged, 0o644))
	_, err = packages.Ensure(t.Context(), ref)
	require.NoError(t, err)
	restored, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	require.Equal(t, original, restored)
	ref.Artifact.SHA256 = strings.Repeat("0", 64)
	_, err = packages.Ensure(t.Context(), ref)
	require.ErrorIs(t, err, plugin.ErrChecksumMismatch)
	entries, err := os.ReadDir(filepath.Join(packages.root, "staging"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "uploads", entries[0].Name())
}

// TestPackagesRetainLargeMarkers covers accepted packages with many long paths.
// Metadata larger than one MiB must survive publication, direct asset lookup,
// and reconstruction of the package store without requiring another upload.
func TestPackagesRetainLargeMarkers(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "package")
	_, err := pkgfmt.Extract(bytes.NewReader(packageArchive(t, "1.0.0")), directory, pkgfmt.Options{Platform: "any"})
	require.NoError(t, err)
	prefix := filepath.Join(directory, strings.Repeat("a", 200), strings.Repeat("b", 200))
	require.NoError(t, os.MkdirAll(prefix, 0o755))
	// Long shared directory prefixes cross the former metadata budget while keeping
	// file count, directory depth, and expanded content below every package limit.
	for i := range 1900 {
		require.NoError(t, os.WriteFile(filepath.Join(prefix, fmt.Sprintf("file-%04d.txt", i)), []byte("x"), 0o644))
	}
	archive, err := pkgfmt.Pack(directory, filepath.Join(t.TempDir(), "package.tar.gz"), pkgfmt.Options{Platform: "any"})
	require.NoError(t, err)
	data, err := os.ReadFile(archive.Path)
	require.NoError(t, err)
	root := t.TempDir()
	packages, err := NewPackageFiles(root, DownloadOptions{})
	require.NoError(t, err)
	upload, err := packages.Preview(t.Context(), bytes.NewReader(data))
	require.NoError(t, err)
	ref := uploadReference(upload)
	installed, err := packages.Ensure(t.Context(), ref)
	require.NoError(t, err)
	marker, err := os.Stat(filepath.Join(installed.Directory, pkgfmt.ReservedMetadataName))
	require.NoError(t, err)
	require.Greater(t, marker.Size(), int64(1<<20))
	local, err := packages.Local(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, installed.Files, local.Files)
	restarted, err := NewPackageFiles(root, DownloadOptions{})
	require.NoError(t, err)
	ref.UploadToken = ""
	recovered, err := restarted.Ensure(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, installed.Files, recovered.Files)
}

// TestPackageFilesRejectEscapesAndRetainPrevious rejects invalid identity components before any path mutation.
// Pruning keeps current and rollback versions, and removing managed storage never follows unrelated development directory paths.
func TestPackageFilesRejectEscapesAndRetainPrevious(t *testing.T) {
	packages, err := NewPackageFiles(t.TempDir(), DownloadOptions{})
	require.NoError(t, err)
	require.Error(t, packages.Remove(t.Context(), "../outside"))
	require.Error(t, packages.Prune(t.Context(), "../outside"))
	refs := make([]plugin.PackageRef, 0, 3)
	paths := make([]string, 0, 3)
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		upload, err := packages.Preview(t.Context(), bytes.NewReader(packageArchive(t, version)))
		require.NoError(t, err)
		ref := uploadReference(upload)
		local, err := packages.Ensure(t.Context(), ref)
		require.NoError(t, err)
		refs = append(refs, ref)
		paths = append(paths, local.Directory)
	}
	require.NoError(t, packages.Prune(context.Background(), "test.plugin", refs[1:]...))
	_, err = os.Stat(paths[0])
	require.True(t, os.IsNotExist(err))
	require.DirExists(t, paths[1])
	require.DirExists(t, paths[2])
}
