package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestUploadExpiryAndStartupCleanup checks failed extraction cleanup, fixed token expiry, and host restart invalidation.
// Preview never publishes an installation, and incomplete or expired uploads cannot later authorize executable installation.
func TestUploadExpiryAndStartupCleanup(t *testing.T) {
	root := t.TempDir()
	packages, err := NewPackageFiles(root, DownloadOptions{})
	require.NoError(t, err)
	_, err = packages.Preview(t.Context(), bytes.NewBufferString("invalid gzip"))
	require.ErrorIs(t, err, plugin.ErrPackageInvalid)
	entries, err := os.ReadDir(filepath.Join(root, "staging", "uploads"))
	require.NoError(t, err)
	require.Empty(t, entries)
	upload, err := packages.Preview(t.Context(), bytes.NewReader(packageArchive(t, "1.0.0")))
	require.NoError(t, err)
	require.Equal(t, "any", upload.Platform)
	_, err = packages.ResolveUpload(t.Context(), "../"+upload.Token)
	require.ErrorIs(t, err, plugin.ErrPackageInvalid)
	packages.now = func() time.Time { return upload.ExpiresAt }
	_, err = packages.ResolveUpload(t.Context(), upload.Token)
	require.ErrorIs(t, err, plugin.ErrPackageInvalid)
	restarted, err := NewPackageFiles(root, DownloadOptions{})
	require.NoError(t, err)
	_, err = restarted.ResolveUpload(t.Context(), upload.Token)
	require.ErrorIs(t, err, plugin.ErrPackageInvalid)
}
