package plugin

import (
	"context"
	"io"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// packages.go defines the verified local assets consumed by process and HTTP
// adapters. Package storage owns download, extraction, integrity markers, and
// removal; installation management supplies accepted release identities and
// serializes operations for the same plugin before replacing local directories.

// PackageRef identifies one accepted archive and its intended local installation.
type PackageRef struct {
	PluginID       string         // publisher-qualified plugin identity
	Version        string         // accepted release SemVer
	Icon           string         // accepted declared public icon
	ManifestSHA256 string         // original manifest asset digest
	Artifact       index.Artifact // selected platform archive identity
	Platform       string         // archive operating-system and architecture
	Source         Source         // recorded package distribution source
	// UploadToken is transient and never persisted in an installation; repair and first install supply it while the staged archive exists.
	UploadToken string
	// LocalDirectory points at an explicitly configured development directory; package cleanup must never remove its publisher-owned contents.
	LocalDirectory string
}

// Package holds the verified locations and asset metadata for one release.
type Package struct {
	Directory      string           // absolute installed package directory
	Executable     string           // absolute platform executable path
	ArchiveSHA256  string           // accepted compressed archive digest
	BinarySHA256   string           // accepted executable SHA-256 digest
	ManifestSHA256 string           // accepted manifest asset digest
	Files          map[string]Asset // immutable package-relative asset metadata
}

// Asset records the size and browser integrity identity of one regular file.
type Asset struct {
	Size   int64  `json:"size"`   // file byte count
	SHA256 string `json:"sha256"` // lowercase SHA-256 digest
	SRI    string `json:"sri"`    // SHA-384 browser integrity attribute
}

// AssetFile owns a bounded readable file and its accepted package metadata.
type AssetFile struct {
	// Reader belongs to the caller after a successful open and must be closed on every response or inspection path.
	Reader     io.ReadSeekCloser
	Metadata   Asset     // accepted file integrity metadata
	ModifiedAt time.Time // filesystem modification time observation
}

// AssetFiles resolves retained packages and opens bounded files without downloading anything.
type AssetFiles interface {
	// Local returns the accepted archive marker without recovery downloads.
	// Missing local versions fail. Development callers already hold their inspected
	// Package and pass it directly to Open instead of using managed lookup.
	Local(context.Context, PackageRef) (Package, error)
	// Open returns one declared regular file bounded by its accepted size.
	// The caller selects an allowed asset path and byte ceiling, then closes the
	// returned Reader. Undeclared, missing, linked, or oversized files fail before serving.
	Open(context.Context, Package, string, int64) (*AssetFile, error)
}

// Upload describes a validated archive available for a bounded installation window.
type Upload struct {
	Token     string          // opaque staged archive identifier
	ExpiresAt time.Time       // staged archive expiration timestamp
	Snapshot  VersionSnapshot // validated release and artifact metadata
	Platform  string          // validated archive target platform
}

// Packages owns verified installation files independently of installation intent.
type Packages interface {
	// Ensure returns accepted local files or validates and publishes their replacement.
	// Callers serialize operations for this plugin and stop a running generation
	// before repairing damaged files for an existing archive. Different archives
	// use separate directories even when their version strings are identical.
	Ensure(context.Context, PackageRef) (Package, error)
	// Prune removes obsolete archives while preserving every supplied package identity.
	// Callers retain current and previous versions. Development directories remain
	// outside managed storage and are never candidates for this cleanup.
	Prune(context.Context, string, ...PackageRef) error
	// Remove deletes managed installation and state directories after process draining.
	// Missing directories succeed for retryability. The caller must finish admitted
	// host callbacks before invoking this operation or deleting business state.
	Remove(context.Context, string) error
}

// Uploads validates administrator archives and resolves their temporary installation handles.
type Uploads interface {
	// Preview stages and validates a bounded archive without executing plugin code.
	// The returned token expires after thirty minutes. Failed validation removes
	// the newly created staging directory before returning an error.
	Preview(context.Context, io.Reader) (*Upload, error)
	// ResolveUpload retrieves a still-valid upload or a package-invalid error.
	// Resolution does not consume the token; a failed installation may be retried
	// within its original expiration window without another upload.
	ResolveUpload(context.Context, string) (*Upload, error)
}
