package provider

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// export_local_store.go confines temporary and committed usage-export CSV files
// inside a dedicated local directory root using os.Root.
// Background export workers stream rows into a .part staging file so concurrent
// readers never observe truncated CSV output. Commit flushes buffered writes,
// syncs file contents to disk, closes the descriptor, and renames the staging
// file to its final key inside the same confined root handle.
//
// Process crashes or task timeouts can leave uncommitted .part files behind when
// no in-memory defer runs. SweepStalePartFiles walks the confined filesystem tree
// and removes .part artifacts older than the configured grace age while treating
// concurrent worker renames or removals as benign races.

// Buffer sizing and file-mode constants standardize confined export file creation.
const (
	exportWriteBufferBytes = 64 * 1024          // buffered CSV writer capacity in bytes
	exportDirPerm          = fs.FileMode(0o750) // directory permission bits inside export root
	exportFilePerm         = fs.FileMode(0o640) // CSV artifact permission bits inside export root
	partFileSuffix         = ".part"            // temporary staging suffix before atomic commit
)

// LocalExportStore implements usage.UsageExportArtifactStore over an os.Root
// directory handle that blocks path traversal and escaping symlinks.
type LocalExportStore struct {
	dir  string           // cleaned filesystem path of the export root directory
	root *os.Root         // confined OS directory handle
	now  func() time.Time // clock function for deterministic stale-part testing
}

// localExportArtifactWriter buffers CSV bytes into a confined .part file until
// Commit renames it to finalPath or Abort removes the uncommitted staging file.
type localExportArtifactWriter struct {
	root      *os.Root      // owning confined root handle
	partPath  string        // OS-separator relative staging file path
	finalPath string        // OS-separator relative committed file path
	file      *os.File      // open staging file descriptor; nil after close
	buf       *bufio.Writer // buffered writer wrapping file
	committed bool          // whether Commit completed the atomic rename
	aborted   bool          // whether Abort already cleaned up the staging file
}

// NewLocalExportStore creates the export root directory with user/ and admin/
// subdirectories and opens a confined os.Root handle over it. Callers may invoke
// Close during shutdown after background export workers have stopped.
func NewLocalExportStore(dir string) (*LocalExportStore, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return nil, fmt.Errorf("usage export directory is required")
	}
	cleaned := filepath.Clean(trimmed)
	if err := os.MkdirAll(cleaned, exportDirPerm); err != nil {
		return nil, fmt.Errorf("create usage export directory %q: %w", cleaned, err)
	}
	root, err := os.OpenRoot(cleaned)
	if err != nil {
		return nil, fmt.Errorf("open usage export root %q: %w", cleaned, err)
	}
	for _, scopeDir := range []string{usage.UsageExportScopeUser, usage.UsageExportScopeAdmin} {
		if err := root.MkdirAll(scopeDir, exportDirPerm); err != nil {
			_ = root.Close()
			return nil, fmt.Errorf("create usage export scope directory %q: %w", scopeDir, err)
		}
	}
	return &LocalExportStore{
		dir:  cleaned,
		root: root,
		now:  time.Now,
	}, nil
}

// Dir returns the cleaned filesystem path of the confined export root directory.
func (s *LocalExportStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Close releases the underlying os.Root file descriptor.
func (s *LocalExportStore) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

// CreateWriter opens a truncated .part staging file inside the confined root for
// key. Callers must invoke Commit to publish the CSV or Abort to remove it.
func (s *LocalExportStore) CreateWriter(key string) (usage.UsageExportArtifactWriter, error) {
	if s == nil || s.root == nil {
		return nil, fmt.Errorf("local export store is not initialized")
	}
	cleanKey, err := normalizeRelativeArtifactKey(key)
	if err != nil {
		return nil, err
	}
	parent := path.Dir(cleanKey)
	if parent != "." {
		if err := s.root.MkdirAll(filepath.FromSlash(parent), exportDirPerm); err != nil {
			return nil, fmt.Errorf("prepare export parent directory: %w", err)
		}
	}

	finalPath := filepath.FromSlash(cleanKey)
	partPath := filepath.FromSlash(cleanKey + partFileSuffix)
	f, err := s.root.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, exportFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open export staging file: %w", err)
	}
	return &localExportArtifactWriter{
		root:      s.root,
		partPath:  partPath,
		finalPath: finalPath,
		file:      f,
		buf:       bufio.NewWriterSize(f, exportWriteBufferBytes),
	}, nil
}

// Open opens a committed CSV artifact for reading and returns its byte length,
// mapping missing files to usage.ErrExportArtifactNotFound.
func (s *LocalExportStore) Open(key string) (io.ReadCloser, int64, error) {
	if s == nil || s.root == nil {
		return nil, 0, fmt.Errorf("local export store is not initialized")
	}
	cleanKey, err := normalizeRelativeArtifactKey(key)
	if err != nil {
		return nil, 0, err
	}
	f, err := s.root.Open(filepath.FromSlash(cleanKey))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, usage.ErrExportArtifactNotFound
		}
		return nil, 0, fmt.Errorf("open export artifact: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("stat export artifact: %w", err)
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, 0, usage.ErrExportArtifactNotFound
	}
	return f, info.Size(), nil
}

// Delete removes both the committed artifact and any staging .part file for key,
// treating already-absent files as a successful cleanup.
func (s *LocalExportStore) Delete(key string) error {
	if s == nil || s.root == nil {
		return fmt.Errorf("local export store is not initialized")
	}
	cleanKey, err := normalizeRelativeArtifactKey(key)
	if err != nil {
		return err
	}
	for _, rel := range []string{filepath.FromSlash(cleanKey), filepath.FromSlash(cleanKey + partFileSuffix)} {
		if rmErr := s.root.Remove(rel); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("remove export artifact %q: %w", rel, rmErr)
		}
	}
	return nil
}

// SweepStalePartFiles walks the confined export tree and removes uncommitted
// .part files whose modification timestamps are at least maxAge in the past.
func (s *LocalExportStore) SweepStalePartFiles(maxAge time.Duration) (int, error) {
	if s == nil || s.root == nil {
		return 0, fmt.Errorf("local export store is not initialized")
	}
	if maxAge <= 0 {
		return 0, nil
	}
	cutoff := s.currentTime().Add(-maxAge)
	removed := 0

	err := fs.WalkDir(s.root.FS(), ".", func(relPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				// Concurrent worker Commit or Abort calls can remove staging files
				// mid-walk; ignore vanished entries so the sweep completes cleanly.
				return nil
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(relPath, partFileSuffix) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		if rmErr := s.root.Remove(filepath.FromSlash(relPath)); rmErr != nil {
			if errors.Is(rmErr, fs.ErrNotExist) {
				return nil
			}
			return rmErr
		}
		removed++
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("sweep stale export part files: %w", err)
	}
	return removed, nil
}

// currentTime returns the store clock instant, defaulting to time.Now.
func (s *LocalExportStore) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Write appends p into the buffered .part staging stream.
func (w *localExportArtifactWriter) Write(p []byte) (int, error) {
	if w.committed || w.aborted || w.buf == nil {
		return 0, fmt.Errorf("export artifact writer is closed")
	}
	return w.buf.Write(p)
}

// Commit flushes buffered CSV bytes, fsyncs the staging file, closes its handle,
// and atomically renames the .part file to its final key inside the confined root.
func (w *localExportArtifactWriter) Commit() (int64, error) {
	if w.committed {
		return 0, fmt.Errorf("export artifact writer already committed")
	}
	if w.aborted || w.file == nil || w.buf == nil {
		return 0, fmt.Errorf("export artifact writer already aborted")
	}

	if err := w.buf.Flush(); err != nil {
		_ = w.Abort()
		return 0, fmt.Errorf("flush export staging buffer: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		_ = w.Abort()
		return 0, fmt.Errorf("sync export staging file: %w", err)
	}
	info, err := w.file.Stat()
	if err != nil {
		_ = w.Abort()
		return 0, fmt.Errorf("stat export staging file: %w", err)
	}
	sizeBytes := info.Size()
	// Close the staging descriptor before Rename so Windows and POSIX filesystems
	// both permit atomic replacement without open-handle sharing conflicts.
	closeErr := w.file.Close()
	w.file = nil
	if closeErr != nil {
		_ = w.root.Remove(w.partPath)
		return 0, fmt.Errorf("close export staging file: %w", closeErr)
	}
	if err := w.root.Rename(w.partPath, w.finalPath); err != nil {
		_ = w.root.Remove(w.partPath)
		return 0, fmt.Errorf("publish export artifact: %w", err)
	}
	w.committed = true
	return sizeBytes, nil
}

// Abort closes and deletes the uncommitted .part staging file; calling Abort
// after Commit or a previous Abort returns nil without removing published files.
func (w *localExportArtifactWriter) Abort() error {
	if w == nil || w.committed || w.aborted {
		return nil
	}
	w.aborted = true
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
		w.file = nil
	}
	rmErr := w.root.Remove(w.partPath)
	if rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return errors.Join(closeErr, rmErr)
	}
	return closeErr
}

// normalizeRelativeArtifactKey validates that key is a clean, relative slash-separated
// path beneath the export root without traversal segments, backslashes, or .part suffix.
func normalizeRelativeArtifactKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "", fmt.Errorf("invalid export artifact key: empty")
	}
	if strings.ContainsRune(trimmed, '\\') || strings.HasPrefix(trimmed, "/") || strings.ContainsRune(trimmed, ':') {
		return "", fmt.Errorf("invalid export artifact key %q", key)
	}
	for part := range strings.SplitSeq(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid export artifact key %q", key)
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasSuffix(cleaned, partFileSuffix) {
		return "", fmt.Errorf("invalid export artifact key %q", key)
	}
	return cleaned, nil
}
