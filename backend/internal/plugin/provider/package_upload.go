package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// package_upload.go stages administrator archives and returns expiring review
// handles. Preview validates package contents without executing any plugin code,
// retaining the compressed archive and its accepted metadata for installation.
// Each token names a private directory and carries a fixed thirty-minute expiry.
//
// The preview record is written only after extraction and integrity inspection
// succeed. Failed previews remove all owned files, while successful previews
// discard their temporary extracted copy to avoid retaining two complete package
// trees. Installation copies the archive into separate staging before publication;
// expired upload cleanup therefore cannot remove an installed version directory.

// Preview validates and stages one administrator-supplied plugin archive.
// Archive size and decompressed content limits are enforced before returning a
// token. The token authorizes only the inspected identity and expires without
// being extended by lookup or a failed installation attempt.
//
// A fresh random directory owns every file written by this operation. Failure
// removes that directory, including partially copied uploads. Successful records
// preserve original manifest bytes and compressed archive identity, allowing the
// service to confirm permissions before any later process launch. Static archives
// are labeled any-platform after their declaration has passed package validation.
func (p *PackageFiles) Preview(ctx context.Context, reader io.Reader) (result *plugin.Upload, resultErr error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce[:])
	directory := filepath.Join(p.root, "staging", "uploads", token)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, err
	}
	// Only a completed preview preserves its staging token; failures remove partially copied data and prevent later resolution of an unvalidated archive.
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	file, err := os.OpenFile(filepath.Join(directory, "package.tar.gz"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	size, err := io.Copy(file, io.LimitReader(&contextReader{ctx: ctx, reader: reader}, pkgfmt.MaxArchiveBytes+1))
	if err != nil || size > pkgfmt.MaxArchiveBytes {
		return nil, plugin.ErrPackageInvalid.WithCause(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	extracted := filepath.Join(directory, "preview")
	metadata, err := pkgfmt.Extract(file, extracted, pkgfmt.Options{Platform: packagePlatform()})
	if err != nil {
		return nil, plugin.ErrPackageInvalid.WithCause(err)
	}
	if _, err := marshalPackageMarker(markerFromPackage(metadata, packagePlatform())); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(extracted); err != nil {
		return nil, err
	}
	platform := packagePlatform()
	if metadata.Manifest.Backend == nil {
		platform = "any"
	}
	result = &plugin.Upload{Token: token, ExpiresAt: p.now().Add(30 * time.Minute), Platform: platform, Snapshot: plugin.VersionSnapshot{
		Version: metadata.Manifest.Version, Source: plugin.SourceUpload, Manifest: metadata.Manifest, ManifestJSON: metadata.ManifestBytes,
		ManifestSHA256: metadata.ManifestSHA256, Artifacts: map[string]index.Artifact{platform: {SHA256: metadata.ArchiveSHA256, Size: size}},
	}}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "preview.json"), data, 0o600); err != nil {
		return nil, err
	}
	return result, ctx.Err()
}

// ResolveUpload retrieves a validated preview within its original expiration window.
// Tokens must be lowercase hexadecimal directory identifiers. Missing, expired,
// or malformed records return a package-invalid error without exposing local paths.
func (p *PackageFiles) ResolveUpload(ctx context.Context, token string) (*plugin.Upload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != token {
		return nil, plugin.ErrPackageInvalid
	}
	file, err := os.Open(filepath.Join(p.root, "staging", "uploads", token, "preview.json"))
	if err != nil {
		return nil, plugin.ErrPackageInvalid.WithCause(err)
	}
	defer func() { _ = file.Close() }()
	var preview plugin.Upload
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&preview); err != nil {
		return nil, plugin.ErrPackageInvalid.WithCause(err)
	}
	if preview.Token != token || !p.now().Before(preview.ExpiresAt) {
		return nil, plugin.ErrPackageInvalid
	}
	return &preview, nil
}

// CleanupUploads removes expired previews without touching published installation directories.
// Missing or unreadable preview records are removed only after their directory
// is older than the upload window, preserving uploads still being copied.
func (p *PackageFiles) CleanupUploads(ctx context.Context) error {
	root := filepath.Join(p.root, "staging", "uploads")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if p.now().Sub(info.ModTime()) >= 30*time.Minute {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
