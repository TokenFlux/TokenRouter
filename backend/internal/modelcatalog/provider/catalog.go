package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// catalog.go builds and publishes model catalogs with pricing and attribute
// supplements.
// Catalog publication combines one downloaded body with custom and embedded
// supplements. Model fields keep catalog values first, custom missing-field
// additions second, and embedded additions last; operation defaults start with
// embedded values and accept custom overrides. Attributes, prices, and indexes
// are prepared from those same layers before readers receive a replacement.
//
// Persistent updates write and close a temporary body before renaming it into
// the cache path, then replace in-memory data under the query lock. Supplement
// fingerprints sampled around construction detect observed content changes and
// permit bounded retries. Loading can use retained body bytes or embedded data
// when disk inputs fail, while first startup can publish a usable base and keep
// the failed candidate's diagnostic for later repair.

// supplementFields names permitted missing-price and rule additions together
// with the source labels recorded when supplements fill them.
var supplementFields = map[string]string{
	"input_cost_per_token": "input", "output_cost_per_token": "output",
	"input_cost_per_token_priority": "priority_input", "output_cost_per_token_priority": "priority_output",
	"cache_read_input_image_token_cost": "image_cache_read",
	"cache_read_input_token_cost":       "cache_read", "cache_read_input_token_cost_priority": "priority_cache_read",
	"cache_creation_input_token_cost": "cache_write", "cache_creation_input_token_cost_priority": "priority_cache_write",
	"cache_creation_input_token_cost_above_1hr": "cache_write_1h",
	"input_cost_per_image_token":                "image_input", "output_cost_per_image_token": "image_output", "output_cost_per_image": "image",
	"image_prices": "image_prices", "video_prices": "video_prices",
	"fast_multiplier": "fast_multiplier", "flex_multiplier": "flex_multiplier",
	"max_reasoning_effort_multiplier": "max_reasoning_effort_multiplier",
	"cache_write_multiplier":          "cache_write_multiplier", "cache_write_1h_multiplier": "cache_write_1h_multiplier",
	"time_pricing": "time_pricing", "source_url": "source_url", "verified_at": "verified_at",
}

// AttributeSnapshot contains administrative display data published under the
// same lock as prices.
type AttributeSnapshot struct {
	Items       []modelcatalog.Entry `json:"items"`                // copied administrative metadata entries
	Version     string               `json:"version"`              // retained catalog content version
	LastUpdated time.Time            `json:"last_updated"`         // retained catalog update time
	LastError   string               `json:"last_error,omitempty"` // latest candidate failure message
}

// CapabilityView captures the retained catalog under the read lock for
// subsequent metadata lookups. Updates publish different catalog data, and a
// capture before publication behaves as an empty modelcatalog.View.
func (s *Service) CapabilityView() modelcatalog.View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modelCatalog.View()
}

// AttributesSnapshot copies all prepared catalog entries and captures their
// update status under the read lock. Returned attributes and price values can
// be modified independently; the initial empty index returns no items.
func (s *Service) AttributesSnapshot() AttributeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := AttributeSnapshot{Items: s.attributeIndex.Search(modelcatalog.AttributeQuery{}).Items, LastUpdated: s.lastUpdated, LastError: s.lastCatalogError}
	if s.modelCatalog != nil {
		result.Version = s.modelCatalog.Version
	}
	return result
}

// BillingDefaults returns copied operation default price pointers and source
// labels from the retained publication. Callers can adjust their returned
// values independently; a service awaiting publication returns its unset
// defaults.
func (s *Service) BillingDefaults() pricing.OperationPrices {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.billingDefaults.Clone()
}

// ModelAttributes resolves copied attributes for a complete model identity
// using one candidate generator from the injected factory. Recognized provider
// qualification remains intact, and unresolved metadata returns unknown
// attribute fields for the caller. A configured candidate factory must return
// a callable function.
func (s *Service) ModelAttributes(model string) modelcatalog.Attributes {
	candidates := []string{model}
	if s.options.ModelLookupCandidates != nil {
		candidates = append(candidates, s.options.ModelLookupCandidates()(model)...)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	attributes, _ := s.modelCatalog.LookupAttributes(model, candidates)
	return attributes
}

// buildModelsCatalog prepares metadata, prices, and operation defaults from
// one catalog body and its supplement layers. Invalid model, supplement, or
// price fields reject the candidate; a valid attribute-only catalog may return
// an empty price map.
//
// Model supplements fill custom gaps before embedded gaps, retaining existing
// catalog fields including explicit zero prices. Operation defaults use the
// opposite layering order so custom fields can override embedded defaults.
// Both paths are validated before return, and price diagnostics record orphan
// cache tiers or one-sided context surcharges for administrative inspection.
func (s *Service) buildModelsCatalog(body []byte) (*modelcatalog.Catalog, map[string]*CatalogModelPricing, pricing.OperationPrices, error) {
	catalog, err := modelcatalog.Parse(body)
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	raw := pricing.ModelsDevPrices(catalog)
	// Fill custom model gaps before embedded gaps so declared catalog values
	// and explicit custom additions remain authoritative when the next layer
	// is applied.
	custom, err := loadLocalPricingEntries(s.options.FallbackFile)
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	builtin, err := decodeModelSupplement(modelcatalog.Supplements(), "embedded model supplements")
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	for _, supplement := range []map[string]json.RawMessage{custom, builtin} {
		if err := applyModelSupplements(raw, catalog, supplement); err != nil {
			return nil, nil, pricing.OperationPrices{}, err
		}
	}
	// Operation defaults are overrides rather than missing-field supplements.
	// Apply embedded values first so populated custom fields, including zero
	// prices, replace those defaults.
	var defaults pricing.OperationPrices
	for _, supplement := range []map[string]json.RawMessage{builtin, custom} {
		if body := supplement[pricing.BillingDefaultsKey]; len(body) > 0 {
			if err := json.Unmarshal(body, &defaults); err != nil {
				return nil, nil, pricing.OperationPrices{}, err
			}
		}
	}
	if err := defaults.Validate(); err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	if len(raw) == 0 {
		return catalog, map[string]*CatalogModelPricing{}, defaults, nil
	}
	prices, diagnostics, err := pricing.ParsePricingEntries(raw)
	if validationErr := diagnostics.ValidationError(); validationErr != nil {
		return nil, nil, pricing.OperationPrices{}, validationErr
	}
	// Missing prices alone are valid for metadata records. Accept that parser
	// outcome while retaining validation failures so attribute-only candidates
	// can still be published.
	if err != nil && !errors.Is(err, pricing.ErrNoPricingEntries) {
		return nil, nil, pricing.OperationPrices{}, err
	}
	if prices == nil {
		prices = map[string]*CatalogModelPricing{}
	}
	warnOrphanCacheTierFields(diagnostics.OrphanCacheTiers)
	warnLopsidedLongContextLadders(diagnostics.LopsidedLadders)
	return catalog, prices, defaults, nil
}

// applyModelSupplements mutates unpublished metadata and raw price candidates
// by applying exact supplement keys before equivalent first party aliases.
// For distinct exact keys, their supplied values are applied before another
// key's supplement can fill the remaining gaps.
//
// Alias propagation uses the same provider and complete model record, keeping
// dated identities and relay records separate. Operation defaults are skipped
// for their independent override path. An error can leave these candidate maps
// partially prepared, so buildModelsCatalog discards them instead of exposing
// the partial work as a publication.
// Raw price keys are compared as supplied, while metadata keys are normalized;
// callers use consistently spelled keys when supplementing the same identity.
func applyModelSupplements(raw map[string]json.RawMessage, catalog *modelcatalog.Catalog, supplement map[string]json.RawMessage) error {
	for model, entry := range supplement {
		if model == pricing.BillingDefaultsKey {
			continue
		}
		if err := catalog.ApplyAttributeSupplement(model, entry); err != nil {
			return err
		}
		if err := mergePricingSupplement(raw, model, entry); err != nil {
			return err
		}
	}
	// Fill equivalent lookup keys after exact settings have been applied, so
	// each qualified record's explicit zero or populated field blocks later
	// additions from its bare alias.
	for model, entry := range supplement {
		if model == pricing.BillingDefaultsKey {
			continue
		}
		for _, alias := range catalog.FirstPartyAliases(model) {
			if alias == model {
				continue
			}
			if err := catalog.ApplyAttributeSupplement(alias, entry); err != nil {
				return err
			}
			if err := mergePricingSupplement(raw, alias, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergePricingSupplement merges one JSON supplement into an unpublished raw
// price map. An existing record receives permitted fields only when their keys
// are missing, retaining explicit zero values and populated compound fields.
//
// Newly introduced records keep the decoded supplement object with local
// source labels. Existing records retain their data source and add local
// labels only for filled fields. Source label maps are serialized before the
// completed object replaces its raw entry; malformed objects or labels reject
// this candidate merge.
func mergePricingSupplement(raw map[string]json.RawMessage, model string, entry json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entry, &fields); err != nil {
		return fmt.Errorf("invalid pricing supplement %s: %w", model, err)
	}
	if fields == nil {
		return fmt.Errorf("invalid pricing supplement %s: expected an object", model)
	}
	fields["source"] = json.RawMessage(`"local_supplement"`)
	base, exists := raw[model]
	if !exists {
		sources := map[string]string{}
		for key, label := range supplementFields {
			if _, ok := fields[key]; ok {
				sources[label] = "local_supplement"
			}
		}
		// Source labels contain string keys and values, which JSON can encode
		// directly. Serialize those labels alongside the new object's supplied
		// fields before installing its raw body.
		fields["price_sources"], _ = json.Marshal(sources)
		body, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		raw[model] = body
		return nil
	}
	var combined map[string]json.RawMessage
	if err := json.Unmarshal(base, &combined); err != nil {
		return err
	}
	var sources map[string]string
	if value, exists := combined["price_sources"]; exists {
		if err := json.Unmarshal(value, &sources); err != nil {
			return fmt.Errorf("invalid price_sources for %s: %w", model, err)
		}
	}
	if sources == nil {
		sources = map[string]string{}
	}
	for key, label := range supplementFields {
		if _, exists := combined[key]; exists {
			continue
		}
		if value, exists := fields[key]; exists {
			combined[key] = value
			sources[label] = "local_supplement"
		}
	}
	// The label map has a string-only JSON representation. Store its encoded
	// values before replacing this candidate's raw object so filled fields
	// retain their local source.
	combined["price_sources"], _ = json.Marshal(sources)
	body, err := json.Marshal(combined)
	if err != nil {
		return err
	}
	raw[model] = body
	return nil
}

// publishModelsCatalog builds and publishes one catalog body with its
// current supplement layers; production loaders hold updateMu for the attempt.
// Isolated fixtures serialize their own publication calls when scheduled
// updates cannot overlap them.
// Fingerprints before and after construction permit up to three builds when
// their sampled values differ, rejecting continued observed changes.
//
// Matching samples do not detect a file changed and restored between them.
// Accepted candidates append the supplement fingerprint to metadata version,
// retain the raw body's hash separately, and prepare indexes before publication.
// When persist is true, write, close, and rename finish first; failures leave
// the retained in-memory publication intact. A final mu-protected replacement
// gives query captures the prepared attributes, prices, defaults, and status
// together.
func (s *Service) publishModelsCatalog(body []byte, updated time.Time, persist bool) error {
	var catalog *modelcatalog.Catalog
	var prices map[string]*CatalogModelPricing
	var fingerprint string
	var defaults pricing.OperationPrices
	// Compare content samples around each build to detect observed supplement
	// changes. Rebuild a changed candidate before assigning its version and
	// exposing its attributes and prices together.
	for attempt := range 3 {
		before := s.customPricingFilesFingerprint()
		var err error
		catalog, prices, defaults, err = s.buildModelsCatalog(body)
		if err != nil {
			return err
		}
		fingerprint = s.customPricingFilesFingerprint()
		if before == fingerprint {
			break
		}
		if attempt == 2 {
			return fmt.Errorf("custom pricing files changed during catalog update")
		}
	}
	sourceHash := catalog.Version
	if fingerprint != "" {
		catalog.Version += ":" + fingerprint
	}
	index := modelcatalog.NewIndex(catalog)
	priceModels := buildPriceModels(catalog, prices)
	if persist {
		path := s.catalogFilePath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(path), "models-catalog-*.json")
		if err != nil {
			return err
		}
		name := temp.Name()
		// Remove an unrenamed temporary file when writing or replacement fails.
		// A successful rename consumes this path, and cleanup errors leave the
		// publication result unchanged.
		defer func() { _ = os.Remove(name) }()
		// Close the written body before rename, returning write failure first.
		// Readers receive the in-memory replacement only after this persistent
		// cache replacement has completed.
		_, writeErr := temp.Write(body)
		closeErr := temp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Rename(name, path); err != nil {
			return err
		}
	}
	// Replace all prepared publication data under the same lock used by query
	// captures, so one captured view cannot mix this candidate's prices with
	// the previous metadata.
	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, prices)
	s.modelCatalog, s.pricingData = catalog, prices
	s.attributeIndex = index
	s.priceModels = priceModels
	s.billingDefaults = defaults
	s.catalogBody = append([]byte(nil), body...)
	s.lastUpdated, s.localHash, s.lastCatalogError = updated, sourceHash, ""
	s.customFilesHash = fingerprint
	s.mu.Unlock()
	return nil
}

// loadModelsCatalog serializes a local rebuild from disk, retained body
// bytes, or the embedded catalog. A readable invalid candidate is rejected
// when a current publication exists, leaving that publication available and
// recording the candidate error.
//
// Disk read failures can reuse retained body bytes. A rejected embedded
// candidate is retried with persistence disabled, allowing cache write
// failures to recover in memory. On first startup, a separate candidate
// without the configured custom layer can supply usable metadata while the
// initial candidate error remains in status.
// Later successful publication clears that diagnostic and replaces the complete
// retained version.
func (s *Service) loadModelsCatalog() (err error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	defer func() { s.recordCatalogError(err) }()
	body, readErr := os.ReadFile(s.catalogFilePath())
	updated := time.Now()
	s.mu.RLock()
	hasCurrent := s.modelCatalog != nil
	if readErr != nil && len(s.catalogBody) > 0 {
		// Retained raw bytes allow custom-layer rebuilding when the disk cache
		// cannot be read. Copy them under the query lock before constructing a
		// fresh candidate.
		body = append([]byte(nil), s.catalogBody...)
		readErr = nil
	}
	s.mu.RUnlock()
	if info, err := os.Stat(s.catalogFilePath()); err == nil {
		updated = info.ModTime()
	}
	var candidateErr error
	if readErr == nil {
		candidateErr = s.publishModelsCatalog(body, updated, false)
		if candidateErr == nil {
			return nil
		}
		if hasCurrent {
			return candidateErr
		}
	}
	cachedBody := body
	body, err = modelcatalog.Offline()
	if err != nil {
		return err
	}
	candidateErr = s.publishModelsCatalog(body, time.Now(), true)
	if candidateErr == nil {
		return nil
	}
	// Retry the embedded candidate with persistence disabled. A cache write
	// failure can then recover in memory; parsing and supplement errors still
	// reject the candidate during the repeated build.
	if err := s.publishModelsCatalog(body, time.Now(), false); err == nil {
		return nil
	}
	if hasCurrent {
		return candidateErr
	}
	// First startup has no publication to retain. Try base catalog candidates
	// with embedded supplements and record the failed configured candidate's
	// diagnostic for subsequent repair.
	bootstrap := NewService(Options{DataDir: s.options.DataDir}, nil)
	for _, candidate := range [][]byte{cachedBody, body} {
		if len(candidate) == 0 {
			continue
		}
		if err := bootstrap.publishModelsCatalog(candidate, updated, true); err != nil {
			if err := bootstrap.publishModelsCatalog(candidate, updated, false); err != nil {
				continue
			}
		}
		// Install the usable bootstrap data and its initial failure diagnostic
		// under the query lock so status readers can explain the retained base
		// publication.
		s.mu.Lock()
		s.modelCatalog, s.pricingData = bootstrap.modelCatalog, bootstrap.pricingData
		s.attributeIndex = bootstrap.attributeIndex
		s.priceModels = bootstrap.priceModels
		s.billingDefaults = bootstrap.billingDefaults
		s.catalogBody = bootstrap.catalogBody
		s.lastUpdated, s.localHash = bootstrap.lastUpdated, bootstrap.localHash
		s.lastCatalogError = candidateErr.Error()
		s.mu.Unlock()
		return nil
	}
	return candidateErr
}

// recordCatalogError stores a candidate failure under the administrative
// status lock while retaining its current publication. Nil errors leave the
// diagnostic unchanged; successful publication clears errors in its
// replacement sequence.
func (s *Service) recordCatalogError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastCatalogError = err.Error()
	s.mu.Unlock()
}

// updateModelsCatalog serializes a remote candidate update or delegates to
// local rebuilding when RemoteURL is blank. Remote work validates the URL,
// uses a thirty-second request context, and clears the outgoing ETag when
// force requests an unconditional download.
//
// A 304 retains raw bytes and checks supplement content for a local rebuild;
// it fails when no retained bytes exist. Unchanged body and supplement hashes
// can skip publication, while force still rebuilds a downloaded same-body
// candidate. Network, validation, and persistence errors retain the current
// publication and retained validator. Downloaded candidates and non-304
// same-content responses install new validators after acceptance; a 304 keeps
// the existing validator.
func (s *Service) updateModelsCatalog(force bool) (err error) {
	// Dispatch local rebuilding before acquiring updateMu because that loader
	// owns its serialization and can reuse retained raw bytes when the disk
	// cache is unavailable.
	if strings.TrimSpace(s.options.RemoteURL) == "" {
		return s.loadModelsCatalog()
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	defer func() { s.recordCatalogError(err) }()
	url, err := s.validateCatalogURL(s.options.RemoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	// Release this download's context timer on every return path. When the
	// scheduler invokes this update, its stop signal waits for the attempt;
	// it does not cancel the independent request context.
	defer cancel()
	if s.remoteClient == nil {
		return fmt.Errorf("model catalog remote client is not configured")
	}
	validator := s.catalogETag
	if force {
		validator = ""
	}
	body, etag, unchanged, err := s.remoteClient.FetchCatalog(ctx, url, validator)
	if err != nil {
		return err
	}
	if unchanged {
		// A remote 304 leaves the body unchanged but custom content can differ.
		// Compare its fingerprint and validate a changed layer before clearing
		// the retained update diagnostic.
		s.mu.RLock()
		current := append([]byte(nil), s.catalogBody...)
		updated, fingerprint := s.lastUpdated, s.customFilesHash
		s.mu.RUnlock()
		if len(current) == 0 {
			return fmt.Errorf("catalog returned 304 without a local snapshot")
		}
		if s.customPricingFilesFingerprint() != fingerprint {
			if err := s.publishModelsCatalog(current, updated, false); err != nil {
				return err
			}
		}
		// The retained body and any changed supplement layer were accepted.
		// Clear the failure under the status lock before readers inspect this
		// successful unchanged-body result.
		s.mu.Lock()
		s.lastCatalogError = ""
		s.mu.Unlock()
		return nil
	}
	hash := sha256.Sum256(body)
	s.mu.RLock()
	same := s.localHash == hex.EncodeToString(hash[:]) && s.customFilesHash == s.customPricingFilesFingerprint()
	s.mu.RUnlock()
	if same && !force {
		// Both fingerprints match the retained publication, so a conditional
		// refresh can keep its existing data and timestamp while accepting the
		// response's current validator.
		s.catalogETag = etag
		s.mu.Lock()
		s.lastCatalogError = ""
		s.mu.Unlock()
		return nil
	}
	if err = s.publishModelsCatalog(body, time.Now(), true); err != nil {
		return err
	}
	s.catalogETag = etag
	return nil
}

// ModelIDs returns a copied sorted list from the prepared metadata index,
// including bare aliases and qualified identities. Callers can modify the
// list independently; an unprepared index returns an empty slice.
func (s *Service) ModelIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attributeIndex.ModelIDs()
}

// ModelEntry resolves metadata with the catalog's qualified identity rules
// and one query candidate generator. Attributes and source labels are copied,
// while found entry prices can remain shared; unresolved names retain the
// requested ID and any known public attributes. A configured candidate factory
// must return a callable function.
func (s *Service) ModelEntry(model string) modelcatalog.Entry {
	candidates := []string{model}
	if s.options.ModelLookupCandidates != nil {
		candidates = append(candidates, s.options.ModelLookupCandidates()(model)...)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := modelcatalog.Entry{Model: model}
	if s.modelCatalog == nil {
		return entry
	}
	if found, ok := s.modelCatalog.Lookup(s.modelCatalog.IdentityCandidates(model, candidates)); ok {
		return found
	}
	entry.Attributes, _ = s.modelCatalog.LookupAttributes(model, candidates)
	return entry
}

// ModelVersion returns the retained catalog's content version. A service with
// no retained metadata catalog returns an empty string; successful catalog
// publication replaces the version together with the queryable model identities.
func (s *Service) ModelVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.modelCatalog == nil {
		return ""
	}
	return s.modelCatalog.Version
}

// AttributeIndex captures the prepared index and its administrative status
// under one read lock. Later publications leave the retained index available,
// and index searches copy their selected entries for caller mutation.
func (s *Service) AttributeIndex() (modelcatalog.Index, AttributeSnapshot) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := s.localHash
	if s.modelCatalog != nil {
		version = s.modelCatalog.Version
	}
	return s.attributeIndex, AttributeSnapshot{Version: version, LastUpdated: s.lastUpdated, LastError: s.lastCatalogError}
}

// OpenCodeProtocols copies the selected mode's protocol suggestions from the
// current publication, returning nil before any catalog is published. The read
// lock keeps the suggestions paired with one validated catalog; callers may
// edit the returned map before saving overrides.
func (s *Service) OpenCodeProtocols(mode string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.modelCatalog == nil {
		return nil
	}
	return s.modelCatalog.OpenCodeProtocols(mode)
}
