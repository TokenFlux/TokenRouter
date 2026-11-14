package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// data_files_test.go verifies owner-directory persistence and scoped removal.
// Tests use real private directories to exercise restart discovery, symlink
// rejection, cancellation, and isolation from disposable package cleanup.

// TestDataFilesSurvivePackageRemovalAndRestart writes business data alongside disposable state.
// Ordinary package cleanup and rebuilding the file-store adapter retain the owner's bytes and usage.
func TestDataFilesSurvivePackageRemovalAndRestart(t *testing.T) {
	base := t.TempDir()
	data, err := NewDataFiles(filepath.Join(base, "data"))
	require.NoError(t, err)
	directory, err := data.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "plugin.sqlite"), []byte("business records"), 0o600))
	packages, err := NewPackageFiles(base, DownloadOptions{})
	require.NoError(t, err)
	require.NoError(t, packages.Remove(t.Context(), "test.plugin"))
	restarted, err := NewDataFiles(filepath.Join(base, "data"))
	require.NoError(t, err)
	items, err := restarted.List(t.Context())
	require.NoError(t, err)
	require.Equal(t, []plugin.DataUsage{{PluginID: "test.plugin", Bytes: 16, Files: 1}}, items)
	same, err := restarted.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	require.Equal(t, directory, same)
}

// TestDataFilesRejectEscapingOwners creates another owner's files and a linked alias.
// Invalid identifiers and linked directories cannot expose those files, while purging the alias retains its target.
func TestDataFilesRejectEscapingOwners(t *testing.T) {
	data, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	other, err := data.Directory(t.Context(), "test.other")
	require.NoError(t, err)
	filename := filepath.Join(other, "keep")
	require.NoError(t, os.WriteFile(filename, []byte("private"), 0o600))
	for _, id := range []string{"../test.other", "", "/tmp/test.plugin", "test.plugin/../test.other"} {
		_, err := data.Directory(t.Context(), id)
		require.ErrorIs(t, err, plugin.ErrInvalidArgument)
		require.ErrorIs(t, data.Remove(t.Context(), id), plugin.ErrInvalidArgument)
	}
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links requires platform-specific privileges")
	}
	require.NoError(t, os.Symlink(other, filepath.Join(data.root, "test.alias")))
	_, err = data.Directory(t.Context(), "test.alias")
	require.ErrorIs(t, err, plugin.ErrDataInvalid)
	require.NoError(t, data.Remove(t.Context(), "test.alias"))
	require.FileExists(t, filename)
	require.NoError(t, data.Remove(t.Context(), "test.other"))
	require.NoError(t, data.Remove(t.Context(), "test.other"))
}

// TestDataFilesCancellationPreservesBusinessRecords cancels management before directory cleanup begins.
// Purging returns the context error and retains existing files for a later explicit retry.
func TestDataFilesCancellationPreservesBusinessRecords(t *testing.T) {
	data, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	directory, err := data.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	filename := filepath.Join(directory, "keep")
	require.NoError(t, os.WriteFile(filename, []byte("retained"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, data.Remove(ctx, "test.plugin"), context.Canceled)
	require.FileExists(t, filename)
}
