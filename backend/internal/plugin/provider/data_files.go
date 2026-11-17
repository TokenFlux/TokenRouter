package provider

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// data_files.go creates and measures persistent directories owned by plugin IDs.
// These directories live separately from installed packages and disposable state,
// so package cleanup leaves business files available to a later installation.
// The manager serializes creation, restore, and purge for each owner.
//
// Filesystem operations use scoped root handles. Owner directories cannot be
// symbolic links, and usage measurements reject links and special files instead
// of traversing another owner's storage. This limits host management operations;
// administrator-trusted plugins still run with the host operating-system user.
// Temporary snapshot and restore names are reserved outside valid plugin IDs and
// reclaimed during startup before management requests can create active archives.

// DataFiles owns persistent local directories independently of package installation.
type DataFiles struct {
	root string // absolute persistent storage root
}

// NewDataFiles prepares durable storage and removes interrupted archive staging.
// Construct one instance before admitting plugin administration. The root belongs
// to the host, while valid plugin-ID children survive initialization and uninstall.
func NewDataFiles(directory string) (*DataFiles, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".data-") {
			// Only host-reserved names are discarded; valid owner directories keep
			// business files even when no installation record exists at this startup.
			if err := root.RemoveAll(entry.Name()); err != nil {
				return nil, err
			}
		}
	}
	return &DataFiles{root: absolute}, nil
}

// Directory creates a private persistent directory for one validated plugin ID.
// Callers hold the owner's management lock through process initialization. Existing
// links and nondirectories are rejected before an executable receives the path.
func (d *DataFiles) Directory(ctx context.Context, id string) (string, error) {
	root, err := d.ownerRoot(ctx, id, true)
	if err != nil {
		return "", err
	}
	if err := root.Close(); err != nil {
		return "", err
	}
	return filepath.Join(d.root, id), nil
}

// List measures retained owners while keeping temporary archive staging private.
// Directory measurements may change during running plugin writes. A malformed
// owner fails the request rather than reporting a misleading complete total.
func (d *DataFiles) List(ctx context.Context) ([]plugin.DataUsage, error) {
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	items := make([]plugin.DataUsage, 0, len(entries))
	for _, entry := range entries {
		if !manifest.ValidID(entry.Name()) {
			continue
		}
		owner, err := d.ownerRoot(ctx, entry.Name(), false)
		if err != nil {
			return nil, err
		}
		usage := plugin.DataUsage{PluginID: entry.Name()}
		err = fs.WalkDir(owner.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return plugin.ErrDataInvalid
			}
			usage.Bytes += info.Size()
			usage.Files++
			return nil
		})
		closeErr := owner.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return nil, err
		}
		items = append(items, usage)
	}
	return items, nil
}

// Remove deletes only the validated owner's directory and succeeds when absent.
// Callers drain executable and host-callback work first. A symlink at the owner
// name is removed as a link; its target and other owners remain untouched.
func (d *DataFiles) Remove(ctx context.Context, id string) error {
	if !manifest.ValidID(id) {
		return plugin.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(id)
}

// ownerRoot opens a validated owner directory, optionally creating an absent one.
// The returned handle belongs to the caller. Links and nondirectories fail, and
// missing data is reported with the stable management error for archive operations.
func (d *DataFiles) ownerRoot(ctx context.Context, id string, create bool) (*os.Root, error) {
	if !manifest.ValidID(id) {
		return nil, plugin.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(id)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := root.Mkdir(id, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = root.Lstat(id)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, plugin.ErrDataNotFound
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, plugin.ErrDataInvalid
	}
	return root.OpenRoot(id)
}
