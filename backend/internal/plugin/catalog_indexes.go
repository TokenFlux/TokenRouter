package plugin

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// catalog_indexes.go manages source registrations independently of installed code.
// The configured built-in row keeps its identity across restarts and accepts only
// enabled changes through administration. Custom sources retain caches when renamed
// or disabled, but changing their URL or verification key clears the old authority's
// document before another transfer can publish.
//
// Registration writes acquire the global registration gate before any source gate.
// Refresh workers acquire only the source gate, keeping URL uniqueness checks and
// cache publication ordered without blocking unrelated transfers. Source deletion
// leaves accepted artifact references on installations, whose nullable foreign keys
// and rollback references are maintained by the storage implementation.

// IndexInput supplies partial source changes while preserving omitted values.
type IndexInput struct {
	// Name and URL are required on creation. Omission during update preserves the
	// current value; both are forbidden for the built-in source even when unchanged.
	Name *string `json:"name,omitempty"` // configured source display name
	URL  *string `json:"url,omitempty"`  // configured HTTPS catalog location
	// PublicKey omission preserves the existing pin; an explicit empty value permits
	// unsigned custom catalogs and therefore changes the source's trust policy.
	PublicKey *string `json:"public_key,omitempty"`
	// Enabled defaults to true for new sources and is the only mutable builtin field.
	Enabled *bool `json:"enabled,omitempty"`
}

// ListIndexes returns source metadata and accepted cache observations for administrator inspection.
func (c *CatalogService) ListIndexes(ctx context.Context) ([]IndexSource, error) {
	return c.indexes.ListIndexes(ctx)
}

// CreateIndex saves a custom source after validating its identity and trust policy.
// URL uniqueness is checked under registration ownership. The returned row is
// persisted before background validation begins, so transfer failure cannot masquerade as failed creation.
//
// Registration ownership serializes duplicate-URL checks with other administrative
// writes. The new durable identifier becomes the refresh identity only after saving,
// and validation failure then belongs to that row's diagnostics. Callers receive
// the saved source regardless of network availability and can disable or edit it
// without recreating an ambiguously registered source after a transport failure.
func (c *CatalogService) CreateIndex(ctx context.Context, input IndexInput) (*IndexSource, error) {
	if !c.options.Enabled {
		return nil, ErrPluginsDisabled
	}
	if input.Name == nil || input.URL == nil {
		return nil, ErrIndexInvalid
	}
	item := &IndexSource{Enabled: true, SignatureStatus: SignatureUnknown}
	applyIndexInput(item, input)
	if err := validateIndexSource(item); err != nil {
		return nil, err
	}
	err := c.withSource(ctx, 0, func() error {
		if err := c.requireUniqueURL(ctx, 0, item.URL); err != nil {
			return err
		}
		return c.indexes.SaveIndex(ctx, item)
	})
	if err != nil {
		return nil, err
	}
	if item.Enabled {
		_, _ = c.beginRefresh(item.ID)
	}
	return item, nil
}

// UpdateIndex applies supplied fields without allowing builtin authority changes.
// URL and key replacements clear the old accepted cache. Enabled sources begin
// a new background read after persistence, while transfer failures remain source diagnostics.
//
// Registration and source locks span the fresh read, uniqueness check, and save.
// A refresh using an old URL or key must finish before this operation changes
// authority and clears its cache. Later refreshes therefore read the new persisted
// policy, and an omitted field cannot overwrite a concurrent administrator's update.
func (c *CatalogService) UpdateIndex(ctx context.Context, id int64, input IndexInput) (*IndexSource, error) {
	if !c.options.Enabled {
		return nil, ErrPluginsDisabled
	}
	if id <= 0 || (input.Name == nil && input.URL == nil && input.PublicKey == nil && input.Enabled == nil) {
		return nil, ErrIndexInvalid
	}
	var item *IndexSource
	err := c.withSource(ctx, 0, func() error {
		return c.withSource(ctx, id, func() error {
			var err error
			item, err = c.indexes.GetIndex(ctx, id)
			if err != nil {
				return err
			}
			if item.Builtin && (input.Name != nil || input.URL != nil || input.PublicKey != nil) {
				return ErrIndexInvalid
			}
			previousURL, previousKey := item.URL, item.PublicKey
			applyIndexInput(item, input)
			if err := validateIndexSource(item); err != nil {
				return err
			}
			if err := c.requireUniqueURL(ctx, id, item.URL); err != nil {
				return err
			}
			// The source gate excludes older refresh publication while authority and cache
			// change together, preventing accepted bytes from being attributed to another key.
			if item.URL != previousURL || item.PublicKey != previousKey {
				clearIndexCache(item)
			}
			return c.indexes.SaveIndex(ctx, item)
		})
	})
	if err != nil {
		return nil, err
	}
	if item.Enabled {
		_, _ = c.beginRefresh(id)
	}
	return item, nil
}

// DeleteIndex removes a custom registration after any admitted cache write has finished.
// Installed packages and business data remain owned by their installation records.
// Builtin deletion is rejected by storage, and stopped services admit no further source changes.
func (c *CatalogService) DeleteIndex(ctx context.Context, id int64) error {
	if !c.options.Enabled {
		return ErrPluginsDisabled
	}
	if id <= 0 {
		return ErrIndexNotFound
	}
	return c.withSource(ctx, 0, func() error {
		return c.withSource(ctx, id, func() error { return c.indexes.DeleteIndex(ctx, id) })
	})
}

// ensureBuiltin creates the official row or updates its configured mirror location.
// Existing enablement survives restarts. A URL change clears the former location's
// cache, and collisions with another registration fail without deleting or promoting it.
//
// Startup locates the builtin row by its persisted flag rather than by a mutable
// URL. Registration ownership prevents a simultaneous administrator from claiming
// the replacement location, and source ownership orders cache invalidation after
// any older transfer. The original identifier survives the change so installed
// releases retain their recorded source relationship and administrator enablement.
func (c *CatalogService) ensureBuiltin(ctx context.Context) error {
	return c.withSource(ctx, 0, func() error {
		items, err := c.indexes.ListIndexes(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			if !item.Builtin {
				continue
			}
			return c.withSource(ctx, item.ID, func() error {
				current, err := c.indexes.GetIndex(ctx, item.ID)
				if err != nil {
					return err
				}
				if current.URL == c.options.OfficialURL && current.PublicKey == "" {
					return nil
				}
				if err := c.requireUniqueURL(ctx, current.ID, c.options.OfficialURL); err != nil {
					return err
				}
				current.URL = c.options.OfficialURL
				current.PublicKey = ""
				clearIndexCache(current)
				return c.indexes.SaveIndex(ctx, current)
			})
		}
		item := &IndexSource{Name: "TokenRouter Official", URL: c.options.OfficialURL, Builtin: true, Enabled: true, SignatureStatus: SignatureUnknown}
		if err := validateIndexSource(item); err != nil {
			return err
		}
		if err := c.requireUniqueURL(ctx, 0, item.URL); err != nil {
			return err
		}
		return c.indexes.SaveIndex(ctx, item)
	})
}

// requireUniqueURL rejects another registration already owning the exact configured URL.
func (c *CatalogService) requireUniqueURL(ctx context.Context, id int64, location string) error {
	items, err := c.indexes.ListIndexes(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID != id && item.URL == location {
			return ErrIndexInvalid.WithMetadata(map[string]string{"field": "url", "problem": "already_registered"})
		}
	}
	return nil
}

// applyIndexInput replaces only supplied fields and normalizes administrator whitespace.
func applyIndexInput(item *IndexSource, input IndexInput) {
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
	}
	if input.URL != nil {
		item.URL = strings.TrimSpace(*input.URL)
	}
	if input.PublicKey != nil {
		item.PublicKey = strings.TrimSpace(*input.PublicKey)
	}
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
}

// validateIndexSource checks storage-sized names, HTTPS locations, and optional key encoding.
// Validation never fetches the URL. A missing custom key permits the explicitly
// unsigned policy, while built-in verification continues to use project-provisioned roots.
func validateIndexSource(item *IndexSource) error {
	if item.Name == "" || !utf8.ValidString(item.Name) || utf8.RuneCountInString(item.Name) > 100 ||
		strings.IndexFunc(item.Name, unicode.IsControl) >= 0 || !index.HTTPSURL(item.URL) {
		return ErrIndexInvalid
	}
	if item.PublicKey != "" {
		key, err := base64.StdEncoding.Strict().DecodeString(item.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return ErrIndexInvalid.WithMetadata(map[string]string{"field": "public_key"})
		}
	}
	return nil
}

// clearIndexCache removes accepted data when its URL or verification authority changes.
// Callers persist the cleared fields together with the new source configuration.
// Keeping an old document under a new authority would misattribute its accepted releases.
func clearIndexCache(item *IndexSource) {
	item.CachedDocument = nil
	item.CachedAt = nil
	item.SignatureStatus = SignatureUnknown
	item.LastError = ""
}
