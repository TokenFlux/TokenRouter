package pkgfmt

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// pack.go writes deterministic release archives from validated package trees.
// The packer discards filesystem timestamps, owners, and executable flags,
// retaining only the declared binary's executable mode. Validation precedes
// output creation so publishers cannot accidentally produce an archive that
// the host's shared package inspector would reject for missing assets.
//
// Output is written to a temporary sibling and renamed only after tar and
// gzip finish successfully and the compressed size is accepted. Failed builds
// remove that temporary file and preserve any existing release archive. The
// caller keeps input files stable during packing; publication must use the
// returned checksum together with the resulting archive as one release asset.

// Archive records a completed deterministic package and its publication digest.
type Archive struct {
	Path   string // written archive filesystem path
	SHA256 string // compressed archive SHA-256 digest
	Size   int64  // compressed archive byte count
}

// Filename returns the canonical release asset name for a platform.
// Callers pass validated identifiers, versions, and platform keys. A static
// any artifact uses the same identity prefix without operating-system architecture.
func Filename(id, version, platform string) string {
	return id + "-" + version + "-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz"
}

// Pack validates an unpacked release and publishes one deterministic archive.
// It owns its temporary file and closes every input before publication. The
// caller must keep the input tree stable and choose an output directory outside
// that tree, so a previous archive cannot become part of the next package.
//
// Sorted names and normalized metadata make repeated builds of identical
// contents byte-identical. A failed write never replaces an existing output;
// the final rename occurs only after gzip completion and size validation.
// Returned integrity values describe the compressed bytes actually published.
func Pack(directory, output string, opts Options) (*Archive, error) {
	p, err := Inspect(directory, opts)
	if err != nil {
		return nil, err
	}
	inputAbs, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(inputAbs, outputAbs)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return nil, fmt.Errorf("archive output must be outside its input directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".trplugin-pack-*")
	if err != nil {
		return nil, err
	}
	// The temporary sibling belongs to this invocation; cleanup preserves the previous published archive whenever packing or final rename fails.
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(file, hash))
	tarWriter := tar.NewWriter(gz)
	names := make([]string, 0, len(p.Files))
	for name := range p.Files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		mode := int64(0o644)
		if p.Manifest.Backend != nil {
			binary := p.Manifest.Backend.Executable
			if strings.HasPrefix(opts.Platform, "windows/") {
				binary += ".exe"
			}
			if name == binary {
				mode = 0o755
			}
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Size: p.Files[name].Size, Mode: mode, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if err := copyArchiveFile(root, tarWriter, name, p.Files[name].Size); err != nil {
			return nil, err
		}
	}
	// Finish both framing layers before checking size and publishing, ensuring the archive includes tar end blocks and gzip integrity trailers.
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxArchiveBytes {
		return nil, fmt.Errorf("archive exceeds %d compressed bytes", MaxArchiveBytes)
	}
	if err := file.Chmod(0o644); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(file.Name(), output); err != nil {
		return nil, err
	}
	return &Archive{Path: output, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size()}, nil
}

// copyArchiveFile copies one inspected file and verifies its observed length.
// Close errors propagate before the caller finalizes the archive. Input mutation
// remains prohibited even when it preserves the file's total length.
func copyArchiveFile(root *os.Root, writer io.Writer, name string, size int64) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(writer, io.LimitReader(file, size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != size {
		return fmt.Errorf("input %s changed during packing", name)
	}
	return closeErr
}
