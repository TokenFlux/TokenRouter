package provider

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// package_files.go publishes verified archives into managed installation storage.
// Extraction occurs in a fresh staging directory, and the accepted manifest and
// archive digests must match before a directory receives its installation marker.
// A rename publishes the completed directory only after all integrity metadata
// has been written. Digest paths keep distinct builds of the same version
// independently recoverable.
//
// Installed markers avoid repeated archive downloads while complete file digest
// checks detect damaged installations, including changes that preserve file size.
// These checks run during installation and recovery; asset requests retain their
// bounded metadata checks, and browser loads enforce the retained asset SRI values.
// Uploaded archives cannot recover from missing installed files after staging
// expires; their installation records remain available for same-version repair.

// PackageFiles owns installed, disposable state, and temporary archive directories.
type PackageFiles struct {
	root     string           // absolute plugin storage directory
	download *downloader      // bounded HTTPS archive transfer policy
	now      func() time.Time // upload expiration observation clock
}

// installedMarker binds a managed directory to accepted archive and file identities.
type installedMarker struct {
	ArchiveSHA256  string                  `json:"archiveSha256"`  // compressed archive SHA-256 digest
	BinarySHA256   string                  `json:"binarySha256"`   // executable content SHA-256 digest
	ManifestSHA256 string                  `json:"manifestSha256"` // original manifest SHA-256 digest
	Executable     string                  `json:"executable"`     // package-relative platform executable path
	Files          map[string]plugin.Asset `json:"files"`          // verified regular file metadata
}

// NewPackageFiles initializes managed storage before HTTP accepts administrative uploads.
// Staging belongs to this single host instance and is cleared at construction.
// Installed versions and disposable process state survive initialization unchanged.
// The application constructs exactly one store before accepting management traffic,
// so clearing abandoned staging cannot invalidate an active upload. Configured
// paths resolve to one absolute root before directory creation. A failed setup
// returns before any manager can use partially initialized transfer or storage policy.
func NewPackageFiles(root string, options DownloadOptions) (*PackageFiles, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	download, err := newDownloader(options)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"installed", "state", "staging"} {
		if err := os.MkdirAll(filepath.Join(absolute, name), 0o700); err != nil {
			return nil, err
		}
	}
	// Staged tokens belong to one host lifetime, preventing an interrupted upload from becoming an unreviewed installation after restart.
	if err := os.RemoveAll(filepath.Join(absolute, "staging")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "staging", "uploads"), 0o700); err != nil {
		return nil, err
	}
	return &PackageFiles{root: absolute, download: download, now: time.Now}, nil
}

// Ensure obtains verified local files for one accepted release identity.
// The manager serializes calls for each plugin and stops a running generation
// before repairing damaged bytes for the same archive. Uploaded recovery requires a live upload
// token whenever the installed marker or its retained files are missing or damaged.
//
// Download and extraction finish in private staging before the current directory
// for that archive is removed. The manifest digest and declared plugin identity are checked again
// after extraction, so metadata from an earlier preview cannot select unrelated
// code. Failed publication removes the staging directory and leaves installation
// intent untouched for the manager to report or retry.
func (p *PackageFiles) Ensure(ctx context.Context, ref plugin.PackageRef) (plugin.Package, error) {
	if err := ctx.Err(); err != nil {
		return plugin.Package{}, err
	}
	if !manifest.ValidID(ref.PluginID) || !manifest.ValidVersion(ref.Version) {
		return plugin.Package{}, plugin.ErrPackageInvalid
	}
	if ref.LocalDirectory != "" {
		return p.developmentPackage(ctx, ref)
	}
	destination, err := p.archiveDirectory(ref)
	if err != nil {
		return plugin.Package{}, err
	}
	// Reuse the legacy version directory only when its accepted digests match;
	// different same-version builds must leave those rollback bytes untouched.
	for _, directory := range []string{destination, filepath.Join(p.root, "installed", ref.PluginID, ref.Version)} {
		if result, err := p.readInstalled(ctx, directory, ref); err == nil {
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return plugin.Package{}, err
	}
	if ref.Source == plugin.SourceUpload && ref.UploadToken == "" {
		return plugin.Package{}, plugin.ErrPackageMissing
	}
	stage, err := os.MkdirTemp(filepath.Join(p.root, "staging"), "install-")
	if err != nil {
		return plugin.Package{}, err
	}
	// This owner removes only its staging directory; a successfully renamed package is already outside that cleanup subtree.
	defer func() { _ = os.RemoveAll(stage) }()
	archive := filepath.Join(stage, "package.tar.gz")
	if err := p.archive(ctx, ref, archive); err != nil {
		return plugin.Package{}, err
	}
	file, err := os.Open(archive)
	if err != nil {
		return plugin.Package{}, err
	}
	defer func() { _ = file.Close() }()
	extracted := filepath.Join(stage, "package")
	metadata, err := pkgfmt.Extract(file, extracted, pkgfmt.Options{Platform: ref.Platform, ManifestSHA256: ref.ManifestSHA256})
	if err != nil {
		return plugin.Package{}, plugin.ErrPackageInvalid.WithCause(err)
	}
	if metadata.ArchiveSHA256 != ref.Artifact.SHA256 {
		return plugin.Package{}, plugin.ErrChecksumMismatch
	}
	if metadata.Manifest.ID != ref.PluginID || metadata.Manifest.Version != ref.Version {
		return plugin.Package{}, plugin.ErrPackageInvalid
	}
	marker := markerFromPackage(metadata, ref.Platform)
	data, err := marshalPackageMarker(marker)
	if err != nil {
		return plugin.Package{}, err
	}
	if err := os.WriteFile(filepath.Join(extracted, pkgfmt.ReservedMetadataName), data, 0o600); err != nil {
		return plugin.Package{}, err
	}
	if err := ctx.Err(); err != nil {
		return plugin.Package{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return plugin.Package{}, err
	}
	// Only a damaged copy of this exact archive can occupy its digest path.
	// Callers drain that generation for repair; other accepted archive directories remain untouched.
	if err := os.RemoveAll(destination); err != nil {
		return plugin.Package{}, err
	}
	if err := os.Rename(extracted, destination); err != nil {
		return plugin.Package{}, err
	}
	return marker.local(destination), nil
}

// readInstalled validates marker identity and every retained file's complete digest.
// Callers use this during installation or recovery, not asset delivery. An edit
// that preserves file size must fail so uploaded repair can replace damaged contents
// before executable verification or browser integrity checks reject them again.
func (p *PackageFiles) readInstalled(ctx context.Context, directory string, ref plugin.PackageRef) (plugin.Package, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return plugin.Package{}, err
	}
	defer func() { _ = root.Close() }()
	marker, err := readPackageMarker(root)
	if err != nil {
		return plugin.Package{}, err
	}
	if marker.ArchiveSHA256 != ref.Artifact.SHA256 || marker.ManifestSHA256 != ref.ManifestSHA256 || len(marker.Files) == 0 {
		return plugin.Package{}, plugin.ErrChecksumMismatch
	}
	for name, asset := range marker.Files {
		if !manifest.ValidPath(name) {
			return plugin.Package{}, plugin.ErrPackageInvalid
		}
		if err := verifyInstalledFile(ctx, root, name, asset); err != nil {
			return plugin.Package{}, err
		}
	}
	if marker.Executable != "" && (!manifest.ValidPath(marker.Executable) || marker.Files[marker.Executable].SHA256 != marker.BinarySHA256) {
		return plugin.Package{}, plugin.ErrPackageInvalid
	}
	return marker.local(directory), nil
}

// verifyInstalledFile compares a bounded file read with both accepted content digests.
// The caller owns the package root and serializes management mutations. Cancellation
// interrupts hashing, and a changed length or digest prevents reuse of this package.
func verifyInstalledFile(ctx context.Context, root *os.Root, name string, asset plugin.Asset) error {
	if asset.Size < 0 || asset.Size > pkgfmt.MaxExpandedBytes {
		return plugin.ErrPackageInvalid
	}
	file, err := openRegularPackageFile(root, name, asset.Size)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash, integrity := sha256.New(), sha512.New384()
	// Read one extra byte to reject a file that grows after inspection without
	// letting a damaged package force an unbounded management-time integrity read.
	reader := io.LimitReader(&contextReader{ctx: ctx, reader: file}, asset.Size+1)
	count, err := io.Copy(io.MultiWriter(hash, integrity), reader)
	if err != nil {
		return err
	}
	if count != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 ||
		"sha384-"+base64.StdEncoding.EncodeToString(integrity.Sum(nil)) != asset.SRI {
		return plugin.ErrChecksumMismatch
	}
	return nil
}

// marshalPackageMarker checks the reader's byte budget before metadata is accepted.
// Preview and publication share this encoder. A package can never be reported as
// successfully staged or installed with metadata that the retained reader truncates.
func marshalPackageMarker(marker installedMarker) ([]byte, error) {
	data, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	if len(data) > maxInstalledMarkerBytes {
		return nil, plugin.ErrPackageInvalid.WithCause(fmt.Errorf("installed metadata exceeds its byte limit"))
	}
	return data, nil
}

// markerFromPackage retains verified file digests and the selected executable name.
func markerFromPackage(metadata *pkgfmt.Package, platform string) installedMarker {
	marker := installedMarker{
		ArchiveSHA256:  metadata.ArchiveSHA256,
		BinarySHA256:   metadata.BinarySHA256,
		ManifestSHA256: metadata.ManifestSHA256,
		Files:          make(map[string]plugin.Asset, len(metadata.Files)),
	}
	for name, file := range metadata.Files {
		marker.Files[name] = plugin.Asset{Size: file.Size, SHA256: file.SHA256, SRI: file.SRI}
	}
	if metadata.Manifest.Backend != nil {
		marker.Executable = metadata.Manifest.Backend.Executable
		if platform == "windows/amd64" {
			marker.Executable += ".exe"
		}
	}
	return marker
}

// local resolves verified package-relative metadata against its published directory.
func (m installedMarker) local(directory string) plugin.Package {
	result := plugin.Package{Directory: directory, ArchiveSHA256: m.ArchiveSHA256, BinarySHA256: m.BinarySHA256, ManifestSHA256: m.ManifestSHA256, Files: m.Files}
	if m.Executable != "" {
		result.Executable = filepath.Join(directory, filepath.FromSlash(m.Executable))
	}
	return result
}

// developmentPackage inspects trusted development files without modifying their directory.
// The ordinary manager consumes the resulting metadata. Execution checksum bypass
// is controlled by the development installation source rather than this inspector.
func (p *PackageFiles) developmentPackage(ctx context.Context, ref plugin.PackageRef) (plugin.Package, error) {
	metadata, err := pkgfmt.Inspect(ref.LocalDirectory, pkgfmt.Options{Platform: ref.Platform})
	if err != nil {
		return plugin.Package{}, plugin.ErrPackageInvalid.WithCause(err)
	}
	if metadata.Manifest.ID != ref.PluginID || metadata.Manifest.Version != ref.Version {
		return plugin.Package{}, plugin.ErrPackageInvalid.WithCause(fmt.Errorf("development declaration changed; restart the plugin to reload it"))
	}
	if err := ctx.Err(); err != nil {
		return plugin.Package{}, err
	}
	absolute, err := filepath.Abs(ref.LocalDirectory)
	if err != nil {
		return plugin.Package{}, err
	}
	return markerFromPackage(metadata, ref.Platform).local(absolute), nil
}

// archiveDirectory maps an accepted archive to a stable, version-independent managed path.
// The hexadecimal digest is validated before path construction. Legacy version
// paths are read separately so existing installations need no eager file migration.
func (p *PackageFiles) archiveDirectory(ref plugin.PackageRef) (string, error) {
	digest, err := hex.DecodeString(ref.Artifact.SHA256)
	if !manifest.ValidID(ref.PluginID) || !manifest.ValidVersion(ref.Version) || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != ref.Artifact.SHA256 {
		return "", plugin.ErrPackageInvalid
	}
	return filepath.Join(p.root, "installed", ref.PluginID, "sha256-"+ref.Artifact.SHA256), nil
}

// Prune removes archive directories outside the current and rollback identities.
// Callers hold the plugin mutation lock and have drained displaced generations.
// Missing installation roots succeed, while invalid retention references fail before deletion.
//
// Legacy version directories remain readable but are retained only when their
// marker matches an accepted archive. Digest directories can therefore coexist
// with legacy files during an update without preserving every historical build
// of one version. Update callers temporarily include the original previous archive
// until readiness or compensation has completed, protecting the recovery target.
func (p *PackageFiles) Prune(ctx context.Context, id string, keep ...plugin.PackageRef) error {
	if !manifest.ValidID(id) {
		return plugin.ErrPackageInvalid
	}
	retained := make(map[string]bool)
	for _, ref := range keep {
		if ref.LocalDirectory != "" {
			continue
		}
		directory, err := p.archiveDirectory(ref)
		if err != nil || ref.PluginID != id {
			return plugin.ErrPackageInvalid
		}
		retained[filepath.Base(directory)] = true
		legacy := filepath.Join(p.root, "installed", id, ref.Version)
		// An old version path is retained only for the same accepted archive;
		// a later build with that version cannot keep unrelated historical files alive.
		if local, err := p.Local(ctx, ref); err == nil && local.Directory == legacy {
			retained[ref.Version] = true
		}
	}
	directory := filepath.Join(p.root, "installed", id)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !retained[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Remove deletes this plugin's managed packages and disposable process state.
// The caller has already drained all executable and host callback work. Files
// in explicitly configured development directories are outside these owned paths.
func (p *PackageFiles) Remove(ctx context.Context, id string) error {
	if !manifest.ValidID(id) {
		return plugin.ErrPackageInvalid
	}
	for _, name := range []string{"installed", "state"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(p.root, name, id)); err != nil {
			return err
		}
	}
	return nil
}

// packagePlatform selects the native target used for upload preview inspection.
func packagePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
