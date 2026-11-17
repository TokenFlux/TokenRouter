package provider

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// data_archives.go snapshots stopped plugin files and validates portable restores.
// Each ZIP binds its plugin ID and relative file names to a versioned manifest
// with byte counts and SHA-256 digests. Private temporary files keep complete
// snapshots independent of later plugin execution and response interruptions.
//
// Restore first receives and validates the complete archive in bounded staging.
// Files are extracted beneath a scoped root, with links, duplicate names, special
// files, unexpected entries, and excessive expansion rejected. Publication renames
// that completed directory into an absent or empty destination, leaving existing
// business files untouched on validation failure. The manager keeps execution
// stopped and excludes reinstall, purge, and enable throughout this operation.

const (
	// Archive limits bound disk consumption and metadata processing for administrator
	// imports and exports, independently of ordinary plugin contribution HTTP limits.
	dataMaxBytes         = plugin.DataMaxArchiveBytes
	dataMaxEntries       = 10000
	dataMaxManifestBytes = 4 << 20
)

// dataManifest binds the exported business files to one plugin identity.
type dataManifest struct {
	Format   int         `json:"format"`    // archive format version
	PluginID string      `json:"plugin_id"` // persistent data owner
	Entries  []dataEntry `json:"entries"`   // ordered files and directories
}

// dataEntry records one portable path and its expected restored content.
type dataEntry struct {
	Path string `json:"path"` // slash-separated relative path
	// Directory, Bytes, and SHA256 describe the same entry. Directories have zero
	// bytes and no digest; regular files declare their decoded size and content hash.
	Directory bool   `json:"directory,omitempty"` // entry represents directory
	Bytes     int64  `json:"bytes"`               // uncompressed regular file bytes
	SHA256    string `json:"sha256,omitempty"`    // hexadecimal file content digest
}

// dataArchiveFile removes a temporary snapshot when its response reader closes.
type dataArchiveFile struct {
	*os.File // owned immutable snapshot file
}

// dataContextReader observes cancellation between bounded archive read operations.
type dataContextReader struct {
	ctx    context.Context // archive operation cancellation
	reader io.Reader       // underlying bounded input
}

// Backup builds a complete checksummed snapshot before exposing its response stream.
// Callers keep the plugin stopped during creation. Snapshot files are private,
// and closing the returned reader releases them after delivery or cancellation.
//
// Files and their content identities are collected under one stopped-owner lock.
// The ZIP central directory is finalized before the result becomes visible, so
// disk failures cannot produce a successful partial download. A later enable can
// resume execution after snapshot creation because subsequent response reads use
// the completed temporary file rather than live plugin business files.
func (d *DataFiles) Backup(ctx context.Context, id string) (*plugin.DataArchive, error) {
	owner, err := d.ownerRoot(ctx, id, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = owner.Close() }()
	file, err := os.CreateTemp(d.root, ".data-snapshot-*.zip")
	if err != nil {
		return nil, err
	}
	archive := &dataArchiveFile{File: file}
	complete := false
	// Until the ZIP is finalized, this call owns temporary-file cleanup; failed
	// cleanup remains under the reserved startup-reclaimed prefix for later removal.
	defer func() {
		if !complete {
			_ = archive.Close()
		}
	}()
	writer := zip.NewWriter(file)
	metadata := dataManifest{Format: 1, PluginID: id, Entries: []dataEntry{}}
	var total int64
	err = fs.WalkDir(owner.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if !validDataPath(name) {
			return plugin.ErrDataInvalid
		}
		if len(metadata.Entries) >= dataMaxEntries {
			return plugin.ErrDataTooLarge
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := dataEntry{Path: name, Directory: info.IsDir()}
		archiveName := "data/" + name
		if item.Directory {
			archiveName += "/"
		} else if !info.Mode().IsRegular() {
			return plugin.ErrDataInvalid
		}
		if info.Size() < 0 || (!item.Directory && info.Size() > dataMaxBytes-total) {
			return plugin.ErrDataTooLarge
		}
		destination, err := writer.Create(archiveName)
		if err != nil {
			return err
		}
		if !item.Directory {
			item.Bytes, item.SHA256, err = writeDataFile(ctx, owner, name, destination, info.Size())
			if err != nil {
				return err
			}
			total += item.Bytes
		}
		metadata.Entries = append(metadata.Entries, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if len(encoded) > dataMaxManifestBytes {
		return nil, plugin.ErrDataTooLarge
	}
	destination, err := writer.Create("manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := destination.Write(encoded); err != nil {
		return nil, err
	}
	// Finish the central directory before returning a snapshot, so write errors
	// cannot turn a partially transferred archive into a successful management response.
	if err := writer.Close(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > dataMaxBytes {
		return nil, plugin.ErrDataTooLarge
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	complete = true
	return &plugin.DataArchive{Reader: archive, Bytes: info.Size()}, nil
}

// writeDataFile copies a bounded stopped file and returns its exact content identity.
// The size observed before opening must match the copied bytes. The caller owns
// the archive writer, while this function closes its scoped source descriptor.
func writeDataFile(ctx context.Context, root *os.Root, name string, destination io.Writer, size int64) (int64, string, error) {
	source, err := root.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = source.Close() }()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(dataContextReader{ctx: ctx, reader: source}, size+1))
	if err != nil {
		return 0, "", err
	}
	if written != size {
		return 0, "", plugin.ErrDataInvalid
	}
	return written, hex.EncodeToString(digest.Sum(nil)), nil
}

// Restore validates an archive and publishes its files into an absent or empty owner.
// The complete upload is spooled before ZIP inspection, and extraction occurs in
// a private directory on the destination filesystem. Existing nonempty data always
// returns a conflict; administrators retain its backup before removing that owner.
//
// The final rename publishes only files whose manifest identity, byte counts,
// checksums, paths, and expansion limits have passed. Validation or disk failures
// discard staging and leave destination contents untouched. Callers serialize
// this operation with plugin execution and installation changes for the same ID.
func (d *DataFiles) Restore(ctx context.Context, id string, source io.Reader) error {
	if _, err := d.destination(ctx, id); err != nil {
		return err
	}
	file, err := os.CreateTemp(d.root, ".data-upload-*.zip")
	if err != nil {
		return err
	}
	defer func() { _ = (&dataArchiveFile{File: file}).Close() }()
	written, err := io.Copy(file, io.LimitReader(dataContextReader{ctx: ctx, reader: source}, dataMaxBytes+1))
	if err != nil {
		return err
	}
	if written > dataMaxBytes {
		return plugin.ErrDataTooLarge
	}
	archive, err := zip.NewReader(file, written)
	if err != nil {
		return plugin.ErrDataInvalid.WithCause(err)
	}
	metadata, entries, err := readDataManifest(archive, id)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(d.root, ".data-restore-")
	if err != nil {
		return err
	}
	// Failed extraction keeps only private staging. Cleanup failures leave its
	// reserved name for startup reclamation while preserving the original restore error.
	defer func() { _ = os.RemoveAll(stage) }()
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	err = extractData(ctx, root, metadata, entries)
	closeErr := root.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	empty, err := d.destination(ctx, id)
	if err != nil {
		return err
	}
	parent, err := os.OpenRoot(d.root)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if empty {
		// Removing an empty directory supports Windows rename behavior; this can
		// lose only an empty placeholder if publication fails, never existing business files.
		if err := parent.Remove(id); err != nil {
			return err
		}
	}
	return parent.Rename(filepath.Base(stage), id)
}

// destination rejects populated or linked owner directories before restore publication.
// Its boolean reports an existing empty directory. Missing destinations are valid,
// and the caller rechecks under the management lock immediately before rename.
func (d *DataFiles) destination(ctx context.Context, id string) (bool, error) {
	owner, err := d.ownerRoot(ctx, id, false)
	if errors.Is(err, plugin.ErrDataNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = owner.Close() }()
	entries, err := fs.ReadDir(owner.FS(), ".")
	if err != nil {
		return false, err
	}
	if len(entries) > 0 {
		return false, plugin.ErrDataExists
	}
	return true, nil
}

// readDataManifest checks owner identity and the archive's complete declared entry set.
// Only a bounded version-one manifest is accepted. Duplicate names, file aliases,
// links, unsupported modes, and undeclared ZIP entries fail before extraction begins.
//
// The manifest must account for every ZIP member exactly once so aliases and
// additional payloads cannot bypass content checks. Expanded byte counts are
// accumulated before file creation, and each regular file must declare a SHA-256
// digest that extraction later compares against its actual decompressed bytes.
func readDataManifest(archive *zip.Reader, id string) (dataManifest, map[string]*zip.File, error) {
	var metadata dataManifest
	if len(archive.File) > dataMaxEntries+1 {
		return metadata, nil, plugin.ErrDataTooLarge
	}
	if len(archive.File) == 0 {
		return metadata, nil, plugin.ErrDataInvalid
	}
	entries := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		if _, duplicate := entries[entry.Name]; duplicate || entry.Mode()&^(os.ModePerm|os.ModeDir) != 0 {
			return metadata, nil, plugin.ErrDataInvalid
		}
		entries[entry.Name] = entry
	}
	manifestFile, ok := entries["manifest.json"]
	if !ok || manifestFile.UncompressedSize64 > dataMaxManifestBytes {
		return metadata, nil, plugin.ErrDataInvalid
	}
	reader, err := manifestFile.Open()
	if err != nil {
		return metadata, nil, plugin.ErrDataInvalid.WithCause(err)
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, dataMaxManifestBytes+1))
	closeErr := reader.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return metadata, nil, plugin.ErrDataInvalid.WithCause(err)
	}
	if len(encoded) > dataMaxManifestBytes ||
		json.Unmarshal(encoded, &metadata) != nil ||
		metadata.Format != 1 || metadata.PluginID != id ||
		len(metadata.Entries) != len(entries)-1 {
		return metadata, nil, plugin.ErrDataInvalid
	}
	seen := make(map[string]bool, len(metadata.Entries))
	var total int64
	for _, item := range metadata.Entries {
		if !validDataPath(item.Path) || seen[item.Path] || item.Bytes < 0 || item.Bytes > dataMaxBytes-total {
			return metadata, nil, plugin.ErrDataInvalid
		}
		seen[item.Path] = true
		name := "data/" + item.Path
		if item.Directory {
			name += "/"
			if item.Bytes != 0 || item.SHA256 != "" {
				return metadata, nil, plugin.ErrDataInvalid
			}
		} else {
			digest, err := hex.DecodeString(item.SHA256)
			if err != nil || len(digest) != sha256.Size {
				return metadata, nil, plugin.ErrDataInvalid
			}
		}
		entry, ok := entries[name]
		if !ok || entry.UncompressedSize64 != uint64(item.Bytes) ||
			entry.FileInfo().IsDir() != item.Directory ||
			(!item.Directory && !entry.Mode().IsRegular()) {
			return metadata, nil, plugin.ErrDataInvalid
		}
		total += item.Bytes
	}
	return metadata, entries, nil
}

// extractData restores verified entries beneath a private root and checks content hashes.
// Callers own publication and discard the entire staging directory on error. File
// bytes are bounded again during decompression, independently of claimed ZIP sizes.
func extractData(ctx context.Context, root *os.Root, metadata dataManifest, entries map[string]*zip.File) error {
	for _, item := range metadata.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Directory {
			if err := root.MkdirAll(item.Path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := root.MkdirAll(path.Dir(item.Path), 0o700); err != nil {
			return err
		}
		if err := extractDataFile(ctx, root, item, entries["data/"+item.Path]); err != nil {
			return err
		}
	}
	return nil
}

// extractDataFile writes one bounded regular file and validates its expected SHA-256.
// Descriptors are closed before returning so publication never includes open staged
// writers. Disk and checksum failures leave cleanup to the enclosing restore operation.
//
// Archive metadata alone cannot establish the decoded content: the copy bounds
// actual bytes and computes their digest while writing. Before publication, the
// byte count and digest are compared with the manifest before flushing and
// closing the destination, so failed writeback or corrupt compressed input cannot expose
// a seemingly successful restored business file.
func extractDataFile(ctx context.Context, root *os.Root, item dataEntry, entry *zip.File) error {
	source, err := entry.Open()
	if err != nil {
		return plugin.ErrDataInvalid.WithCause(err)
	}
	defer func() { _ = source.Close() }()
	destination, err := root.OpenFile(item.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(dataContextReader{ctx: ctx, reader: source}, item.Bytes+1))
	if copyErr == nil && (written != item.Bytes || !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), item.SHA256)) {
		copyErr = plugin.ErrDataInvalid
	}
	var malformed flate.CorruptInputError
	if errors.As(copyErr, &malformed) || errors.Is(copyErr, zip.ErrChecksum) ||
		errors.Is(copyErr, zip.ErrFormat) || errors.Is(copyErr, io.ErrUnexpectedEOF) {
		// Damaged compressed input is a bad archive; preserve write and cancellation
		// errors separately so storage failures are not reported as invalid uploads.
		copyErr = plugin.ErrDataInvalid.WithCause(copyErr)
	}
	var syncErr error
	if copyErr == nil {
		// Flush validated bytes before publication so writeback errors cannot follow
		// a successful restore, while corrupt partial files need no additional writeback.
		syncErr = destination.Sync()
	}
	closeErr := destination.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

// validDataPath accepts bounded relative paths for archive business files.
// Path metadata and directory depth are capped across platforms. Separators and
// colon syntax that could change ownership during extraction are rejected.
func validDataPath(name string) bool {
	return name != "." && len(name) <= 512 && strings.Count(name, "/") < 20 &&
		fs.ValidPath(name) && path.Clean(name) == name &&
		!strings.ContainsAny(name, "\\:\x00")
}

// Close releases the snapshot descriptor and removes its private temporary file.
// Callers invoke it once after delivery or cancellation. Removal is attempted
// even if descriptor closing fails, and both errors remain available to callers.
func (f *dataArchiveFile) Close() error {
	name := f.Name()
	closeErr := f.File.Close()
	return errors.Join(closeErr, os.Remove(name))
}

// Read observes archive cancellation before delegating each bounded input read.
func (r dataContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
