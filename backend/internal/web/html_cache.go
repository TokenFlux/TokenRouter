//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// html_cache.go caches rendered language pages and rejects publication from an
// invalidated settings generation.
// A language page can finish rendering after public settings invalidate the
// cache. Snapshot returns its entry and generation under one read lock, and
// Publish stores a render only when that observed generation still matches.
// Invalidate clears all language entries and advances the generation under the
// write lock, preventing an earlier render from repopulating the newly emptied
// cache.
//
// ETags combine the base-template digest with a digest of the language and
// serialized settings bytes. Rendering content and substituted request nonces
// are not hashed directly. Stored and snapshot HTML and settings have copied
// byte storage,
// while Publish returns the caller's HTML slice even when generation mismatch
// prevents storage. SetBaseHTML establishes the template component before
// publication and does not itself invalidate existing entries.

// HTMLCache coordinates language-page storage and invalidation while retaining
// the configured base-template hash.
type HTMLCache struct {
	// mu protects entries and settingsVersion so Snapshot pairs an entry with its
	// publication generation. Publish checks that same generation while inserting a copied
	// language page, and Invalidate clears entries before advancing it.
	mu      sync.RWMutex          // entry and generation access lock
	entries map[string]CachedHTML // stored pages by language code
	// baseHTMLHash is assigned by SetBaseHTML under mu before publication. Changing
	// it later affects new tags but leaves already-stored entries available until invalidation.
	baseHTMLHash    string
	settingsVersion uint64 // current settings invalidation generation
}

// CachedHTML pairs page bytes with their template, language, and
// settings-derived validator text.
type CachedHTML struct {
	// Content is copied in stored entries and Snapshot results. A value returned
	// directly by Publish instead shares the HTML slice supplied by that caller.
	Content []byte
	// SettingsJSON is copied for publication and reads; request-specific metadata
	// rendering can inspect it without sharing mutable storage with another response.
	SettingsJSON []byte // public metadata snapshot bytes
	ETag         string // quoted page revalidation tag
}

// NewHTMLCache returns an empty language-page cache ready for base-template
// initialization.
func NewHTMLCache() *HTMLCache {
	return &HTMLCache{}
}

// SetBaseHTML records a truncated SHA-256 digest of the supplied base template.
// Call it before publishing pages; it stores no template bytes and leaves any
// existing entries and invalidation generation unchanged.
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := sha256.Sum256(baseHTML)
	c.baseHTMLHash = hex.EncodeToString(hash[:8]) // Eight digest bytes yield sixteen hex characters.
}

// Invalidate clears every language entry and advances the settings generation
// under the cache lock. Renders carrying the previous generation can still
// produce a return value from Publish, but their pages are not stored.
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clear entries and advance their generation together. A render that observed
	// the previous generation then fails Publish's storage comparison after this invalidation.
	c.entries = nil
	c.settingsVersion++
}

// Snapshot returns copied HTML for code and the generation observed with that
// lookup. Missing language entries return nil with the current generation,
// allowing the caller to render and attempt publication against that value.
//
// Both observations occur under one read lock so a concurrent invalidation
// cannot place an old entry beside a newly advanced generation in this result.
// The returned Content can be modified independently of cache storage, and its
// ETag remains the immutable validator computed at publication.
func (c *HTMLCache) Snapshot(code string) (*CachedHTML, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot, ok := c.entries[code]
	if !ok {
		return nil, c.settingsVersion
	}
	return &CachedHTML{Content: append([]byte(nil), snapshot.Content...), SettingsJSON: append([]byte(nil), snapshot.SettingsJSON...), ETag: snapshot.ETag}, c.settingsVersion
}

// Publish returns html with a validator derived from the template hash, code,
// and settingsJSON. The returned Content shares the caller's html slice,
// including when the supplied version is stale.
//
// Under the cache write lock, matching version stores a copy of html for that
// language; a mismatch leaves current entries untouched. This lets renders
// started before invalidation finish serving their initiating requests while
// preventing those renders from replacing later cache state. Call SetBaseHTML
// before publishing, and keep serialized settings stable when relying on the
// resulting ETag.
func (c *HTMLCache) Publish(code string, version uint64, html, settingsJSON []byte) CachedHTML {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := append([]byte(code+"\n"), settingsJSON...)
	rendered := CachedHTML{Content: html, SettingsJSON: append([]byte(nil), settingsJSON...), ETag: c.generateETag(key)}
	// Compare under the same lock used for invalidation so an earlier settings
	// render cannot be stored after its generation has been replaced by a new one.
	if version == c.settingsVersion {
		if c.entries == nil {
			c.entries = map[string]CachedHTML{}
		}
		c.entries[code] = CachedHTML{Content: append([]byte(nil), html...), SettingsJSON: append([]byte(nil), settingsJSON...), ETag: rendered.ETag}
	}
	return rendered
}

// generateETag combines the current template hash with the supplied
// language/settings byte digest as quoted validator text. Its caller holds mu,
// and each digest component uses eight bytes, so the tag represents truncated
// hashes rather than full rendered-content identity.
func (c *HTMLCache) generateETag(settingsJSON []byte) string {
	settingsHash := sha256.Sum256(settingsJSON)
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(settingsHash[:8]) + `"`
}
