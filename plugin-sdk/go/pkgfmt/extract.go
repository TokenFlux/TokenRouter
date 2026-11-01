package pkgfmt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// extract.go unpacks plugin archives into newly created directories while
// enforcing portable paths and resource limits. No archive entry can replace
// an existing file or introduce links, devices, traversal components, or aliases
// of the host's reserved installation metadata filename.
// File modes from publishers are discarded; extraction grants executable
// mode only to the binary named by a validated manifest for the selected
// platform. The destination is removed when any validation or write fails.
//
// Archive bytes are bounded before decompression, and expanded sizes are
// counted before file creation. Reading through the gzip trailer detects
// truncated or corrupted streams even when tar reached its end marker first.
// The caller owns the successful directory and publishes it only after the
// returned manifest and digests satisfy its own installation identity checks.

const (
	// Archive limits bound local disk and memory use before untrusted plugin contents reach the installer or runtime.
	MaxArchiveBytes  = 64 << 20
	MaxExpandedBytes = 256 << 20
	MaxFrontendBytes = 8 << 20
	MaxEntries       = 2000
	MaxDepth         = 10

	// ReservedMetadataName belongs to the host installer rather than publisher
	// archives, preventing verified package contents from colliding with retained metadata.
	ReservedMetadataName = ".tr-installed"
)

// Options binds package inspection to a platform and optional release manifest.
type Options struct {
	Platform string // target operating-system and architecture
	// ManifestBytes, when supplied, must match plugin.json byte for byte rather than its reserialized JSON representation.
	ManifestBytes []byte
	// ManifestSHA256, when supplied, verifies the original plugin.json bytes against the release index's independently published digest.
	ManifestSHA256 string
}

// Extract validates and unpacks one gzip tar archive into a nonexistent
// destination. The caller chooses its parent directory and owns successful
// contents. Any failure removes only the directory created by this call.
//
// Limits apply to declared and actually copied sizes, and names are checked
// before any filesystem operation. Directory entries and implicit parent
// directories may coexist, but duplicate declared paths are rejected. Hashes
// describe the original archive and manifest bytes, so downstream installers
// can compare them without canonicalizing JSON or trusting tar metadata.
// @project-doc docs/interfaces/plugin_contracts.md#packages
func Extract(reader io.Reader, destination string, opts Options) (result *Package, resultErr error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if len(data) > MaxArchiveBytes {
		return nil, fmt.Errorf("archive exceeds %d bytes", MaxArchiveBytes)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() { _ = compressed.Close() }()
	if err := os.Mkdir(destination, 0o755); err != nil {
		return nil, fmt.Errorf("create extraction directory: %w", err)
	}
	// Failed extraction never becomes an installation; remove partial files only after ownership of this new destination is established.
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Include tar headers and padding in a separate stream budget so metadata-only archives cannot decompress without a finite bound.
	expanded := &io.LimitedReader{R: compressed, N: MaxExpandedBytes + MaxEntries*4096}
	archive := tar.NewReader(expanded)
	seen := make(map[string]bool)
	var total, frontend int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		depth := strings.Count(name, "/")
		if header.Typeflag == tar.TypeDir {
			depth++
		}
		if !validPackagePath(name) || depth > MaxDepth || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate archive path %q", header.Name)
		}
		seen[name] = true
		if len(seen) > MaxEntries {
			return nil, fmt.Errorf("archive exceeds %d entries", MaxEntries)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return nil, fmt.Errorf("archive entry %q must be a regular file or directory", name)
		}
		if header.Size < 0 || header.Size > MaxExpandedBytes-total {
			return nil, fmt.Errorf("archive exceeds expanded size at %q", name)
		}
		total += header.Size
		if strings.HasPrefix(name, "frontend/") {
			frontend += header.Size
			if frontend > MaxFrontendBytes {
				return nil, fmt.Errorf("frontend exceeds %d bytes", MaxFrontendBytes)
			}
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, fmt.Errorf("directory %q has nonzero data size", name)
			}
			if err := root.MkdirAll(name, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := writeEntry(root, name, archive, header.Size); err != nil {
			return nil, err
		}
	}
	// Tar stops before gzip's checksum trailer; consume the remaining bounded stream so a bad trailer invalidates the complete package.
	if _, err := io.Copy(io.Discard, expanded); err != nil {
		return nil, fmt.Errorf("verify gzip trailer: %w", err)
	}
	if expanded.N == 0 {
		return nil, fmt.Errorf("expanded tar stream exceeds limit")
	}
	result, err = Inspect(destination, opts)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	result.ArchiveSHA256 = hex.EncodeToString(digest[:])
	if err := applyModes(root, result, opts.Platform); err != nil {
		return nil, err
	}
	return result, nil
}

// validPackagePath rejects unsafe names and aliases of host-owned root metadata.
// Case-insensitive filesystems and Windows trailing-dot normalization must not turn
// a publisher file or directory into the installer's reserved metadata path.
func validPackagePath(name string) bool {
	if !manifest.ValidPath(name) {
		return false
	}
	first, _, _ := strings.Cut(name, "/")
	return !strings.EqualFold(strings.TrimRight(first, "."), ReservedMetadataName)
}

// applyModes assigns fixed package permissions after all contents are validated.
// This corrects restrictive process umasks as well as publisher archive modes.
// Only directories and the declared platform binary receive executable permission.
func applyModes(root *os.Root, p *Package, platform string) error {
	binary := ""
	if p.Manifest.Backend != nil {
		binary = p.Manifest.Backend.Executable
		if strings.HasPrefix(platform, "windows/") {
			binary += ".exe"
		}
	}
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entry.IsDir() || name == binary {
			mode = 0o755
		}
		return root.Chmod(name, mode)
	})
}

// writeEntry creates a regular file with fixed nonexecutable permissions.
// Existing paths are never replaced. Close failures propagate before the
// caller can accept the package as a complete extraction.
func writeEntry(root *os.Root, name string, reader io.Reader, size int64) error {
	if pos := strings.LastIndexByte(name, '/'); pos > 0 {
		if err := root.MkdirAll(name[:pos], 0o755); err != nil {
			return err
		}
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	_, copyErr := io.CopyN(file, reader, size)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write %s: %w", name, copyErr)
	}
	return closeErr
}
