package plugin

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// dev_source.go loads explicitly enabled development directories into the local
// runtime without writing installation records. Development policy is owned
// here: declared permissions are granted, executable verification is bypassed,
// configuration starts from manifest defaults, and native processes count toward
// the same deterministic per-instance maximum. Directory inspection remains an
// injected filesystem capability so the core does not unpack or download files.
//
// Loading runs in one owned background task after lifecycle startup. Stop closes
// admission, cancels that task, and stops the process collection, preventing a
// slow directory read from publishing a late generation after shutdown. Invalid
// directories are logged independently so one broken development plugin cannot
// prevent the host's ordinary HTTP services from becoming available.

// DirectoryPackage contains validated metadata from an unpacked local package.
type DirectoryPackage struct {
	Manifest   *manifest.Manifest // validated local release declaration
	Executable string             // absolute platform executable path
}

// DirectoryReader validates unpacked files without executing their declared code.
type DirectoryReader interface {
	// ReadDirectory returns a validated declaration and its absolute executable path.
	// Static packages have an empty executable. Implementations must reject links
	// and missing declared assets under the shared package validation rules.
	ReadDirectory(context.Context, string) (DirectoryPackage, error)
}

// DevOptions identifies the explicitly permitted development directories and process cap.
type DevOptions struct {
	Enabled     bool     // global plugin availability switch
	DevMode     bool     // explicit development mode switch
	Directories []string // configured unpacked package directories
	StateRoot   string   // disposable plugin state root
	MaxRunning  int      // native process admission maximum
	HostVersion string   // host release build version
}

// DevSource owns asynchronous directory loading and its process shutdown dependency.
type DevSource struct {
	options   DevOptions      // copied development configuration inputs
	reader    DirectoryReader // validated directory inspection capability
	processes Supervisors     // owned native process collection
	logger    *slog.Logger    // host diagnostic logger
	// mu orders startup against terminal stop; background completion is published by done after the loading task returns.
	mu      sync.Mutex
	started bool               // loading task started flag
	stopped bool               // terminal source stop flag
	cancel  context.CancelFunc // background loading cancellation signal
	done    chan struct{}      // completed directory loading signal
}

// NewDevSource creates a dormant development source with copied directory inputs.
// The caller supplies filesystem inspection and process ownership. No directory
// is read and no subprocess starts until lifecycle calls Start.
func NewDevSource(options DevOptions, reader DirectoryReader, processes Supervisors, logger *slog.Logger) *DevSource {
	options.Directories = append([]string(nil), options.Directories...)
	if logger == nil {
		logger = slog.Default()
	}
	return &DevSource{options: options, reader: reader, processes: processes, logger: logger}
}

// Start launches directory loading once without blocking ordinary host availability.
// Both enable switches must be set. The loading lifetime is independent of
// the startup callback context and ends through the source's Stop method.
func (d *DevSource) Start(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started || d.stopped || !d.options.Enabled || !d.options.DevMode {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done, d.started = cancel, make(chan struct{}), true
	go func() { defer close(d.done); d.load(ctx) }()
	return nil
}

// load prepares all declarations before admitting native processes in ID order.
// Invalid directories and duplicate IDs are skipped with diagnostics. Static
// packages consume no process slots, and configuration errors prevent execution.
//
// Permission grants and checksum bypass are assigned only along this explicitly
// enabled development path. Sorting before admission makes the process cap
// independent of filesystem traversal or configured directory order. Cancellation
// is checked before each replacement, while the collection's terminal shutdown
// gate independently prevents a late start racing with source cleanup.
func (d *DevSource) load(ctx context.Context) {
	var specs []ProcessSpec
	seen := make(map[string]bool)
	for _, directory := range d.options.Directories {
		if ctx.Err() != nil {
			return
		}
		local, err := d.reader.ReadDirectory(ctx, directory)
		if err != nil {
			d.logger.Warn("Development plugin directory failed validation", "directory", directory, "error", err)
			continue
		}
		m := local.Manifest
		platform := runtime.GOOS + "/" + runtime.GOARCH
		host := manifest.HostInfo{Version: d.options.HostVersion, APIVersions: []int{1}, Platform: platform, Permissions: manifest.Permissions()}
		if incompatible := manifest.CheckCompatibility(m, []string{platform}, host); len(incompatible) > 0 {
			d.logger.Warn("Development plugin is incompatible", "plugin_id", m.ID, "incompatibilities", incompatible)
			continue
		}
		if seen[m.ID] {
			d.logger.Warn("Duplicate development plugin ID", "plugin_id", m.ID)
			continue
		}
		seen[m.ID] = true
		if m.Backend == nil {
			continue
		}
		values, err := m.Config.Normalize(nil)
		if err != nil {
			d.logger.Warn("Development plugin configuration is incomplete", "plugin_id", m.ID, "error", err)
			continue
		}
		configJSON, err := json.Marshal(values)
		if err != nil {
			d.logger.Warn("Development plugin configuration encoding failed", "plugin_id", m.ID, "error", err)
			continue
		}
		stateDir, err := filepath.Abs(filepath.Join(d.options.StateRoot, m.ID))
		if err != nil {
			d.logger.Warn("Development plugin state path is invalid", "plugin_id", m.ID, "error", err)
			continue
		}
		specs = append(specs, ProcessSpec{PluginID: m.ID, Version: m.Version, Executable: local.Executable, StateDir: stateDir, ConfigJSON: configJSON, Permissions: m.Permissions, MemoryMiB: m.Resources.MemoryLimit(), VerifyBinary: false})
	}
	slices.SortFunc(specs, func(a, b ProcessSpec) int { return strings.Compare(a.PluginID, b.PluginID) })
	for i, spec := range specs {
		if ctx.Err() != nil {
			return
		}
		if i >= d.options.MaxRunning {
			d.logger.Warn("Development plugin exceeds process limit", "plugin_id", spec.PluginID, "reason", "MAX_RUNNING_EXCEEDED")
			continue
		}
		if err := d.processes.Replace(ctx, spec); err != nil {
			d.logger.Warn("Development plugin startup failed", "plugin_id", spec.PluginID, "error", err)
		}
	}
}

// Stop prevents late loading and stops all processes within the caller's budget.
// It is safe before Start and on repeated calls. Cancellation reports incomplete
// loading while the process collection still receives terminal shutdown immediately.
func (d *DevSource) Stop(ctx context.Context) error {
	d.mu.Lock()
	d.stopped = true
	if d.cancel != nil {
		d.cancel()
	}
	done := d.done
	d.mu.Unlock()
	err := d.processes.StopAll(ctx)
	if done == nil {
		return err
	}
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
