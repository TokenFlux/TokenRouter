package provider

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// package_assets.go opens retained package assets without triggering installation
// recovery or network transfers. Managed versions resolve through their publication
// markers; development packages use the manager's already inspected metadata.
// Both paths require a declared regular file within an os.Root boundary and
// validate its current size before transferring descriptor ownership to the caller.
//
// A section reader fixes the readable range at the accepted size, so later file
// growth cannot expand a bounded HTTP response. The open file remains independent
// of the directory handle closed during setup. Integrity metadata remains the
// accepted release identity used for browser SRI and cache validators; it does
// not authorize reading executable or configuration paths from a public route.

// assetReader owns the file descriptor behind a fixed accepted byte range.
type assetReader struct {
	*io.SectionReader          // bounded seekable content view
	file              *os.File // caller-owned open file descriptor
}

// Close releases the file descriptor owned by this bounded asset handle.
func (r *assetReader) Close() error { return r.file.Close() }

// Local reads publication metadata for one retained managed package version.
// Invalid identities and missing markers fail without downloading or rewriting
// anything. The returned paths remain internal inputs to bounded asset opening.
func (p *PackageFiles) Local(ctx context.Context, id, version string) (plugin.Package, error) {
	if !manifest.ValidID(id) || !manifest.ValidVersion(version) {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	if err := ctx.Err(); err != nil {
		return plugin.Package{}, err
	}
	directory := filepath.Join(p.root, "installed", id, version)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	defer func() { _ = root.Close() }()
	marker, err := readPackageMarker(root)
	if err != nil || len(marker.Files) == 0 {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	return marker.local(directory), nil
}

// Open opens a declared regular file inside its package root with a fixed byte bound.
// The caller has selected the public path or a declared menu icon. A successful
// result transfers Reader ownership; all failure paths close acquired resources.
//
// Lstat rejects replaced file or directory links before opening, and os.Root confines
// path resolution even when a parent directory changes. The accepted size must
// match the opened descriptor's current regular-file size. A section reader
// then prevents later growth from increasing the response beyond its verified
// package limit while the caller streams or inspects the already opened file.
func (p *PackageFiles) Open(ctx context.Context, local plugin.Package, name string, maximum int64) (*plugin.AssetFile, error) {
	metadata, exists := local.Files[name]
	if !exists || !manifest.ValidPath(name) || metadata.Size < 0 || metadata.Size > maximum {
		return nil, plugin.ErrPackageMissing
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(local.Directory)
	if err != nil {
		return nil, plugin.ErrPackageMissing
	}
	defer func() { _ = root.Close() }()
	// Check every component because a directory link inside the package can otherwise redirect an accepted asset name to a different retained file.
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, plugin.ErrPackageMissing
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, plugin.ErrPackageMissing
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, plugin.ErrPackageMissing
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, plugin.ErrPackageMissing
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != metadata.Size {
		_ = file.Close()
		return nil, plugin.ErrPackageMissing
	}
	return &plugin.AssetFile{
		Reader:     &assetReader{SectionReader: io.NewSectionReader(file, 0, metadata.Size), file: file},
		Metadata:   metadata,
		ModifiedAt: info.ModTime(),
	}, nil
}

// readPackageMarker reads the bounded metadata created only after package publication succeeds.
// The caller owns and closes root. Marker contents are internal metadata, and
// consumers still validate requested paths and descriptor sizes before exposing files.
func readPackageMarker(root *os.Root) (installedMarker, error) {
	file, err := root.Open(".tr-installed")
	if err != nil {
		return installedMarker{}, err
	}
	defer func() { _ = file.Close() }()
	var marker installedMarker
	// A marker for at most two thousand files fits within one MiB, bounding malformed local metadata before decoding it.
	err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&marker)
	return marker, err
}
