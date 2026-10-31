package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// dev_source.go overlays explicitly configured development directories on durable
// installation storage. Each directory becomes an ordinary in-memory installation
// with default limits, declared permissions, and a local package location. The
// manager applies the same reconciliation and process capacity rules to both
// sources, keeping development exceptions outside process and HTTP consumers.
//
// Development configuration remains in memory and disappears on host restart.
// Reload rereads the directory while preserving its current configuration under
// the new schema. A development declaration cannot shadow an existing durable
// installation or change its plugin ID during reload. Stored JSON copies keep
// management callers from mutating published records before an explicit save.

// DirectoryPackage contains validated metadata from an unpacked local package.
type DirectoryPackage struct {
	Manifest   *manifest.Manifest // validated local release declaration
	Executable string             // absolute platform executable path
}

// DirectoryReader validates unpacked files without executing their declared code.
type DirectoryReader interface {
	// ReadDirectory returns a validated declaration and its absolute executable path.
	// Static packages have an empty executable. Implementations reject links and
	// missing declared assets under the shared package validation rules.
	ReadDirectory(context.Context, string) (DirectoryPackage, error)
}

// DevOptions selects the explicit directories eligible for in-memory installation.
type DevOptions struct {
	Enabled     bool     // global plugin availability switch
	DevMode     bool     // explicit development mode switch
	Directories []string // configured unpacked package directories
}

// DevSource overlays memory-only installations on the durable installation store.
type DevSource struct {
	options       DevOptions        // copied development configuration inputs
	reader        DirectoryReader   // validated directory inspection capability
	store         InstallationStore // underlying durable installation storage
	configuration *Configuration    // schema and secret configuration boundary
	logger        *slog.Logger      // host diagnostic logging destination
	// mu protects records and directories; records contain serialized copies so each Get or List returns independently mutable installation values.
	mu          sync.RWMutex
	records     map[string][]byte // serialized development installation records
	directories map[string]string // configured directory ownership by ID
}

// NewDevSource creates an unloaded overlay with copied development directory inputs.
// Construction does not read files or start processes. The manager invokes Load
// once before startup reconciliation and Reload under its per-plugin mutation lock.
func NewDevSource(options DevOptions, reader DirectoryReader, store InstallationStore, configuration *Configuration, logger *slog.Logger) *DevSource {
	options.Directories = slices.Clone(options.Directories)
	if logger == nil {
		logger = slog.Default()
	}
	return &DevSource{
		options:       options,
		reader:        reader,
		store:         store,
		configuration: configuration,
		logger:        logger,
		records:       make(map[string][]byte),
		directories:   make(map[string]string),
	}
}

// Load inspects enabled development directories and registers valid in-memory records.
// Invalid or conflicting directories are reported independently. Durable records
// always retain their identity when a development directory declares the same ID.
func (d *DevSource) Load(ctx context.Context) error {
	if !d.options.Enabled || !d.options.DevMode {
		return nil
	}
	for _, directory := range d.options.Directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.loadDirectory(ctx, directory); err != nil {
			d.logger.Warn("Development plugin directory could not be loaded", "directory", directory, "error", err)
		}
	}
	return nil
}

// loadDirectory validates one directory before publishing its ordinary installation record.
// Default configuration may omit required administrator input. The manager records
// that validation failure when enabling; no process is started by this source.
// Durable identity is checked before in-memory publication, and duplicate directory
// identities are rejected under the overlay lock. The source grants only the
// manifest's declared permissions and serializes the complete record before making
// it visible. Loading a later invalid directory leaves earlier accepted records intact.
func (d *DevSource) loadDirectory(ctx context.Context, directory string) error {
	local, err := d.reader.ReadDirectory(ctx, directory)
	if err != nil {
		return err
	}
	if local.Manifest == nil {
		return ErrPackageInvalid
	}
	id := local.Manifest.ID
	if _, err := d.store.Get(ctx, id); err == nil {
		return ErrPluginAlreadyInstalled
	} else if !errors.Is(err, ErrPluginNotFound) {
		return err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	item := &Installation{
		PluginID:        id,
		Enabled:         true,
		LocalDirectory:  absolute,
		VersionSnapshot: VersionSnapshot{Version: local.Manifest.Version, Source: SourceDev, Manifest: local.Manifest, GrantedPermissions: slices.Clone(local.Manifest.Permissions)},
	}
	if err := d.configuration.Set(&item.VersionSnapshot, nil, false); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.records[id]; exists {
		return ErrPluginAlreadyInstalled
	}
	d.records[id] = data
	d.directories[id] = absolute
	return nil
}

// Reload refreshes one development declaration while retaining compatible saved settings.
// Durable installations require no source refresh. The caller holds the manager's
// plugin lock and has already drained its old process before replacing metadata.
// The refreshed declaration must retain the original plugin ID, preserving the
// namespace used by settings and retained business data. Compatible settings move
// through the same secrecy-aware migration as version updates. Failed inspection
// or validation leaves the previous in-memory record available for another repair attempt.
func (d *DevSource) Reload(ctx context.Context, id string) error {
	d.mu.RLock()
	directory := d.directories[id]
	d.mu.RUnlock()
	if directory == "" {
		return nil
	}
	old, err := d.Get(ctx, id)
	if err != nil {
		return err
	}
	local, err := d.reader.ReadDirectory(ctx, directory)
	if err != nil {
		return err
	}
	if local.Manifest == nil || local.Manifest.ID != id {
		return ErrPackageInvalid
	}
	target := VersionSnapshot{Version: local.Manifest.Version, Source: SourceDev, Manifest: local.Manifest, GrantedPermissions: slices.Clone(local.Manifest.Permissions)}
	if err := d.configuration.Migrate(&old.VersionSnapshot, &target, old.Enabled); err != nil {
		return err
	}
	old.VersionSnapshot = target
	return d.Save(ctx, old)
}

// List combines durable and independently decoded development installations in ID order.
func (d *DevSource) List(ctx context.Context) ([]Installation, error) {
	result, err := d.store.List(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, data := range d.records {
		item, err := decodeDevelopment(data)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	slices.SortFunc(result, func(a, b Installation) int { return strings.Compare(a.PluginID, b.PluginID) })
	return result, nil
}

// Get returns a fresh development record or delegates to durable installation storage.
// The returned containers are independent of the overlay's serialized copy. A
// missing development identity follows the underlying store's missing-record contract.
func (d *DevSource) Get(ctx context.Context, id string) (*Installation, error) {
	d.mu.RLock()
	data, ok := d.records[id]
	d.mu.RUnlock()
	if ok {
		return decodeDevelopment(data)
	}
	return d.store.Get(ctx, id)
}

// Save replaces an existing development record or persists an ordinary durable record.
// Development identities must have been registered by Load. Callers hold the
// manager's per-plugin mutation lock, and successful writes publish fresh serialized bytes.
func (d *DevSource) Save(ctx context.Context, item *Installation) error {
	if item.Source != SourceDev {
		return d.store.Save(ctx, item)
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.records[item.PluginID]; !ok {
		return ErrPluginNotFound
	}
	d.records[item.PluginID] = data
	return nil
}

// Delete removes development intent after optional durable KV cleanup succeeds.
// The caller drains executable and callback work first. Durable deletion also
// handles retained KV for development IDs while always preserving ledger history.
// The overlay is removed only after durable cleanup succeeds, so a database
// failure leaves the development record available for retry. Removing a record
// also forgets its configured reload location for this host lifetime. A subsequent
// host startup can rediscover that configured directory as a fresh development record.
func (d *DevSource) Delete(ctx context.Context, id string, purge bool) error {
	if err := d.store.Delete(ctx, id, purge); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.records, id)
	delete(d.directories, id)
	d.mu.Unlock()
	return nil
}

// decodeDevelopment reconstructs independent configuration values while preserving JSON numbers.
func decodeDevelopment(data []byte) (*Installation, error) {
	var item Installation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&item); err != nil {
		return nil, err
	}
	return &item, nil
}
