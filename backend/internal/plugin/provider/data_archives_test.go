package provider

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// data_archives_test.go exercises checksummed snapshots using actual ZIP and files.
// Round trips preserve binary payloads, SQLite companion files, and empty directories.
// Corrupt or hostile uploads must leave the destination unpublished or unchanged.

// TestDataArchivesRoundTripStoppedFiles snapshots binary files and an empty directory.
// Restoring into a fresh store reproduces all bytes, and an already populated destination rejects replacement.
func TestDataArchivesRoundTripStoppedFiles(t *testing.T) {
	source, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	directory, err := source.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	files := map[string][]byte{
		"plugin.sqlite":      {0, 1, 2, 0xff},
		"plugin.sqlite-wal":  []byte("committed WAL records"),
		"images/private.png": []byte("private attachment bytes"),
	}
	for name, content := range files {
		filename := filepath.Join(directory, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
		require.NoError(t, os.WriteFile(filename, content, 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(directory, "empty"), 0o700))
	archive, err := source.Backup(t.Context(), "test.plugin")
	require.NoError(t, err)
	content, err := io.ReadAll(archive.Reader)
	require.NoError(t, err)
	require.Equal(t, archive.Bytes, int64(len(content)))
	require.NoError(t, archive.Reader.Close())
	target, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, target.Restore(t.Context(), "test.plugin", bytes.NewReader(content)))
	for name, expected := range files {
		actual, err := os.ReadFile(filepath.Join(target.root, "test.plugin", filepath.FromSlash(name)))
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	require.DirExists(t, filepath.Join(target.root, "test.plugin", "empty"))
	require.ErrorIs(t, target.Restore(t.Context(), "test.plugin", bytes.NewReader(content)), plugin.ErrDataExists)
	items, err := os.ReadDir(source.root)
	require.NoError(t, err)
	require.Len(t, items, 1)
}

// TestDataArchivesRejectInvalidContent builds archives with traversal, identity, and digest violations.
// Every validation failure keeps the owner's existing empty directory free of partially restored files.
func TestDataArchivesRejectInvalidContent(t *testing.T) {
	content := []byte("business data")
	digest := sha256.Sum256(content)
	valid := dataEntry{Path: "plugin.sqlite", Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	for _, test := range []struct {
		name  string    // failing archive scenario
		owner string    // declared archive owner
		entry dataEntry // untrusted manifest entry
		extra bool      // append undeclared payload
	}{
		{name: "wrong owner", owner: "test.other", entry: valid},
		{name: "traversal", owner: "test.plugin", entry: dataEntry{Path: "../escape", Bytes: valid.Bytes, SHA256: valid.SHA256}},
		{name: "absolute", owner: "test.plugin", entry: dataEntry{Path: "/escape", Bytes: valid.Bytes, SHA256: valid.SHA256}},
		{name: "backslash", owner: "test.plugin", entry: dataEntry{Path: "dir\\escape", Bytes: valid.Bytes, SHA256: valid.SHA256}},
		{name: "checksum", owner: "test.plugin", entry: dataEntry{Path: valid.Path, Bytes: valid.Bytes, SHA256: hex.EncodeToString(make([]byte, sha256.Size))}},
		{name: "size", owner: "test.plugin", entry: dataEntry{Path: valid.Path, Bytes: valid.Bytes + 1, SHA256: valid.SHA256}},
		{name: "expansion", owner: "test.plugin", entry: dataEntry{Path: valid.Path, Bytes: dataMaxBytes + 1, SHA256: valid.SHA256}},
		{name: "extra file", owner: "test.plugin", entry: valid, extra: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, err := NewDataFiles(t.TempDir())
			require.NoError(t, err)
			directory, err := target.Directory(t.Context(), "test.plugin")
			require.NoError(t, err)
			archive := dataTestArchive(t, dataManifest{Format: 1, PluginID: test.owner, Entries: []dataEntry{test.entry}}, map[string][]byte{"data/" + test.entry.Path: content}, test.extra)
			require.Error(t, target.Restore(t.Context(), "test.plugin", bytes.NewReader(archive)))
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
			entries, err = os.ReadDir(target.root)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

// TestDataArchivesRejectLinkedFiles creates a link to a different owner's private file.
// Backup rejects the link and removes its temporary snapshot, leaving the target's original bytes untouched.
func TestDataArchivesRejectLinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links requires platform-specific privileges")
	}
	data, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	directory, err := data.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	other, err := data.Directory(t.Context(), "test.other")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(other, "private"), []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(other, "private"), filepath.Join(directory, "alias")))
	_, err = data.Backup(t.Context(), "test.plugin")
	require.ErrorIs(t, err, plugin.ErrDataInvalid)
	entries, err := os.ReadDir(data.root)
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

// TestDataArchivesRejectDuplicateAndLinkedMembers supplies ambiguous or executable filesystem metadata.
// Restore rejects duplicate member names, symbolic links, and device modes before publishing any destination files.
func TestDataArchivesRejectDuplicateAndLinkedMembers(t *testing.T) {
	content := []byte("payload")
	digest := sha256.Sum256(content)
	metadata := dataManifest{Format: 1, PluginID: "test.plugin", Entries: []dataEntry{{Path: "file", Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}}}
	for _, mode := range []os.FileMode{0, os.ModeSymlink, os.ModeDevice} {
		t.Run(mode.String(), func(t *testing.T) {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			header := &zip.FileHeader{Name: "data/file"}
			header.SetMode(mode | 0o600)
			entry, err := writer.CreateHeader(header)
			require.NoError(t, err)
			_, err = entry.Write(content)
			require.NoError(t, err)
			if mode == 0 {
				_, err := writer.Create("data/file")
				require.NoError(t, err)
			}
			entry, err = writer.Create("manifest.json")
			require.NoError(t, err)
			require.NoError(t, json.NewEncoder(entry).Encode(metadata))
			require.NoError(t, writer.Close())
			target, err := NewDataFiles(t.TempDir())
			require.NoError(t, err)
			require.ErrorIs(t, target.Restore(t.Context(), "test.plugin", bytes.NewReader(buffer.Bytes())), plugin.ErrDataInvalid)
			require.NoDirExists(t, filepath.Join(target.root, "test.plugin"))
		})
	}
}

// TestDataArchivesInterruptedUploadLeavesExistingDirectoryEmpty fails after receiving a partial upload.
// The original read error propagates and private upload staging is removed, allowing a later complete restore to retry safely.
func TestDataArchivesInterruptedUploadLeavesExistingDirectoryEmpty(t *testing.T) {
	target, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	directory, err := target.Directory(t.Context(), "test.plugin")
	require.NoError(t, err)
	reader := io.MultiReader(bytes.NewReader([]byte("partial ZIP")), iotest.ErrReader(io.ErrUnexpectedEOF))
	require.ErrorIs(t, target.Restore(t.Context(), "test.plugin", reader), io.ErrUnexpectedEOF)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	entries, err = os.ReadDir(target.root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestDataArchivesCorruptZIPChecksumReturnsValidationError changes stored payload bytes after archive creation.
// The decoded member fails ZIP integrity checks, returns the stable invalid-archive error, and never publishes a business directory.
func TestDataArchivesCorruptZIPChecksumReturnsValidationError(t *testing.T) {
	content := []byte("unique stopped SQLite payload")
	digest := sha256.Sum256(content)
	metadata := dataManifest{Format: 1, PluginID: "test.plugin", Entries: []dataEntry{{Path: "file", Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}}}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: "data/file", Method: zip.Store})
	require.NoError(t, err)
	_, err = entry.Write(content)
	require.NoError(t, err)
	entry, err = writer.Create("manifest.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(entry).Encode(metadata))
	require.NoError(t, writer.Close())
	archive := buffer.Bytes()
	position := bytes.Index(archive, content)
	require.GreaterOrEqual(t, position, 0)
	archive[position] ^= 0xff
	target, err := NewDataFiles(t.TempDir())
	require.NoError(t, err)
	require.ErrorIs(t, target.Restore(t.Context(), "test.plugin", bytes.NewReader(archive)), plugin.ErrDataInvalid)
	require.NoDirExists(t, filepath.Join(target.root, "test.plugin"))
}

// dataTestArchive builds a synthetic archive with supplied metadata and untrusted names.
// Inputs intentionally bypass production export validation so restore checks encounter
// corrupted content and undeclared entries from an external archive producer.
func dataTestArchive(t *testing.T, metadata dataManifest, files map[string][]byte, extra bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(content)
		require.NoError(t, err)
	}
	if extra {
		_, err := writer.Create("data/undeclared")
		require.NoError(t, err)
	}
	entry, err := writer.Create("manifest.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(entry).Encode(metadata))
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
