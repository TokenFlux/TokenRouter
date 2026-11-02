package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// catalog_service.go coordinates accepted index caches and source-qualified release reads.
// Empty caches are loaded before the initial market result, while an expired
// accepted cache remains available during background refresh. Source transfer
// failures are recorded per index and cannot replace previously accepted release
// data or change an installation's recorded distribution authority.
//
// Refresh work belongs to the service lifetime rather than a single HTTP waiter.
// Callers share one active refresh per source, and source mutation locks keep
// configuration changes or deletion ordered against cache publication. Shutdown
// seals admission before canceling and waiting for actual worker completion.
// Release resolution returns owned containers suitable for the manager's later
// compatibility, permission, and persistence checks under its plugin lock.

const (
	// Accepted catalog documents become eligible for background refresh after the
	// planned one-hour cache lifetime; explicit refresh always bypasses this age check.
	catalogCacheLifetime = time.Hour
)

// CatalogOptions fixes host compatibility and the configured built-in source location.
type CatalogOptions struct {
	Enabled     bool              // global plugin distribution availability
	OfficialURL string            // configured built-in catalog HTTPS URL
	Host        manifest.HostInfo // immutable host compatibility capabilities
	// Now defaults to time.Now and supplies cache-age observations; it does not
	// alter publisher timestamps or the database's source modification timestamps.
	Now func() time.Time
}

// CatalogService owns source coordination and its cancellable background refresh lifetime.
type CatalogService struct {
	options       CatalogOptions     // copied compatibility and clock options
	indexes       IndexStore         // durable sources and accepted caches
	installations InstallationStore  // accepted source ownership for plugin IDs
	fetcher       IndexFetcher       // bounded signature-verifying transfer port
	logger        *slog.Logger       // source refresh diagnostic destination
	ctx           context.Context    // service-owned worker cancellation context
	cancel        context.CancelFunc // interrupts admitted refresh workers
	// mu protects stopped, gates, flights, and stopDone. Worker registration happens
	// before releasing mu so Stop cannot begin waiting before an admitted Add.
	mu       sync.Mutex              // coordination state publication lock
	stopped  bool                    // terminal refresh admission state
	gates    map[int64]chan struct{} // per-source mutation ownership tokens
	flights  map[int64]*indexRefresh // shared active source refreshes
	stopDone chan struct{}           // actual worker completion notification
	workers  sync.WaitGroup          // admitted refresh worker ownership
}

// indexRefresh publishes one shared refresh outcome after its owned source write finishes.
type indexRefresh struct {
	// done closes only after the worker returns from source persistence, publishing
	// err to every waiter that shared this attempt.
	done chan struct{}
	// err is written before done closes and read only after that close, so waiting
	// callers observe the completed outcome without retaining the coordination lock.
	err error
}

// NewCatalogService creates source coordination without reading files or starting network work.
// Caller-provided capability slices are copied. The owner must call Stop before
// releasing storage, even when initialization or an administrative operation fails.
func NewCatalogService(options CatalogOptions, indexes IndexStore, installations InstallationStore, fetcher IndexFetcher, logger *slog.Logger) *CatalogService {
	if options.Now == nil {
		options.Now = time.Now
	}
	options.Host.APIVersions = slices.Clone(options.Host.APIVersions)
	options.Host.Permissions = slices.Clone(options.Host.Permissions)
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CatalogService{
		options: options, indexes: indexes, installations: installations, fetcher: fetcher, logger: logger,
		ctx: ctx, cancel: cancel, gates: make(map[int64]chan struct{}), flights: make(map[int64]*indexRefresh),
	}
}

// Start ensures the configured builtin source before ordinary market access begins.
// Globally disabled distributions perform no initialization writes. An omitted
// official URL leaves explicitly registered sources available without inventing a location.
func (c *CatalogService) Start(ctx context.Context) error {
	if !c.options.Enabled || c.options.OfficialURL == "" {
		return nil
	}
	return c.ensureBuiltin(ctx)
}

// Stop closes refresh admission, cancels transport, and waits for owned workers to return.
// A caller deadline can interrupt waiting without reopening admission. Later Stop
// calls wait on the same completion channel and never create another shutdown owner.
//
// Worker registration and the stopped transition share mu, so no WaitGroup Add
// can race after waiting begins. Cancellation is signaled only after that seal.
// The final completion channel closes after refresh functions and their source
// persistence paths have actually returned, not merely when a context was canceled.
func (c *CatalogService) Stop(ctx context.Context) error {
	// Seal worker admission under the same lock used by registration before waiting,
	// so shutdown cannot miss a refresh that has already been accepted.
	c.mu.Lock()
	if !c.stopped {
		c.stopped = true
		c.cancel()
		c.stopDone = make(chan struct{})
		go func() {
			c.workers.Wait()
			close(c.stopDone)
		}()
	}
	done := c.stopDone
	c.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// List returns source-qualified market entries using current installation ownership.
// First reads wait for absent caches; expired accepted caches refresh in the
// background. Per-source transfer failures retain old data and remain visible through ListIndexes.
//
// Installation ownership is read once for the response, keeping duplicate identifiers
// associated with their accepted source across the combined result. A failed first
// transfer contributes no entries, while independent sources remain usable. Caller
// cancellation stops collection rather than returning an incomplete result that looks
// successful, and background workers retain their separate service-owned lifetime.
func (c *CatalogService) List(ctx context.Context) ([]CatalogEntry, error) {
	sources, err := c.indexes.ListIndexes(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := c.installations.List(ctx)
	if err != nil {
		return nil, err
	}
	ownership := make(map[string]Installation, len(installed))
	for _, item := range installed {
		ownership[item.PluginID] = item
	}
	result := make([]CatalogEntry, 0)
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		current, document, err := c.readDocument(ctx, source)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// A failed source cannot hide valid entries from independent indexes;
			// its retained diagnostic is exposed through the source-management response.
			continue
		}
		result = append(result, selectCatalog(current, document, ownership, c.options.Host)...)
	}
	return sortedCatalog(result), nil
}

// Get returns one source-qualified plugin including its retained version decisions.
// Delisted entries remain available only to their own installed source. Missing
// sources or entries return the ordinary not-found result without crossing source authority.
func (c *CatalogService) Get(ctx context.Context, indexID int64, pluginID string) (*CatalogEntry, error) {
	source, err := c.indexes.GetIndex(ctx, indexID)
	if err != nil {
		return nil, err
	}
	current, document, err := c.readDocument(ctx, *source)
	if err != nil {
		return nil, err
	}
	ownership := make(map[string]Installation)
	installed, err := c.installations.Get(ctx, pluginID)
	if err == nil {
		ownership[pluginID] = *installed
	} else if !errors.Is(err, ErrPluginNotFound) {
		return nil, err
	}
	for _, entry := range selectCatalog(current, document, ownership, c.options.Host) {
		if entry.ID == pluginID {
			return &entry, nil
		}
	}
	return nil, ErrPluginNotFound
}

// Resolve selects one explicitly reviewed indexed release and returns owned installation metadata.
// Exact version choice is mandatory; a changing recommendation never substitutes
// another release. Explicit input options waive host version bounds and existing
// ownership independently; disabled sources, withdrawn releases, and unsupported
// host capabilities remain unavailable. The manager strictly validates the selected
// declaration and permission acceptance under its installation lock before execution.
func (c *CatalogService) Resolve(ctx context.Context, input InstallInput) (VersionSnapshot, error) {
	if !c.options.Enabled {
		return VersionSnapshot{}, ErrPluginsDisabled
	}
	indexID, pluginID, version := input.IndexID, input.PluginID, input.Version
	if indexID <= 0 || !manifest.ValidID(pluginID) || !manifest.ValidVersion(version) {
		return VersionSnapshot{}, ErrIndexInvalid
	}
	entry, err := c.Get(ctx, indexID, pluginID)
	if err != nil {
		return VersionSnapshot{}, err
	}
	if entry.InstalledFromOther && !input.Force {
		return VersionSnapshot{}, ErrPluginAlreadyInstalled
	}
	for _, release := range entry.Versions {
		if release.Version.Version != version {
			continue
		}
		if issues := blockingIncompatibilities(release.Incompatibilities, input.IgnoreHostVersion); len(issues) > 0 {
			data, _ := json.Marshal(issues)
			return VersionSnapshot{}, ErrPluginIncompatible.WithMetadata(map[string]string{"incompatibilities": string(data)})
		}
		if entry.Delisted || release.Yanked {
			return VersionSnapshot{}, ErrIndexInvalid
		}
		return VersionSnapshot{
			Version: version, Source: SourceIndex, IndexID: &indexID,
			Manifest: release.Manifest, ManifestJSON: bytes.Clone(release.ManifestJSON), ManifestSHA256: release.ManifestSHA256,
			Artifacts: maps.Clone(release.Artifacts),
		}, nil
	}
	return VersionSnapshot{}, ErrPluginNotFound
}

// readDocument obtains one accepted cache and applies host-aware declaration parsing.
// Empty enabled caches receive a shared initial refresh. Existing expired caches
// stay readable while refresh runs, and disabled distributions never start network work.
//
// An initial transfer is followed by a fresh source read because its publication
// may also have changed diagnostics or authority. Existing bytes can be served
// immediately without waiting on that transfer. Host-aware parsing still runs for
// every returned document, preserving compatibility checks after a host upgrade
// without treating a stored signature classification as a new verification operation.
func (c *CatalogService) readDocument(ctx context.Context, source IndexSource) (IndexSource, *index.Document, error) {
	if !source.Enabled {
		return source, nil, ErrIndexNotFound
	}
	if len(source.CachedDocument) == 0 && c.options.Enabled {
		if err := c.Refresh(ctx, source.ID); err != nil {
			return source, nil, err
		}
		updated, err := c.indexes.GetIndex(ctx, source.ID)
		if err != nil {
			return source, nil, err
		}
		source = *updated
	} else if c.options.Enabled && (source.CachedAt == nil || c.options.Now().Sub(*source.CachedAt) >= catalogCacheLifetime) {
		// Reuse the accepted cache immediately; source-owned background work records
		// success or failure for later reads without extending this caller's latency.
		_, _ = c.beginRefresh(source.ID)
	}
	if len(source.CachedDocument) == 0 {
		return source, nil, ErrIndexInvalid
	}
	document, err := index.ParseForHost(source.CachedDocument, c.options.Host.Version)
	if err != nil {
		return source, nil, ErrIndexInvalid.WithCause(err)
	}
	return source, document, nil
}

// InstalledEntries returns cached source annotations without delaying process-status polling on downloads.
// Only the accepted source can supply an update or withdrawal warning. A missing
// or malformed cache leaves installation inspection available and carries source diagnostics separately.
//
// Registration metadata remains visible for disabled sources, but those sources
// supply no update recommendation. Cached release selection uses the same ownership
// rules as the market, so a competing index cannot attach an update button to
// another source's installation. Missing registrations retain their installation
// records without inventing replacement authority or initiating a recovery download.
func (c *CatalogService) InstalledEntries(ctx context.Context, items []Installation) (map[string]CatalogEntry, error) {
	sources, err := c.indexes.ListIndexes(ctx)
	if err != nil {
		return nil, err
	}
	ownership := make(map[string]Installation, len(items))
	for _, item := range items {
		ownership[item.PluginID] = item
	}
	result := make(map[string]CatalogEntry)
	for _, source := range sources {
		for _, item := range items {
			if item.Source == SourceIndex && item.IndexID != nil && *item.IndexID == source.ID {
				result[item.PluginID] = CatalogEntry{IndexID: source.ID, IndexName: source.Name, Builtin: source.Builtin, SignatureStatus: source.SignatureStatus}
			}
		}
		if !source.Enabled || len(source.CachedDocument) == 0 {
			continue
		}
		document, err := index.ParseForHost(source.CachedDocument, c.options.Host.Version)
		if err != nil {
			// Installation status remains inspectable when cached metadata cannot be
			// parsed; source diagnostics and explicit refresh provide the recovery path.
			continue
		}
		for _, entry := range selectCatalog(source, document, ownership, c.options.Host) {
			if entry.InstalledFromSource {
				result[entry.ID] = entry
			}
		}
	}
	return result, nil
}
