package provider

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestLocalExportStoreCommitOpenDeleteRoundTrip verifies that writing to a
// confined key stages bytes in a .part file until Commit atomically publishes
// the CSV, Open streams the exact payload, and Delete removes the artifact.
func TestLocalExportStoreCommitOpenDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalExportStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	key := "user/usage-export-42.csv"
	writer, err := store.CreateWriter(key)
	require.NoError(t, err)

	_, _, openBeforeCommitErr := store.Open(key)
	require.ErrorIs(t, openBeforeCommitErr, usage.ErrExportArtifactNotFound)

	payload := "\xEF\xBB\xBFTime,Model\n2026-04-16T00:00:00Z,gpt-5.4\n"
	n, err := writer.Write([]byte(payload))
	require.NoError(t, err)
	require.Equal(t, len(payload), n)

	size, err := writer.Commit()
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), size)
	require.NoError(t, writer.Abort())

	rc, openedSize, err := store.Open(key)
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), openedSize)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, payload, string(got))

	require.NoError(t, store.Delete(key))
	_, _, afterDeleteErr := store.Open(key)
	require.ErrorIs(t, afterDeleteErr, usage.ErrExportArtifactNotFound)
}

// TestLocalExportStoreAbortAndSweepStalePartFiles verifies that Abort removes
// uncommitted .part files immediately and SweepStalePartFiles removes only aged
// staging files while preserving fresh .part files and committed CSV artifacts.
func TestLocalExportStoreAbortAndSweepStalePartFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalExportStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	wAbort, err := store.CreateWriter("user/aborted.csv")
	require.NoError(t, err)
	_, err = wAbort.Write([]byte("partial"))
	require.NoError(t, err)
	require.NoError(t, wAbort.Abort())
	_, statErr := os.Stat(filepath.Join(dir, "user", "aborted.csv.part"))
	require.True(t, os.IsNotExist(statErr))

	wCommitted, err := store.CreateWriter("admin/committed.csv")
	require.NoError(t, err)
	_, err = wCommitted.Write([]byte("ok"))
	require.NoError(t, err)
	_, err = wCommitted.Commit()
	require.NoError(t, err)

	stalePart := filepath.Join(dir, "user", "stale.csv.part")
	freshPart := filepath.Join(dir, "user", "fresh.csv.part")
	require.NoError(t, os.WriteFile(stalePart, []byte("old"), 0o640))
	require.NoError(t, os.WriteFile(freshPart, []byte("new"), 0o640))

	baseNow := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseNow }
	require.NoError(t, os.Chtimes(stalePart, baseNow.Add(-2*time.Hour), baseNow.Add(-2*time.Hour)))
	require.NoError(t, os.Chtimes(freshPart, baseNow.Add(-5*time.Minute), baseNow.Add(-5*time.Minute)))

	removed, err := store.SweepStalePartFiles(30 * time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	_, err = os.Stat(stalePart)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(freshPart)
	require.NoError(t, err)

	rc, _, err := store.Open("admin/committed.csv")
	require.NoError(t, err)
	require.NoError(t, rc.Close())
}

// TestLocalExportStoreRejectsUnsafeKeys passes empty keys, path traversal
// segments, absolute paths, backslashes, and reserved .part suffixes to
// LocalExportStore. CreateWriter, Open, and Delete reject every unsafe key
// before performing any filesystem mutation.
func TestLocalExportStoreRejectsUnsafeKeys(t *testing.T) {
	store, err := NewLocalExportStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	for _, badKey := range []string{
		"",
		"../secret.csv",
		"user/../../secret.csv",
		"/etc/passwd",
		`user\file.csv`,
		"user/task.csv.part",
	} {
		_, createErr := store.CreateWriter(badKey)
		require.Error(t, createErr, "expected error for key %q", badKey)
		_, _, openErr := store.Open(badKey)
		require.Error(t, openErr, "expected error for key %q", badKey)
		require.Error(t, store.Delete(badKey), "expected error for key %q", badKey)
	}
}
