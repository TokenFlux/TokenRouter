package pkgfmt

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// inspect.go validates unpacked releases and derives integrity metadata for
// installation and browser loading. All referenced assets must be regular
// files within the inspected directory. Manifest digests use original bytes,
// executable digests bind process startup, and SHA-384 asset digests support
// browser subresource integrity without reinterpreting the file contents.
// The inspector first rejects nonregular entries, reserved installer metadata,
// and excessive filesystem content before reading the manifest that identifies
// required contributions.
//
// Release checks compare the original manifest bytes or their supplied digest,
// not a JSON object reconstructed from decoded fields. Referenced files are
// then resolved against the already inspected metadata, including platform-specific
// executable names and bounded icons. This ordering keeps the accepted identity
// and integrity values tied to the same caller-owned directory snapshot.

// Package contains a validated release and the digests needed by its installer.
type Package struct {
	Manifest       *manifest.Manifest // validated release declaration
	ManifestBytes  []byte             // original plugin.json bytes
	ManifestSHA256 string             // original manifest SHA-256
	ArchiveSHA256  string             // compressed package SHA-256
	BinarySHA256   string             // declared executable SHA-256
	Files          map[string]File    // package-relative file metadata
}

// File records a regular file's size and content integrity values.
type File struct {
	Size   int64  // file byte count
	SHA256 string // lowercase SHA-256 digest
	SRI    string // SHA-384 browser integrity value
}

// Platforms returns the supported native build targets in release order.
func Platforms() []string {
	return []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"}
}

// Inspect validates an unpacked directory without executing or changing it.
// The caller keeps the directory stable during inspection. Options must select
// a supported platform, or any for a release without a backend executable.
//
// File metadata is collected before manifest references are resolved, keeping
// existence and size decisions consistent with the hashed content. Expected
// release bytes and digests are checked against the original plugin.json,
// preserving distinctions that JSON reserialization would otherwise erase.
// Inspection never changes modes or executes the declared binary.
func Inspect(directory string, opts Options) (*Package, error) {
	if opts.Platform != "any" && !slices.Contains(Platforms(), opts.Platform) {
		return nil, fmt.Errorf("unsupported package platform %q", opts.Platform)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	result := &Package{Files: make(map[string]File)}
	var count int
	var total, frontend int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		count++
		depth := strings.Count(name, "/")
		if entry.IsDir() {
			depth++
		}
		if !validPackagePath(name) || depth > MaxDepth || count > MaxEntries {
			return fmt.Errorf("invalid or excessive package entries at %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("package entry %q is not a regular file", name)
		}
		if info.Size() < 0 || info.Size() > MaxExpandedBytes-total {
			return fmt.Errorf("package exceeds expanded size at %q", name)
		}
		total += info.Size()
		if strings.HasPrefix(name, "frontend/") {
			frontend += info.Size()
			if frontend > MaxFrontendBytes {
				return fmt.Errorf("frontend exceeds %d bytes", MaxFrontendBytes)
			}
		}
		metadata, err := inspectFile(root, name, info.Size())
		if err != nil {
			return err
		}
		result.Files[name] = metadata
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := result.Files["plugin.json"]; !ok {
		return nil, fmt.Errorf("package is missing plugin.json")
	}
	data, err := root.ReadFile("plugin.json")
	if err != nil {
		return nil, err
	}
	result.ManifestBytes = data
	result.ManifestSHA256 = result.Files["plugin.json"].SHA256
	if opts.ManifestBytes != nil && !bytes.Equal(data, opts.ManifestBytes) {
		return nil, fmt.Errorf("package manifest does not match release manifest bytes")
	}
	if opts.ManifestSHA256 != "" && result.ManifestSHA256 != opts.ManifestSHA256 {
		return nil, fmt.Errorf("package manifest checksum mismatch")
	}
	result.Manifest, err = manifest.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := result.validateReferences(opts.Platform); err != nil {
		return nil, err
	}
	return result, nil
}

// inspectFile hashes one file while enforcing its previously observed size.
// A changed size fails inspection; callers still own the broader obligation
// to prevent concurrent edits while a package is being validated.
func inspectFile(root *os.Root, name string, size int64) (File, error) {
	f, err := root.Open(name)
	if err != nil {
		return File{}, err
	}
	defer func() { _ = f.Close() }()
	hash, integrity := sha256.New(), sha512.New384()
	n, err := io.Copy(io.MultiWriter(hash, integrity), io.LimitReader(f, size+1))
	if err != nil {
		return File{}, err
	}
	if n != size {
		return File{}, fmt.Errorf("package file %q changed during inspection", name)
	}
	return File{Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), SRI: "sha384-" + base64.StdEncoding.EncodeToString(integrity.Sum(nil))}, nil
}

// validateReferences checks required files and icon limits against inspected
// metadata. Only the declared platform binary supplies BinarySHA256; other
// files remain ordinary package data even when their archive mode was executable.
func (p *Package) validateReferences(platform string) error {
	m := p.Manifest
	if m.Backend != nil {
		if platform == "any" {
			return fmt.Errorf("backend packages require a native platform")
		}
		binary := m.Backend.Executable
		if strings.HasPrefix(platform, "windows/") {
			binary += ".exe"
		}
		file, ok := p.Files[binary]
		if !ok || file.Size == 0 {
			return fmt.Errorf("package is missing executable %q", binary)
		}
		p.BinarySHA256 = file.SHA256
	}
	if m.Icon != "" {
		if err := p.requireFile(m.Icon, 64<<10); err != nil {
			return err
		}
	}
	if m.Frontend != nil {
		for _, asset := range append([]string{m.Frontend.Entry}, m.Frontend.Styles...) {
			if err := p.requireFile(asset, MaxFrontendBytes); err != nil {
				return err
			}
		}
		for _, menu := range m.Frontend.Menus {
			if menu.Icon != "" {
				if err := p.requireFile(menu.Icon, 10<<10); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// requireFile verifies a declared asset exists within its byte allowance.
func (p *Package) requireFile(name string, maximum int64) error {
	file, ok := p.Files[name]
	if !ok || file.Size > maximum {
		return fmt.Errorf("missing or oversized declared asset %q", name)
	}
	return nil
}
