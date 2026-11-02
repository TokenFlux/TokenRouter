package plugin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// installation.go describes durable installation intent and complete rollback
// snapshots independently of storage and process implementations. A version
// snapshot includes the exact manifest digest, artifact sources, configuration,
// encrypted secrets, and previously accepted permissions. Swapping only the
// executable version would lose old required fields when an update removes them.
//
// Published snapshots treat nested containers as immutable. Management use cases
// create replacement maps and slices before saving or publishing changes, while
// process and HTTP consumers only read them. Limits and enabled state belong to
// the installation rather than a version, so rollback retains the administrator's
// current spending policy and desired availability.

const (
	// Source labels distinguish recoverable indexed archives, uploaded archives, and memory-only development records without changing plugin identity.
	SourceIndex  Source = "index"  // signed or custom catalog source
	SourceUpload Source = "upload" // administrator-uploaded archive source
	SourceDev    Source = "dev"    // explicit local development source
)

// Source identifies the distribution authority retained for an installed version.
type Source string

// VersionSnapshot preserves the data required to run or restore one accepted release.
type VersionSnapshot struct {
	// IgnoreHostVersion belongs to this accepted snapshot and survives restart and rollback; a newly reviewed release defaults to enforcing bounds.
	IgnoreHostVersion bool
	Version           string // accepted plugin release version
	Source            Source // accepted package distribution source
	// IndexID may become nil after its source index is removed; stored artifact URLs still allow recovery of the accepted package.
	IndexID *int64
	// Manifest, Artifacts, Config, and GrantedPermissions are immutable after publication. Management changes replace these containers so active readers retain one coherent release view.
	Manifest           *manifest.Manifest        // decoded release metadata for compatibility checks
	ManifestJSON       json.RawMessage           `json:"manifest_json,omitempty"` // complete stored release declaration
	ManifestSHA256     string                    // original manifest asset digest
	Artifacts          map[string]index.Artifact // accepted archives keyed by platform
	Config             map[string]any            // normalized nonsecret configuration values
	GrantedPermissions []string                  // administrator-confirmed capability names
	SecretConfig       string                    // encrypted secret configuration object
}

// Installation combines current release state with availability, limits, and history.
type Installation struct {
	VersionSnapshot                  // current accepted release snapshot
	PluginID        string           // publisher-qualified installation identity
	Previous        *VersionSnapshot // optional complete rollback snapshot
	Enabled         bool             // desired runtime availability flag
	Limits          json.RawMessage  `json:"limits,omitempty"` // administrator capability quota configuration
	InstalledAt     time.Time        // first installation creation time
	UpdatedAt       time.Time        // latest persisted installation change
	UpdatedBy       *int64           // administrator responsible for latest change
	// LocalDirectory is populated only by the development source and never written to installation storage or removed by package cleanup.
	LocalDirectory string
}

// IndexSource stores one administrator-selected catalog and its last accepted cache.
type IndexSource struct {
	ID             int64           // database index source identifier
	Name           string          // administrator-facing source name
	URL            string          // configured catalog HTTPS location
	PublicKey      string          // optional base64 verification key
	Builtin        bool            // official index identity flag
	Enabled        bool            // catalog refresh availability flag
	CachedDocument json.RawMessage // last validated catalog JSON
	CachedAt       *time.Time      // last successful refresh timestamp
	// SignatureStatus describes the latest verification observation; CachedDocument
	// can still contain the previous accepted generation after a failed refresh.
	SignatureStatus string
	LastError       string    // latest refresh failure explanation
	CreatedAt       time.Time // source registration creation time
	UpdatedAt       time.Time // latest source or cache update
}

// InstallationStore persists desired plugin state and optional business-data removal.
type InstallationStore interface {
	// List returns independently decoded installation records ordered by plugin ID.
	List(context.Context) ([]Installation, error)
	// Get returns one installation or ErrPluginNotFound without mutating runtime state.
	// The returned containers belong to the caller. Saving modifications requires
	// the manager's plugin mutation lock and a complete replacement installation.
	Get(context.Context, string) (*Installation, error)
	// Save persists a complete installation while retaining its original creation timestamp.
	// Callers serialize management changes for this plugin. Process synchronization
	// occurs after saving and remains the manager's separate responsibility.
	Save(context.Context, *Installation) error
	// Delete removes installation intent and optionally KV in one transaction.
	// Callers must first drain plugin requests and host callbacks. The financial
	// ledger always remains available for replay protection and audit reconstruction.
	Delete(context.Context, string, bool) error
}

// IndexStore persists catalog registrations and their accepted document caches.
type IndexStore interface {
	// ListIndexes returns registered catalog sources in their stable database order.
	ListIndexes(context.Context) ([]IndexSource, error)
	// GetIndex returns one catalog source or an index-not-found application error.
	// Returned metadata belongs to the caller. Cache refresh coordination remains
	// outside storage and must preserve the last accepted document on failure.
	GetIndex(context.Context, int64) (*IndexSource, error)
	// SaveIndex creates or updates one source and returns its persisted identity.
	// A zero identifier requests creation. The caller serializes source changes
	// and retains its previous cache when saving a failed refresh diagnostic.
	SaveIndex(context.Context, *IndexSource) error
	// DeleteIndex removes a custom source while installed artifact records remain usable.
	// Builtin sources cannot be deleted through this method. Missing or builtin
	// identifiers return the same index-not-found error without changing source records.
	DeleteIndex(context.Context, int64) error
}
