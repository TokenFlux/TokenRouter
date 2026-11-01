package index

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// index.go defines signed release catalogs shared by publishers and hosts.
// Catalog signatures authenticate the original document bytes independently
// of JSON parsing. Release entries carry the digest of the original manifest
// asset, which may differ in whitespace from its embedded catalog object;
// installers compare that digest with plugin.json inside the downloaded package.

const (
	// MaxDocumentBytes bounds original catalog downloads and signing-tool output,
	// including JSON indentation and the final newline. Stored JSON may be reformatted.
	MaxDocumentBytes = 8 << 20
)

// Document describes one signed catalog and its registered plugin releases.
type Document struct {
	SchemaVersion int       `json:"schemaVersion"` // catalog schema major
	Name          string    `json:"name"`          // catalog display name
	GeneratedAt   time.Time `json:"generatedAt"`   // catalog generation timestamp
	Plugins       []Plugin  `json:"plugins"`       // registered plugin release sets
}

// Plugin groups one registered repository's versions and catalog presentation.
type Plugin struct {
	ID         string    `json:"id"`                // publisher-qualified plugin identity
	Repository string    `json:"repository"`        // source repository HTTPS URL
	Categories []string  `json:"categories"`        // catalog discovery categories
	IconURL    string    `json:"iconUrl,omitempty"` // catalog-relative display icon URL
	Delisted   bool      `json:"delisted"`          // catalog listing withdrawal flag
	Versions   []Version `json:"versions"`          // releases in descending SemVer
}

// Version binds release metadata to a manifest and platform-specific archives.
type Version struct {
	Version     string             `json:"version"`              // unprefixed release SemVer
	PublishedAt time.Time          `json:"publishedAt"`          // release publication timestamp
	Prerelease  bool               `json:"prerelease"`           // publisher prerelease designation
	Yanked      bool               `json:"yanked"`               // release installation withdrawal flag
	YankReason  string             `json:"yankReason,omitempty"` // release withdrawal explanation
	Changelog   string             `json:"changelog"`            // bounded release notes Markdown
	Manifest    *manifest.Manifest `json:"manifest"`             // embedded release declaration
	// ManifestJSON preserves the embedded declaration for host persistence, including
	// fields from a future release that this host cannot yet validate or execute.
	ManifestJSON json.RawMessage `json:"-"`
	// ManifestSHA256 hashes the original release asset, not this embedded object's JSON serialization after catalog parsing or storage.
	ManifestSHA256 string              `json:"manifestSha256"`
	Artifacts      map[string]Artifact `json:"artifacts"` // archives keyed by platform
}

// Artifact identifies a downloadable archive by location, digest, and size.
type Artifact struct {
	URL    string `json:"url"`    // archive download HTTPS URL
	SHA256 string `json:"sha256"` // lowercase archive SHA-256
	Size   int64  `json:"size"`   // compressed archive byte count
}

// Parse decodes one catalog and validates all nested release declarations.
// Signature verification remains a separate step because callers must verify
// the exact downloaded bytes before parsing or accepting a replacement cache.
// @project-doc docs/interfaces/plugin_contracts.md#catalog
func Parse(data []byte) (*Document, error) {
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("index must contain one JSON object")
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Validate checks catalog metadata, release ordering, and artifact identities.
// It does not fetch URLs or verify archive bytes. Hosts and index builders
// perform those operations with their own bounded download and package policies.
func (d *Document) Validate() error {
	if err := d.validateMetadata(); err != nil {
		return err
	}
	for _, plugin := range d.Plugins {
		for _, version := range plugin.Versions {
			if len(version.ManifestJSON) > 0 {
				if _, err := manifest.Parse(version.ManifestJSON); err != nil {
					return fmt.Errorf("manifest for %s@%s: %w", plugin.ID, version.Version, err)
				}
			}
			if err := version.Manifest.Validate(); err != nil {
				return fmt.Errorf("manifest for %s@%s: %w", plugin.ID, version.Version, err)
			}
		}
	}
	return nil
}

// validateMetadata checks catalog structure independently of contribution schema evolution.
// Every version still needs valid identity, timestamps, hashes, URLs, and supported
// archive metadata. Host-aware decoding owns the separate decision to defer future manifest validation.
func (d *Document) validateMetadata() error {
	if d == nil || d.SchemaVersion != 1 || strings.TrimSpace(d.Name) == "" || d.GeneratedAt.IsZero() {
		return fmt.Errorf("index requires schemaVersion 1, a name, and generatedAt")
	}
	seen := make(map[string]bool)
	for _, p := range d.Plugins {
		if !manifest.ValidID(p.ID) || seen[p.ID] {
			return fmt.Errorf("invalid or duplicate plugin ID %q", p.ID)
		}
		seen[p.ID] = true
		if !HTTPSURL(p.Repository) {
			return fmt.Errorf("plugin %s requires an HTTPS repository URL", p.ID)
		}
		if p.IconURL != "" && !HTTPSURL(p.IconURL) && !manifest.ValidPath(p.IconURL) {
			return fmt.Errorf("plugin %s has invalid icon URL", p.ID)
		}
		for _, category := range p.Categories {
			if !slices.Contains([]string{"engagement", "tools", "analytics", "content", "integration", "other"}, category) {
				return fmt.Errorf("plugin %s has unknown category %q", p.ID, category)
			}
		}
		if len(p.Versions) > 30 {
			return fmt.Errorf("plugin %s exceeds 30 retained versions", p.ID)
		}
		versions := make(map[string]bool)
		for i, v := range p.Versions {
			if versions[v.Version] {
				return fmt.Errorf("duplicate version %s for %s", v.Version, p.ID)
			}
			versions[v.Version] = true
			if i > 0 && semver.Compare("v"+p.Versions[i-1].Version, "v"+v.Version) < 0 {
				return fmt.Errorf("versions for %s must be in descending SemVer order", p.ID)
			}
			if err := v.validateMetadata(p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateMetadata checks release identity and archives without validating contribution fields.
// Backend releases require native artifacts. The official index builder adds
// its stricter requirement for both Linux architectures before publishing entries.
func (v Version) validateMetadata(id string) error {
	if !manifest.ValidVersion(v.Version) || v.PublishedAt.IsZero() || len(v.Changelog) > 4096 || !ValidSHA256(v.ManifestSHA256) {
		return fmt.Errorf("invalid release metadata for %s@%s", id, v.Version)
	}
	if v.Manifest == nil || v.Manifest.ID != id || v.Manifest.Version != v.Version {
		return fmt.Errorf("manifest identity differs from %s@%s", id, v.Version)
	}
	if len(v.Artifacts) == 0 {
		return fmt.Errorf("release %s@%s has no artifacts", id, v.Version)
	}
	for platform, artifact := range v.Artifacts {
		if (platform != "any" && !slices.Contains(pkgfmt.Platforms(), platform)) || (platform == "any" && v.Manifest.Backend != nil) {
			return fmt.Errorf("invalid artifact platform %q for %s", platform, id)
		}
		if !HTTPSURL(artifact.URL) || !ValidSHA256(artifact.SHA256) || artifact.Size <= 0 || artifact.Size > pkgfmt.MaxArchiveBytes {
			return fmt.Errorf("invalid artifact metadata for %s@%s/%s", id, v.Version, platform)
		}
	}
	return nil
}

// HTTPSURL accepts absolute HTTPS URLs without embedded credentials or fragments.
func HTTPSURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// ValidSHA256 reports whether a string contains a lowercase SHA-256 hex digest.
func ValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

// Verify authenticates raw catalog bytes with any supplied Ed25519 public key.
// The signature is a single base64 value with optional surrounding whitespace.
// Empty key lists, malformed keys, and altered document bytes always fail.
func Verify(data, signature []byte, keys []ed25519.PublicKey) error {
	sig, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("invalid base64 Ed25519 signature")
	}
	for _, key := range keys {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, data, sig) {
			return nil
		}
	}
	return fmt.Errorf("index signature is not trusted")
}

// Sign returns the single-line base64 signature of the exact catalog bytes.
// Invalid private-key lengths and oversized documents return errors. Callers
// validate the document schema before choosing to publish its signature.
func Sign(data []byte, key ed25519.PrivateKey) ([]byte, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("index exceeds %d bytes", MaxDocumentBytes)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key length")
	}
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n"), nil
}
