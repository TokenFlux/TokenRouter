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
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
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

const (
	// Marker capacity covers every permitted entry with ten directory components
	// and one filename, each using 255 ASCII bytes, plus 256 bytes for separators
	// and encoded file metadata.
	// The final 4096 bytes cover release digests and the executable path. Writers
	// independently check actual encoded size before preview or publication succeeds.
	maxInstalledMarkerBytes = pkgfmt.MaxEntries*((pkgfmt.MaxDepth+1)*255+256) + 4096
)

// assetReader owns the file descriptor behind a fixed accepted byte range.
type assetReader struct {
	*io.SectionReader          // bounded seekable content view
	file              *os.File // caller-owned open file descriptor
}

// Close releases the file descriptor owned by this bounded asset handle.
func (r *assetReader) Close() error { return r.file.Close() }

// Local reads publication metadata matching one accepted archive and manifest identity.
// Invalid identities and missing markers fail without downloading or rewriting
// anything. Legacy version paths are accepted only when both recorded digests match.
func (p *PackageFiles) Local(ctx context.Context, ref plugin.PackageRef) (plugin.Package, error) {
	directory, err := p.archiveDirectory(ref)
	if err != nil {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	// Older installations use version paths; checking their markers prevents a
	// same-version replacement from accidentally serving a different retained build.
	for _, candidate := range []string{directory, filepath.Join(p.root, "installed", ref.PluginID, ref.Version)} {
		if err := ctx.Err(); err != nil {
			return plugin.Package{}, err
		}
		root, err := os.OpenRoot(candidate)
		if err != nil {
			continue
		}
		marker, err := readPackageMarker(root)
		_ = root.Close()
		if err == nil && len(marker.Files) > 0 && marker.ArchiveSHA256 == ref.Artifact.SHA256 && marker.ManifestSHA256 == ref.ManifestSHA256 {
			return marker.local(candidate), nil
		}
	}
	return plugin.Package{}, plugin.ErrPackageMissing
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
	file, err := openRegularPackageFile(root, name, metadata.Size)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, plugin.ErrPackageMissing
	}
	return &plugin.AssetFile{
		Reader:     &assetReader{SectionReader: io.NewSectionReader(file, 0, metadata.Size), file: file},
		Metadata:   metadata,
		ModifiedAt: info.ModTime(),
	}, nil
}

// openRegularPackageFile opens a declared file without accepting linked path components.
// The caller validates the package-relative name and owns the returned descriptor.
// The opened regular file must have the accepted size; os.Root confines resolution
// even if a directory changes between component inspection and the final open.
func openRegularPackageFile(root *os.Root, name string, size int64) (*os.File, error) {
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
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		_ = file.Close()
		return nil, plugin.ErrPackageMissing
	}
	return file, nil
}

// readPackageMarker reads the bounded metadata created only after package publication succeeds.
// The caller owns and closes root. Marker contents are internal metadata, and
// consumers still validate requested paths and descriptor sizes before exposing files.
func readPackageMarker(root *os.Root) (installedMarker, error) {
	file, err := root.Open(pkgfmt.ReservedMetadataName)
	if err != nil {
		return installedMarker{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInstalledMarkerBytes {
		return installedMarker{}, plugin.ErrPackageInvalid
	}
	var marker installedMarker
	err = json.NewDecoder(io.LimitReader(file, maxInstalledMarkerBytes)).Decode(&marker)
	return marker, err
}
