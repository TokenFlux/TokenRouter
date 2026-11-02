package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// manager.go reconciles durable installation intent with verified files and
// process ownership. One lock per plugin covers both management persistence and
// reconciliation, while a separate publication lock protects immutable loaded
// snapshots and process-slot reservations. HTTP consumers never wait for package
// downloads or administrative mutation locks before observing availability.
//
// Desired availability can differ from actual runtime state after failed startup
// or package recovery. Such failures retain installation records and diagnostics
// for operator retry. Removal closes request admission and drains native requests
// and host callbacks before deleting state. Startup uses the same reconciliation
// path as administrative changes, admitting native processes in deterministic ID
// order and applying one shared process limit to development and durable records.

// ManagerOptions defines host-wide plugin availability and local process policy.
type ManagerOptions struct {
	Enabled     bool   // global plugin availability switch
	MaxRunning  int    // native process reservation maximum
	HostVersion string // host release compatibility version
	StateRoot   string // disposable process state directory
}

// Loaded publishes a coherent installation and verified package to HTTP consumers.
type Loaded struct {
	// Installation and Package contain immutable maps after publication; callers must never modify their nested values or configuration containers.
	Installation *Installation // accepted installation intent snapshot
	Package      Package       // verified local release assets
}

// Manager owns local reconciliation, published snapshots, and serialized installation changes.
// @project-doc docs/architecture/plugin_system.md#installation_state
type Manager struct {
	options       ManagerOptions    // host plugin execution policy
	store         InstallationStore // durable and development installation source
	packages      Packages          // verified local archive storage
	processes     Supervisors       // child generation lifecycle owner
	configuration *Configuration    // schema and encrypted settings boundary
	logger        *slog.Logger      // host diagnostic logging destination
	tasks         *TaskScheduler    // declared task scheduling and recent outcomes
	// mu protects locks, loaded, observations, blocked, slots, and lifecycle state. Per-plugin mutation channels serialize changes without blocking unrelated snapshot readers.
	mu           sync.RWMutex
	locks        map[string]chan struct{} // plugin ID to admission channel
	loaded       map[string]Loaded        // published immutable installation views
	observations map[string]Status        // management failure and static states
	blocked      map[string]bool          // request admission suspension flags
	slots        map[string]bool          // native process capacity reservations
	started      bool                     // lifecycle startup registration flag
	stopped      bool                     // terminal lifecycle shutdown flag
	cancel       context.CancelFunc       // background worker cancellation signal
	done         chan struct{}            // background worker completion signal
	// sourceReady closes after development-source registration, ordering initial identity discovery before any administrative mutation can create a colliding durable record.
	sourceReady chan struct{}
}

// NewManager constructs a dormant reconciler with no filesystem or process effects.
// Installation sources and configuration are shared dependencies. Start loads records
// asynchronously; callers can inspect empty snapshots before initialization completes.
func NewManager(options ManagerOptions, store InstallationStore, packages Packages, processes Supervisors, configuration *Configuration, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	manager := &Manager{
		options:       options,
		store:         store,
		packages:      packages,
		processes:     processes,
		configuration: configuration,
		logger:        logger,
		locks:         make(map[string]chan struct{}),
		loaded:        make(map[string]Loaded),
		observations:  make(map[string]Status),
		blocked:       make(map[string]bool),
		slots:         make(map[string]bool),
	}
	manager.tasks = NewTaskScheduler(manager, nil, logger)
	return manager
}

// Start schedules initial reconciliation without delaying the host's HTTP startup.
// The owned background context survives the lifecycle callback. Optional development
// records load before the same ordered SyncAll pass used for durable installations.
// Task scheduling starts before reconciliation registers declarations. Upload
// cleanup then shares the loader worker's lifetime. Start records cancellation
// and completion ownership before releasing its lock, so concurrent Stop can
// prevent late process reservations even when a slow directory inspection or
// database lookup has not returned. Repeated starts never create extra workers.
func (m *Manager) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.done, m.started = cancel, make(chan struct{}), true
	m.sourceReady = make(chan struct{})
	if err := m.tasks.Start(ctx); err != nil {
		cancel()
		return err
	}
	// Publish lifecycle ownership before starting the loader so Stop can cancel it and prevent any late generation from acquiring capacity.
	go func() {
		defer close(m.done)
		if source, ok := m.store.(interface{ Load(context.Context) error }); ok {
			if err := source.Load(ctx); err != nil {
				close(m.sourceReady)
				m.logger.Error("Plugin installation source failed to load", "error", err)
				return
			}
		}
		close(m.sourceReady)
		m.SyncAll(ctx)
		if cleanup, ok := m.packages.(interface{ CleanupUploads(context.Context) error }); ok {
			// Upload expiration is enforced at lookup; periodic cleanup reclaims only stale staging files while the lifecycle context keeps ownership explicit.
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := cleanup.CleanupUploads(ctx); err != nil && ctx.Err() == nil {
						m.logger.Warn("Expired plugin uploads could not be removed", "error", err)
					}
				}
			}
		}
	}()
	return nil
}

// Stop closes manager admission and drains all child generations within the caller's budget.
// Initialization is canceled first, preventing late replacements. Repeated calls
// reuse supervisor cleanup and report any work still outstanding at the deadline.
// The terminal flag prevents administrative reconciliation from reserving another
// generation while shutdown waits. The supervisor owns actual child termination
// and callback completion, while this method also waits for the initialization
// task scheduler and loader maintenance worker; expiration reports all
// outstanding shutdown causes.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.stopped = true
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
	taskErr := m.tasks.Stop(ctx)
	err := errors.Join(taskErr, m.processes.StopAll(ctx))
	if done == nil {
		return err
	}
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

// locked serializes one plugin's complete management operation with cancellation-aware waiting.
// Lock entries live for the manager lifetime, avoiding replacement while queued
// requests still reference them. The callback runs only after context validation.
// A canceled waiter never enters the mutation, and cancellation that races with
// channel admission is checked again before invoking the callback. The global
// publication lock protects only lock lookup and creation; downloads, process
// draining, and database mutations run under this plugin's channel alone.
func (m *Manager) locked(ctx context.Context, id string, operation func() error) error {
	if !manifest.ValidID(id) {
		return ErrPluginNotFound
	}
	m.mu.RLock()
	ready := m.sourceReady
	m.mu.RUnlock()
	if ready != nil {
		// Startup discovers development identities before mutations can create durable records, preventing a late overlay publication from shadowing an administrative installation.
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	lock := m.locks[id]
	if lock == nil {
		lock = make(chan struct{}, 1)
		m.locks[id] = lock
	}
	m.mu.Unlock()
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}

// Sync reconciles one installation while excluding concurrent changes to its intent.
// Missing records drain and remove local state. A failed reconciliation preserves
// its installation record and publishes a diagnostic state for administration.
func (m *Manager) Sync(ctx context.Context, id string) error {
	return m.locked(ctx, id, func() error { return m.syncLocked(ctx, id) })
}

// syncLocked applies one coherent desired record to package and process ownership.
// Callers hold the plugin's mutation lock. Unchanged running versions retain their
// verified package, while the supervisor compares desired configuration against
// values actually applied to its generation and retries unfinished notifications.
//
// Replacements drain the old generation before touching its files. The native
// process slot stays reserved until successful draining, so concurrent installs
// cannot exceed the host maximum while an older callback is still finishing.
// Publication blocks HTTP admission during replacement, then exposes the new
// snapshot before process startup so broker callbacks can resolve accepted policy.
// Retained versions protect an update's original rollback history until readiness is decided.
func (m *Manager) syncLocked(ctx context.Context, id string, retain ...PackageRef) (resultErr error) {
	defer func() {
		if resultErr != nil {
			m.recordFailure(id, resultErr)
		}
	}()
	want, err := m.store.Get(ctx, id)
	if errors.Is(err, ErrPluginNotFound) {
		if err := m.drainLocked(ctx, id); err != nil {
			return err
		}
		if err := m.packages.Remove(ctx, id); err != nil {
			return err
		}
		m.mu.Lock()
		delete(m.loaded, id)
		delete(m.observations, id)
		delete(m.blocked, id)
		m.mu.Unlock()
		m.tasks.Remove(id)
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.validate(want); err != nil {
		return errors.Join(err, m.drainLocked(ctx, id))
	}
	old, found := m.Snapshot(id)
	same := found && sameRelease(old.Installation, want)
	var local Package
	if same {
		local = old.Package
	} else {
		if err := m.drainLocked(ctx, id); err != nil {
			return err
		}
		local, err = m.packages.Ensure(ctx, packageReference(want, ""))
		if err != nil {
			return err
		}
	}
	loaded := Loaded{Installation: want, Package: local}
	var values map[string]any
	if want.Enabled && m.options.Enabled {
		values, err = m.configuration.Plain(&want.VersionSnapshot)
		if err != nil {
			return errors.Join(err, m.drainLocked(ctx, id))
		}
	}
	if !want.Enabled || !m.options.Enabled || want.Manifest.Backend == nil {
		if err := m.drainLocked(ctx, id); err != nil {
			return err
		}
		state := StateStopped
		if want.Enabled && m.options.Enabled && want.Manifest.Backend == nil {
			state = StateStatic
		}
		if err := m.publish(id, loaded, Status{State: state, Since: time.Now()}); err != nil {
			return err
		}
		return m.prune(ctx, want, retain...)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	stateDir, err := filepath.Abs(filepath.Join(m.options.StateRoot, id))
	if err != nil {
		return err
	}
	spec := ProcessSpec{
		PluginID:     id,
		Version:      want.Version,
		Executable:   local.Executable,
		BinarySHA256: local.BinarySHA256,
		StateDir:     stateDir,
		ConfigJSON:   data,
		Permissions:  slices.Clone(want.GrantedPermissions),
		MemoryMiB:    want.Manifest.Resources.MemoryLimit(),
		VerifyBinary: want.LocalDirectory == "",
	}
	if want.Previous != nil {
		spec.PreviousVersion = want.Previous.Version
	}
	if err := m.reserve(id); err != nil {
		return err
	}
	state := m.processes.Status(id).State
	needsStart := !same || state == StateStopped || state == StateFailed || state == StateDraining
	if err := m.publish(id, loaded, Status{}); err != nil {
		return err
	}
	if needsStart {
		err = m.processes.Replace(ctx, spec)
	} else {
		err = m.processes.ApplyConfig(ctx, spec)
	}
	if err != nil {
		return err
	}
	return m.prune(ctx, want, retain...)
}

// validate rejects unsupported releases before strictly parsing their complete declaration.
// Stored unknown fields remain visible as incompatible records. Compatible releases
// must still pass the current manifest schema before reaching package execution.
func (m *Manager) validate(item *Installation) error {
	if item.Manifest == nil || item.Manifest.ID != item.PluginID || item.Manifest.Version != item.Version {
		return ErrPackageInvalid
	}
	platforms := make([]string, 0, len(item.Artifacts))
	for platform := range item.Artifacts {
		platforms = append(platforms, platform)
	}
	if item.LocalDirectory != "" {
		platforms = []string{runtime.GOOS + "/" + runtime.GOARCH, "any"}
	}
	host := manifest.HostInfo{Version: m.options.HostVersion, APIVersions: []int{1}, Platform: runtime.GOOS + "/" + runtime.GOARCH, Permissions: manifest.Permissions()}
	if issues := blockingIncompatibilities(manifest.CheckCompatibility(item.Manifest, platforms, host), item.IgnoreHostVersion); len(issues) > 0 {
		data, _ := json.Marshal(issues)
		return ErrPluginIncompatible.WithMetadata(map[string]string{"incompatibilities": string(data)})
	}
	if len(item.ManifestJSON) > 0 {
		parsed, err := manifest.Parse(item.ManifestJSON)
		if err != nil {
			return ErrPackageInvalid.WithCause(err)
		}
		item.Manifest = parsed
	} else if err := item.Manifest.Validate(); err != nil {
		return ErrPackageInvalid.WithCause(err)
	}
	if item.Manifest.ID != item.PluginID || item.Manifest.Version != item.Version {
		return ErrPackageInvalid
	}
	if !acceptedPermissions(item.Manifest.Permissions, item.GrantedPermissions) {
		data, _ := json.Marshal(item.Manifest.Permissions)
		return ErrPermissionsNotAccepted.WithMetadata(map[string]string{"permissions": string(data)})
	}
	_, err := ParseLimits(item.Limits)
	return err
}

// blockingIncompatibilities retains every unsupported capability except explicitly waived host bounds.
// The override applies only to minimum and maximum gateway versions. Manifest
// parsing, API support, platform availability, and permission checks remain mandatory.
func blockingIncompatibilities(issues []manifest.Incompatibility, ignoreHostVersion bool) []manifest.Incompatibility {
	if !ignoreHostVersion {
		return issues
	}
	// Filter only named version reasons so future compatibility checks remain enforced
	// without relying on a mutable or fabricated host version during reconciliation.
	return slices.DeleteFunc(slices.Clone(issues), func(issue manifest.Incompatibility) bool {
		return issue.Code == "HOST_TOO_OLD" || issue.Code == "HOST_TOO_NEW"
	})
}

// sameRelease compares executable identity and granted policy across immutable snapshots.
func sameRelease(a, b *Installation) bool {
	left, right := packageReference(a, ""), packageReference(b, "")
	return left == right && slices.Equal(a.GrantedPermissions, b.GrantedPermissions)
}

// packageReference selects the platform artifact and temporary upload handle for storage.
// The installation must contain a validated declaration. Static contributions use
// any-platform artifacts when available, otherwise the host-specific archive.
// Native contributions always require the host's exact target.
func packageReference(item *Installation, token string) PackageRef {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if item.Manifest.Backend == nil {
		if _, shared := item.Artifacts["any"]; shared {
			platform = "any"
		}
	}
	return PackageRef{
		PluginID:       item.PluginID,
		Version:        item.Version,
		Icon:           item.Manifest.Icon,
		ManifestSHA256: item.ManifestSHA256,
		Artifact:       item.Artifacts[platform],
		Platform:       platform,
		Source:         item.Source,
		UploadToken:    token,
		LocalDirectory: item.LocalDirectory,
	}
}

// reserve atomically admits this plugin under the host-wide native process maximum.
// Existing reservations are reused. A terminal shutdown or globally disabled host
// rejects admission before a caller can publish or launch another child generation.
// Slot counting and terminal-state inspection happen under one lock, so two
// administrators installing different IDs cannot both claim the last slot.
// A reservation survives startup failure and remains assigned until draining
// confirms callback completion, matching the configured set of enabled owners.
func (m *Manager) reserve(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || !m.options.Enabled {
		return ErrPluginsDisabled
	}
	if !m.slots[id] && len(m.slots) >= m.options.MaxRunning {
		return ErrMaxRunning
	}
	m.slots[id] = true
	return nil
}

// drainLocked rejects new requests and waits for native work and host callbacks.
// Callers hold the plugin's mutation lock. Failure leaves admission blocked and
// retains the capacity reservation, installation, and policy for a later retry.
// Admission is closed before entering supervisor shutdown, including the interval
// before that supervisor changes its own state. Capacity becomes reusable only
// when Stop confirms all admitted work has returned, so an unsuccessful purge
// attempt cannot race a replacement generation against an old host callback.
func (m *Manager) drainLocked(ctx context.Context, id string) error {
	m.mu.Lock()
	m.blocked[id] = true
	m.mu.Unlock()
	m.tasks.Suspend(id)
	if err := m.processes.Stop(ctx, id); err != nil {
		return err
	}
	// Capacity is reusable only after the supervisor confirms actual callback completion, preventing a purged namespace from receiving late writes.
	m.mu.Lock()
	delete(m.slots, id)
	m.mu.Unlock()
	return nil
}

// publish installs a coherent immutable view and its administrative observation.
// The caller holds the plugin mutation lock and has verified admission eligibility.
// All containers belong to the published snapshot and must remain immutable afterward.
//
// Publication precedes schedule registration so newly dispatched jobs can acquire
// the accepted release through ordinary manager admission. A previous schedule
// racing this change compares its expected version with the acquired lease and
// skips a different generation. Disabled or static installations suspend future
// tasks while retaining recent results; unexpected registration errors propagate
// to reconciliation's existing failure handling and close admission again.
func (m *Manager) publish(id string, loaded Loaded, status Status) error {
	m.mu.Lock()
	m.loaded[id] = loaded
	m.observations[id] = status
	m.blocked[id] = false
	m.mu.Unlock()
	item := loaded.Installation
	if !m.options.Enabled || !item.Enabled || item.Manifest.Backend == nil {
		m.tasks.Suspend(id)
		return nil
	}
	return m.tasks.Replace(id, item.Version, item.Manifest.Backend.Tasks)
}

// recordFailure retains the manager error while closing new plugin request admission.
// It preserves the last loaded policy and files for outstanding callback readers.
// Task suspension also cancels active scheduled calls. A later successful
// reconciliation replaces this observation, registers future tasks, and reopens admission.
func (m *Manager) recordFailure(id string, err error) {
	m.mu.Lock()
	m.observations[id] = Status{State: StateFailed, LastError: err.Error(), Since: time.Now()}
	m.blocked[id] = true
	m.mu.Unlock()
	m.tasks.Suspend(id)
}

// prune retains current and previous packages after a successful reconciliation.
// The caller holds the plugin mutation lock and has stopped any displaced version.
// Extra retained versions belong to an in-progress update; they prevent pruning the
// original previous package before failure compensation has restored its snapshot.
// Filesystem cleanup errors remain visible instead of claiming obsolete packages disappeared.
func (m *Manager) prune(ctx context.Context, item *Installation, retain ...PackageRef) error {
	keep := []PackageRef{packageReference(item, "")}
	if item.Previous != nil {
		keep = append(keep, packageReference(&Installation{PluginID: item.PluginID, VersionSnapshot: *item.Previous}, ""))
	}
	keep = append(keep, retain...)
	return m.packages.Prune(ctx, item.PluginID, keep...)
}

// SyncAll reconciles stored installations in ID order during asynchronous host startup.
// Individual failures are logged and leave other installations eligible to start.
// Cancellation stops further admission without changing the remaining durable records.
func (m *Manager) SyncAll(ctx context.Context) {
	items, err := m.store.List(ctx)
	if err != nil {
		m.logger.Error("Plugin installations could not be loaded", "error", err)
		return
	}
	slices.SortFunc(items, func(a, b Installation) int { return bytes.Compare([]byte(a.PluginID), []byte(b.PluginID)) })
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if err := m.Sync(ctx, item.PluginID); err != nil {
			m.logger.Warn("Plugin reconciliation failed", "plugin_id", item.PluginID, "error", err)
		}
	}
}

// Restart drains one installation and reloads its verified package before starting again.
// Development sources may refresh their in-memory declaration first. Disabled
// installations remain stopped, and restarting clears supervisor crash-loop history.
// The loaded package is invalidated after draining so missing files cannot be
// hidden behind the previous in-memory asset snapshot. Declaration refresh errors
// preserve durable intent and publish failed status. Successful reconciliation
// creates a new supervisor only when the stored installation still requests one.
func (m *Manager) Restart(ctx context.Context, id string) error {
	return m.locked(ctx, id, func() (resultErr error) {
		defer func() {
			if resultErr != nil {
				m.recordFailure(id, resultErr)
			}
		}()
		if _, err := m.store.Get(ctx, id); err != nil {
			return err
		}
		if err := m.drainLocked(ctx, id); err != nil {
			return err
		}
		if source, ok := m.store.(interface {
			Reload(context.Context, string) error
		}); ok {
			if err := source.Reload(ctx, id); err != nil {
				return err
			}
		}
		m.mu.Lock()
		delete(m.loaded, id)
		m.mu.Unlock()
		return m.syncLocked(ctx, id)
	})
}

// Snapshot returns an immutable loaded release without waiting for administrative work.
// Returned nested maps and pointers remain manager-owned. Consumers may retain the
// snapshot for a request but must never mutate its installation or asset metadata.
func (m *Manager) Snapshot(id string) (Loaded, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.loaded[id]
	return value, ok
}

// Status combines management observations with the current native supervisor state.
// A manager failure or static state takes precedence over the process observation.
// Otherwise the supervisor supplies live startup, restart, resource, and drain details.
func (m *Manager) Status(id string) Status {
	m.mu.RLock()
	observation := m.observations[id]
	m.mu.RUnlock()
	if observation.State != "" {
		return observation
	}
	return m.processes.Status(id)
}

// Acquire grants a process lease only for enabled and currently admitted installations.
// It never waits for manager reconciliation. Global shutdown, administrative draining,
// and unavailable snapshots fail before accessing the supervisor's bounded lease pool.
func (m *Manager) Acquire(ctx context.Context, id string) (*Lease, error) {
	m.mu.RLock()
	loaded, ok := m.loaded[id]
	blocked := m.blocked[id] || m.stopped
	enabled := m.options.Enabled
	m.mu.RUnlock()
	if !enabled {
		return nil, ErrPluginsDisabled
	}
	if !ok || blocked || !loaded.Installation.Enabled {
		return nil, ErrPluginUnavailable
	}
	return m.processes.Acquire(ctx, id)
}

// acceptedPermissions requires the administrator to confirm every requested capability once.
func acceptedPermissions(requested, accepted []string) bool {
	if len(requested) != len(accepted) {
		return false
	}
	a, b := slices.Clone(requested), slices.Clone(accepted)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// TaskStatuses returns copied in-memory observations for this installation's declared tasks.
// Disabled tasks retain their most recent execution result with no next occurrence.
// The observations reset when the installation is removed or the host restarts.
func (m *Manager) TaskStatuses(id string) []TaskStatus {
	return m.tasks.Status(id)
}

// Enabled reports the immutable host-wide plugin availability switch for HTTP admission.
func (m *Manager) Enabled() bool { return m.options.Enabled }

// Snapshots returns copied outer views ordered by plugin identity for UI discovery.
// Nested release metadata remains manager-owned and immutable. Backend readiness
// does not filter the list because static assets remain usable during process recovery.
func (m *Manager) Snapshots() []Loaded {
	m.mu.RLock()
	views := make([]Loaded, 0, len(m.loaded))
	for _, loaded := range m.loaded {
		views = append(views, loaded)
	}
	m.mu.RUnlock()
	slices.SortFunc(views, func(a, b Loaded) int {
		return bytes.Compare([]byte(a.Installation.PluginID), []byte(b.Installation.PluginID))
	})
	return views
}

// AssetReference selects accepted archive and icon metadata for public asset lookup.
// This metadata lookup does not recover package files or decrypt configuration.
// Unknown identities, versions, and missing declarations return a not-found result.
//
// Current state takes precedence when both accepted packages have the same version.
// Public URLs keep their version component, while browser integrity query keys
// distinguish content changes. Choosing the current archive prevents a stale client
// from silently requesting the old build after replacement; rollback makes that
// retained build current again before its assets become the version's public content.
func (m *Manager) AssetReference(ctx context.Context, id, version string) (PackageRef, error) {
	if !manifest.ValidID(id) || !manifest.ValidVersion(version) {
		return PackageRef{}, ErrPluginNotFound
	}
	item, err := m.store.Get(ctx, id)
	if err != nil {
		return PackageRef{}, err
	}
	snapshot := &item.VersionSnapshot
	if snapshot.Version != version {
		if item.Previous == nil || item.Previous.Version != version {
			return PackageRef{}, ErrPluginNotFound
		}
		snapshot = item.Previous
	}
	if snapshot.Manifest == nil || snapshot.Manifest.ID != id || snapshot.Manifest.Version != version {
		return PackageRef{}, ErrPluginNotFound
	}
	return packageReference(&Installation{PluginID: id, VersionSnapshot: *snapshot}, ""), nil
}
