package provider

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// service.go manages model catalog initialization, scheduled refreshes, and
// retained publication reads.
// Catalog prices, attributes, and operation defaults are retained together
// between updates. Initialization loads a cached or embedded publication;
// optional remote synchronization and supplement watching subsequently build
// replacements. A rejected candidate leaves the previous publication usable
// and records its error for administrative status.
//
// The update loop attempts remote work before checking local supplements on
// each tick. Update serialization belongs to updateMu, while query captures
// and publication replacement use mu. Stop closes the loop's signal and waits
// for its work to exit, including an update already in progress. Lifecycle
// callers initialize and start the service before stopping it; queries can
// continue reading the retained publication afterward.

// RemoteClient downloads catalog candidates with an optional conditional ETag
// validator.
type RemoteClient interface {
	// FetchCatalog returns candidate bytes, an ETag, and whether the current
	// body is unchanged. Callers discard bytes on error, retain their existing
	// body for unchanged results, and pass cancellation through ctx.
	FetchCatalog(ctx context.Context, url, etag string) ([]byte, string, bool, error)
}

// Service loads, synchronizes, and publishes catalog prices and display
// attributes together.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
type Service struct {
	// billingDefaults, modelCatalog, attributeIndex, priceModels,
	// lastCatalogError, catalogBody, pricingData, lastUpdated, localHash, and
	// customFilesHash are read and replaced under mu after initialization.
	// Published catalog and pricing values remain read-only for views
	// retaining those references.
	billingDefaults purepricing.OperationPrices // published operation price values
	// updateMu serializes catalog construction, persistent cache replacement,
	// and ETag changes across remote updates and local supplement reloads.
	updateMu       sync.Mutex
	modelCatalog   *modelcatalog.Catalog // retained metadata publication reference
	attributeIndex modelcatalog.Index    // prepared attribute query index
	priceModels    []PriceModel          // sorted price listing rows
	// catalogETag identifies the retained remote body for conditional requests.
	// updateMu protects its reads and replacements throughout the remote update.
	catalogETag      string
	lastCatalogError string // latest candidate update failure
	catalogBody      []byte // retained unsupplemented remote catalog bytes
	// options retains startup configuration. Query views can share this
	// pointer, so production callers keep its fields and referenced host list
	// read-only.
	options      *Options
	remoteClient RemoteClient                    // injected conditional catalog downloader
	mu           sync.RWMutex                    // retained publication read write lock
	pricingData  map[string]*CatalogModelPricing // published model price records
	lastUpdated  time.Time                       // retained catalog update time
	localHash    string                          // unsupplemented catalog content fingerprint
	// customFilesHash records supplement content from the last successful
	// build. Reload compares it with the current content fingerprint before
	// preparing a replacement.
	customFilesHash string

	// stopCh, wg, startOnce, and stopOnce coordinate one scheduled loop.
	// Start registers its work once, Stop closes the signal once, and Wait
	// joins registered work without canceling a fetch already in progress.
	stopCh    chan struct{}  // scheduled loop stop signal
	wg        sync.WaitGroup // registered update loop work
	startOnce sync.Once      // one-time update loop start
	stopOnce  sync.Once      // one-time stop signal close
}

// CatalogModelPricing is the billing package's representation of catalog prices.
type (
	CatalogModelPricing = purepricing.CatalogModelPricing
)

// Options contains catalog configuration supplied by app at startup.
type Options struct {
	DataDir string // catalog cache directory path
	// RemoteURL enables conditional downloads when populated. An empty value
	// leaves initialization and supplement watching on their local loading paths.
	RemoteURL string
	// FallbackFile names the optional custom supplement. Missing files supply an
	// empty layer, while unreadable or invalid content rejects an attempted
	// rebuild.
	FallbackFile string
	// CheckIntervalMinutes selects scheduler spacing. Values below one minute
	// use ten minutes when either remote synchronization or supplement
	// watching starts.
	CheckIntervalMinutes int
	// URLAllowlistEnabled selects HTTPS allowlist validation. When false, URL
	// validation uses format checks and the AllowInsecureHTTP option instead.
	URLAllowlistEnabled bool
	// AllowInsecureHTTP permits HTTP in format-only validation. When allowlisting
	// is enabled, validateCatalogURL uses the HTTPS validator with its host policy.
	AllowInsecureHTTP bool
	// AllowPrivateHosts is passed to allowlisted egress validation. Format-only
	// validation does not inspect this option when URLAllowlistEnabled is false.
	AllowPrivateHosts bool
	// PricingHosts supplies the egress validator's permitted catalog hosts.
	// NewService copies Options by value, retaining this slice for read-only use.
	PricingHosts []string
	// ModelLookupCandidates acquires one candidate generator per query. The
	// resulting function keeps that query's identity expansion inputs together.
	// Metadata methods require a supplied factory to return a callable
	// generator; price queries can use default candidates when the produced
	// function is nil.
	ModelLookupCandidates func() func(string) []string
}

// Snapshot contains independently mutable pricing data and a fixed catalog
// identity for queries and tests.
type Snapshot struct {
	BillingDefaults purepricing.OperationPrices // copied operation price defaults
	// catalogIdentity retains the source publication by reference. Snapshot
	// consumers keep it read-only, and NewServiceFromSnapshot reuses it for
	// metadata queries.
	catalogIdentity *modelcatalog.Catalog
	// Data contains copied prices when returned by Snapshot. Passing it to
	// NewServiceFromSnapshot transfers its map and entry ownership to that
	// service.
	Data                       map[string]*CatalogModelPricing
	LastUpdated                time.Time // retained publication update time
	LocalHash, CustomFilesHash string    // raw catalog content fingerprint and custom supplement content fingerprint
	LastError                  string    // captured candidate update failure
}

// NewService constructs an unloaded catalog service with the supplied
// downloader and startup options. Initialize loads its first publication before
// Start begins synchronization; option slices and injected dependencies remain
// shared with their caller.
func NewService(options Options, remoteClient RemoteClient) *Service {
	s := &Service{
		options:      &options,
		remoteClient: remoteClient,
		pricingData:  make(map[string]*CatalogModelPricing),
		stopCh:       make(chan struct{}),
	}
	return s
}

// NewServiceFromSnapshot initializes a query service from parsed snapshot
// data without starting I/O or scheduled work. It takes ownership of Data,
// clones operation defaults, and retains the snapshot's read-only catalog
// identity for model queries.
func NewServiceFromSnapshot(options Options, remote RemoteClient, snapshot Snapshot) *Service {
	s := NewService(options, remote)
	s.pricingData = snapshot.Data
	s.billingDefaults = snapshot.BillingDefaults.Clone()
	s.modelCatalog = snapshot.catalogIdentity
	s.attributeIndex = modelcatalog.NewIndex(s.modelCatalog)
	s.priceModels = buildPriceModels(s.modelCatalog, s.pricingData)
	s.lastCatalogError = snapshot.LastError
	s.lastUpdated = snapshot.LastUpdated
	s.localHash = snapshot.LocalHash
	s.customFilesHash = snapshot.CustomFilesHash
	return s
}

// Initialize loads the first retained catalog before scheduled work starts.
// Cache directory creation failures are logged, allowing an in-memory
// publication to serve queries; candidate load failures are returned with
// initialization context.
func (s *Service) Initialize() error {
	// Cache persistence can fail while embedded metadata remains usable in
	// memory. Record directory creation errors and let the loader attempt that
	// available publication path.
	if err := os.MkdirAll(s.currentOptions().DataDir, 0o755); err != nil {
		logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Failed to create data directory: %v", err)
	}

	// Load the retained publication before Start can schedule its first remote
	// update, giving query consumers metadata while the downloaded replacement
	// is being validated.
	if err := s.loadInitialCatalog(); err != nil {
		return fmt.Errorf("failed to load model catalog: %w", err)
	}

	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Service initialized with %d models", len(s.pricingData))
	return nil
}

// Stop closes the scheduled loop's signal once and joins any registered work.
// It waits for the loop's in-progress update to return. Callers finish any
// intended Start before stopping, so work registration does not race with Wait.
func (s *Service) Stop() {
	// Signal the loop before joining its lifetime; otherwise an active loop
	// can keep Wait blocked. stopOnce lets concurrent or repeated Stop calls
	// share that signal.
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.Wait()
	logging.LegacyPrintf("service.modelcatalog", "%s", "[ModelCatalog] Service stopped")
}

// startUpdateScheduler starts one update loop when a remote URL or custom
// supplement path is configured. The startup attempt and each scheduled remote
// fetch retain the current publication on failure, and supplement checks follow
// remote work on each tick.
//
// An interval below one minute selects ten minutes. Configuration choices are
// captured when the loop starts, and Stop's closed channel is observed between
// update cycles. The wait group tracks the entire loop, so Stop also waits for
// its current remote fetch or local rebuild to return.
func (s *Service) startUpdateScheduler() {
	if s == nil || s.options == nil {
		return
	}
	remoteEnabled := strings.TrimSpace(s.currentOptions().RemoteURL) != ""
	watchCustom := s.hasCustomPricingFiles()
	if !remoteEnabled {
		logging.LegacyPrintf("service.modelcatalog", "%s", "[ModelCatalog] Remote sync disabled: pricing remote URL is empty")
	}
	if !remoteEnabled && !watchCustom {
		return
	}

	checkInterval := time.Duration(s.currentOptions().CheckIntervalMinutes) * time.Minute
	// Sub-minute intervals use the configured polling default. This also
	// supplies NewTicker's required positive duration when the integer
	// interval is zero or negative.
	if checkInterval < time.Minute {
		checkInterval = 10 * time.Minute
	}

	// Register the whole scheduler lifetime before it runs so Stop can join
	// both the initial remote attempt and subsequent ticker-driven update
	// cycles.
	s.wg.Go(func() {
		if remoteEnabled {
			// The update method records a failed candidate in status. Continue
			// into periodic attempts so the retained startup catalog remains
			// usable during remote failures.
			_ = s.syncWithRemote()
		}
		ticker := time.NewTicker(checkInterval)
		// Stop the ticker when the loop exits so a stopped service releases its
		// timer resources after registered update work has finished.
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if remoteEnabled {
					if err := s.syncWithRemote(); err != nil {
						logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Sync failed: %v", err)
					}
				}
				if watchCustom {
					s.reloadIfCustomFilesChanged()
				}
			case <-s.stopCh:
				return
			}
		}
	})

	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Update scheduler started (check every %v, remote sync=%t, custom file watch=%t)", checkInterval, remoteEnabled, watchCustom)
}

// loadInitialCatalog delegates cached or embedded startup loading to the
// catalog loader.
func (s *Service) loadInitialCatalog() error {
	return s.loadModelsCatalog()
}

// syncWithRemote requests a conditional catalog update through the serialized
// loader.
func (s *Service) syncWithRemote() error {
	return s.updateModelsCatalog(false)
}

// hasCustomPricingFiles reports whether a custom supplement path is configured.
// Nil services, missing options, and blank paths return false, while a
// configured path remains watchable when its file is absent.
func (s *Service) hasCustomPricingFiles() bool {
	if s == nil || s.options == nil {
		return false
	}
	return strings.TrimSpace(s.currentOptions().FallbackFile) != ""
}

// customPricingFilesFingerprint hashes the configured supplement's
// length-prefixed content. An unconfigured path returns an empty string;
// missing or unreadable files contribute empty bytes, leaving actual read
// validation to the candidate loader.
func (s *Service) customPricingFilesFingerprint() string {
	if !s.hasCustomPricingFiles() {
		return ""
	}
	h := sha256.New()
	// Fingerprinting treats a failed read as empty content, matching a removed
	// supplement layer. Candidate loading reports read failures when this
	// fingerprint triggers a rebuild.
	body, _ := os.ReadFile(strings.TrimSpace(s.currentOptions().FallbackFile))
	var size [8]byte
	// The big-endian byte count participates in the content fingerprint even
	// for an empty layer. SHA-256 accepts both writes, leaving candidate read
	// errors to file validation.
	binary.BigEndian.PutUint64(size[:], uint64(len(body)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// loadLocalPricingEntries decodes and validates a custom supplement layer.
// Blank paths and deleted files return an empty layer; other read or decode
// failures reject the candidate update using that layer.
func loadLocalPricingEntries(path string) (map[string]json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	// Removing the custom file restores the embedded supplement layer during
	// the next successful rebuild, so absence is accepted as an empty custom
	// layer.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeModelSupplement(body, path)
}

// decodeModelSupplement decodes a supplement and validates every supplied
// pricing field, labeling failures with source. Attribute-only entries can
// remain unpriced; fields are validated even when catalog precedence later
// leaves them unused.
func decodeModelSupplement(body []byte, source string) (map[string]json.RawMessage, error) {
	entries, err := purepricing.DecodeCatalogEntries(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	// Parse diagnostics validate supplied fields even when higher-priority
	// catalog values will win. A missing price set alone remains valid for
	// attribute-only supplement entries.
	_, diagnostics, _ := purepricing.ParsePricingEntries(entries)
	if err := diagnostics.ValidationError(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return entries, nil
}

// reloadIfCustomFilesChanged rebuilds local layers when their content
// fingerprint differs from the last successful publication. A failed rebuild
// logs its error and retains the successful fingerprint so a differing
// candidate remains eligible for later retry.
//
// Fingerprinting represents a failed file read as empty content. If that
// matches the previously successful empty layer, this check sees no change and
// performs no rebuild. Candidate loading distinguishes missing files from
// other read errors when a rebuild is attempted, while the catalog body's
// synchronization hash remains independent of supplement content.
func (s *Service) reloadIfCustomFilesChanged() {
	fingerprint := s.customPricingFilesFingerprint()
	s.mu.RLock()
	unchanged := fingerprint == s.customFilesHash
	s.mu.RUnlock()
	if unchanged {
		return
	}
	if err := s.reloadCustomPricingLayers(); err != nil {
		// Keep the last successful fingerprint when rebuilding fails. A
		// changed candidate will compare different again, allowing subsequent
		// ticks to retry and report its error.
		logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Custom pricing file changed but reload failed: %v", err)
	}
}

// reloadCustomPricingLayers delegates supplement rebuilding to the serialized
// catalog loader.
func (s *Service) reloadCustomPricingLayers() error {
	return s.loadModelsCatalog()
}

// warnLopsidedLongContextLadders logs models with a one-sided context
// surcharge, sorting the supplied slice in place. Logs include the complete
// affected count and at most twenty model names for inspection.
func warnLopsidedLongContextLadders(entries []string) {
	if len(entries) == 0 {
		return
	}
	sort.Strings(entries)
	total := len(entries)
	if total > 20 {
		// Keep the total while limiting the logged name sample.
		entries = append(entries[:20], "...")
	}
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: %d model(s) derive a one-sided long-context ladder (surcharge on only input or only output); base prices and above-tier prices likely come from different price versions: %s", total, strings.Join(entries, ", "))
}

// warnOrphanCacheTierFields logs context cache prices lacking their required
// base price, sorting the supplied slice in place. Logs include the complete
// affected count and at most twenty model names.
func warnOrphanCacheTierFields(entries []string) {
	if len(entries) == 0 {
		return
	}
	sort.Strings(entries)
	total := len(entries)
	if total > 20 {
		// Keep the total while limiting the logged name sample.
		entries = append(entries[:20], "...")
	}
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: %d model(s) carry cache above-tier prices without a base cache price; that cache item bills at $0 until the catalog/supplement supplies the base: %s", total, strings.Join(entries, ", "))
}

// warnDroppedLongContextLadders logs retained model keys whose previously
// declared context tiers disappear in the next price map. A threshold or
// absolute tier list makes the previous record eligible; a nil next value is
// also treated as lacking tiers.
//
// Keys absent from the next map are skipped because this comparison addresses
// a remaining model's lost surcharge rules. The warning reports the complete
// affected count and at most twenty sorted names, giving administrators a
// bounded sample when inspecting catalog or supplement changes.
func warnDroppedLongContextLadders(old, next map[string]*CatalogModelPricing) {
	if len(old) == 0 {
		return
	}
	var dropped []string
	for name, previous := range old {
		if previous == nil || previous.LongContextInputTokenThreshold <= 0 && len(previous.ContextPrices) == 0 {
			continue
		}
		if current, ok := next[name]; ok && (current == nil || current.LongContextInputTokenThreshold <= 0 && len(current.ContextPrices) == 0) {
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	total := len(dropped)
	if total > 20 {
		// Keep the total while limiting the logged name sample.
		dropped = append(dropped[:20], "...")
	}
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: Long-context ladder dropped for %d model(s) after reload: %s (verify catalog/supplement data if unintended)", total, strings.Join(dropped, ", "))
}

// validateCatalogURL applies configured egress checks and returns the
// normalized catalog URL. Enabled allowlisting requires HTTPS and permitted
// hosts; format-only validation uses AllowInsecureHTTP and wraps failures with
// pricing URL context.
func (s *Service) validateCatalogURL(raw string) (string, error) {
	if (s.options != nil) && !s.currentOptions().URLAllowlistEnabled {
		normalized, err := egress.ValidateURLFormat(raw, s.currentOptions().AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid pricing url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.currentOptions().PricingHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.currentOptions().AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid pricing url: %w", err)
	}
	return normalized, nil
}

// GetModelPricing resolves a complete model identity against retained prices
// under the catalog read lock. Returned stored values remain shared and
// read-only; callers needing mutation use PricingView or copy the price.
//
// Recognized provider qualifiers constrain identity candidates, and ambiguous
// or qualified identities lacking a price return an unpriced marker with
// TokenPricingAbsent. Other identities use the pricing query's candidates and
// can return nil when unresolved. Each query acquires one candidate generator
// from the injected factory before looking up its price values.
func (s *Service) GetModelPricing(modelName string) *CatalogModelPricing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	if s.modelCatalog != nil {
		candidates := []string{modelName}
		if query.Candidates != nil {
			candidates = append(candidates, query.Candidates(modelName)...)
		}
		for _, name := range s.modelCatalog.IdentityCandidates(modelName, candidates) {
			if value := s.pricingData[strings.ToLower(strings.TrimSpace(name))]; value != nil {
				return value
			}
		}
		if s.modelCatalog.RequiresExact(modelName) {
			// Qualified or ambiguous names cannot use another source's price.
			// Return an unpriced marker so callers retain that identity's
			// missing token pricing state.
			return &CatalogModelPricing{Source: "unpriced", TokenPricingAbsent: true}
		}
	}
	return query.GetModelPricing(modelName)
}

// GetModelModalities returns copied, pricing-derived modality lists under the
// catalog read lock. The pricing query can derive compatibility modalities
// from mode and support flags; nil services and unresolved prices return nil
// lists.
func (s *Service) GetModelModalities(modelName string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	return query.GetModelModalities(modelName)
}

// GetStatus returns a fresh administrative map describing the retained
// publication and latest candidate outcome. The count covers price entries,
// version uses catalog identity when available, and local_hash contains a
// short content-hash prefix.
func (s *Service) GetStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := s.localHash
	if s.modelCatalog != nil {
		version = s.modelCatalog.Version
	}

	// The full publication version remains available beside the compact source
	// hash prefix, letting status display both supplement identity and a short
	// raw-catalog reference.
	return map[string]any{
		"model_count":  len(s.pricingData),
		"version":      version,
		"last_error":   s.lastCatalogError,
		"last_updated": s.lastUpdated,
		"local_hash":   s.localHash[:min(8, len(s.localHash))],
	}
}

// ForceUpdate requests an unconditional remote candidate or reloads local
// layers when RemoteURL is blank. Remote candidates finish disk replacement
// before publication; local rebuilds can publish from retained bytes without
// another cache write. Failures retain the current catalog and prices.
func (s *Service) ForceUpdate() error {
	return s.updateModelsCatalog(true)
}

// catalogFilePath returns the models.dev cache filename within the configured
// directory.
func (s *Service) catalogFilePath() string {
	return filepath.Join(s.currentOptions().DataDir, "models_dev_catalog_v2.json")
}

// ListModelNamesByProvider returns sorted price-entry names for a normalized,
// case-insensitive provider match. It reads the retained price map under mu
// and expects its stored entries to be nonnil, as supplied by publication.
func (s *Service) ListModelNamesByProvider(provider string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	provider = strings.ToLower(strings.TrimSpace(provider))
	names := make([]string, 0)
	for name, p := range s.pricingData {
		if strings.EqualFold(p.Provider, provider) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Start invokes scheduled synchronization once after initialization and
// dependency binding. Configuration can leave the scheduler inactive; callers
// start before Stop so registered work belongs to the intended lifecycle.
func (s *Service) Start() {
	s.startOnce.Do(s.startUpdateScheduler)
}

// currentOptions returns configuration by value or zero options when missing.
// Referenced host slices and candidate factories remain shared with the stored
// startup configuration and are treated as read-only.
func (s *Service) currentOptions() Options {
	if s.options == nil {
		return Options{}
	}
	return *s.options
}

// Snapshot captures status and independently mutable prices under the read
// lock, including nested tiers, rules, and operation defaults. Its private
// catalogIdentity still references the retained read-only metadata publication
// for query construction.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{BillingDefaults: s.billingDefaults.Clone(), catalogIdentity: s.modelCatalog, LastError: s.lastCatalogError, LastUpdated: s.lastUpdated, LocalHash: s.localHash, CustomFilesHash: s.customFilesHash}
	if s.pricingData != nil {
		out.Data = make(map[string]*CatalogModelPricing, len(s.pricingData))
	}
	for key, value := range s.pricingData {
		if value == nil {
			out.Data[key] = nil
			continue
		}
		out.Data[key] = purepricing.CloneCatalogPrice(value)
	}
	return out
}

// Wait joins registered scheduler work after its loop exits. Calling it while
// an active scheduler has no stop signal waits for that lifecycle to end;
// Stop closes the signal before joining.
func (s *Service) Wait() {
	s.wg.Wait()
}

// catalogQuery acquires one model candidate generator and borrows retained
// pricing entries for the query. Callers hold the service read lock or use
// fixed publication data while the query reads those entries.
func (s *Service) catalogQuery() *purepricing.CatalogQuery {
	options := s.currentOptions()
	var candidates func(string) []string
	if options.ModelLookupCandidates != nil {
		candidates = options.ModelLookupCandidates()
	}
	return &purepricing.CatalogQuery{
		Entries:    s.pricingData,
		Candidates: candidates,
	}
}

// ReadOnlySnapshot constructs a service for one administrative query from
// copied prices and retained metadata identity. Callers use its read methods
// for that capture; update methods are still part of Service.
func (s *Service) ReadOnlySnapshot() *Service {
	return NewServiceFromSnapshot(s.currentOptions(), nil, s.Snapshot())
}
