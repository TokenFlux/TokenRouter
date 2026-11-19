package preaggregation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// settings.go resolves and persists runtime aggregation controls within
// deployment permissions.
// Preaggregation runtime settings can disable work that deployment options
// permit, while deployment-disabled tasks stay unavailable. One JSON setting holds
// usage and Ops controls, and missing fields inherit deployment defaults before
// normalization clamps the usage interval and applies those availability ceilings.
// The same controller supplies the settings API and query/task readers.
//
// Reads share a short-lived atomic cache and one singleflight load. Update
// serializes persistence, cache publication, and local notification, but a concurrent
// Get load publishes without an update generation check and can replace that cache
// with an earlier read. Read failures retain an error in Get; Resolve can use an
// expired cached value or deployment defaults to keep query selection available.

// These interval limits bound normalized usage cadence, and the cache TTL limits
// how long ordinary reads reuse a successfully loaded settings value.
const (
	PreAggregationMinIntervalSeconds = 30
	PreAggregationMaxIntervalSeconds = 3600
	preAggregationSettingsCacheTTL   = 15 * time.Second

	// SettingKeyPreAggregationSettings identifies the single JSON record storing
	// both usage and Ops controls for API updates and background readers.
	SettingKeyPreAggregationSettings = "pre_aggregation_settings"
)

// PreAggregationUsageSettings carries usage enablement and cadence through runtime normalization.
type PreAggregationUsageSettings struct {
	Enabled bool `json:"enabled"` // requested usage aggregation enablement
	// IntervalSeconds supplies usage cadence. The controller clamps it between
	// PreAggregationMinIntervalSeconds and PreAggregationMaxIntervalSeconds before publishing results.
	IntervalSeconds int `json:"interval_seconds"` // seconds
}

// PreAggregationOpsSettings carries the Ops aggregation switch through runtime normalization.
type PreAggregationOpsSettings struct {
	Enabled bool `json:"enabled"` // requested Ops aggregation enablement
}

// PreAggregationSettings contains runtime pre-aggregation configuration stored under one key.
type PreAggregationSettings struct {
	Usage PreAggregationUsageSettings `json:"usage"` // usage enablement and cadence
	Ops   PreAggregationOpsSettings   `json:"ops"`   // Ops aggregation enablement choice
}

// PreAggregationAvailability describes tasks and backfills permitted by deployment configuration.
type PreAggregationAvailability struct {
	// UsageAvailable and OpsAvailable state deployment capability. Their paired
	// UsageDisabledReason and OpsDisabledReason fields explain a false result;
	// runtime disable switches are returned separately in PreAggregationSettings.
	UsageAvailable      bool   // deployment permits usage aggregation
	UsageDisabledReason string // usage deployment restriction reason
	OpsAvailable        bool   // deployment permits Ops aggregation
	OpsDisabledReason   string // Ops deployment restriction reason
	// ManualBackfillAvailable requires deployment usage availability and backfill
	// permission when options are configured; nil options select the constructor's permissive defaults.
	ManualBackfillAvailable bool // deployment permits usage manual backfill
	// ManualBackfillMaxDays is 31 unless deployment supplies a positive maximum.
	// Backfill HTTP admission compares requested days with this resulting limit.
	ManualBackfillMaxDays int // maximum permitted backfill days
}

// cachedPreAggregationSettings holds one immutable value and its reuse cutoff.
type cachedPreAggregationSettings struct {
	// settings and expiresAt are published as one atomic pointer by storeCache.
	// Get checks that cutoff, while Resolve can use the same value after expiry
	// when reloading fails; neither reader mutates this record.
	settings  PreAggregationSettings // normalized cached configuration value
	expiresAt int64                  // Unix nanoseconds
}

// persistedPreAggregationSettings preserves optional JSON fields before merging deployment defaults.
type persistedPreAggregationSettings struct {
	// Usage and Ops keep missing objects separate from supplied field values.
	// Each nonnil nested pointer overrides its deployment default, including false
	// and zero; JSON null or omission leaves that default unchanged.
	Usage *struct { // optional persisted usage controls
		Enabled         *bool `json:"enabled"`          // supplied usage aggregation switch
		IntervalSeconds *int  `json:"interval_seconds"` // supplied usage cadence seconds
	} `json:"usage"`
	Ops *struct { // optional persisted Ops controls
		Enabled *bool `json:"enabled"` // supplied Ops aggregation switch
	} `json:"ops"`
}

// PreAggregationSettingsService resolves normalized controls through a cached
// storage read and publishes persisted replacements to local task listeners.
type PreAggregationSettingsService struct {
	settingRepo Repository // runtime JSON storage operations
	// cfg retains the caller's deployment options. Keep their values stable after
	// construction while cache reads, normalization, and availability checks can use them.
	cfg *Options

	// cache stores immutable normalized values; load coalesces concurrent misses
	// using the first caller's context. Get publication runs outside updateMu, so
	// an earlier load can replace a value published by Update.
	cache atomic.Pointer[cachedPreAggregationSettings] // latest published cache record
	load  singleflight.Group                           // concurrent cache miss coalescing

	// updateMu covers persistence, cache publication, and listener invocation.
	// Listeners may read settings but cannot call Update reentrantly while that
	// lock is held; listenersMu protects registration and callback-list snapshots.
	updateMu    sync.Mutex                                             // complete Update operation lock
	listenersMu sync.RWMutex                                           // listener list access lock
	listeners   []func(PreAggregationSettings, PreAggregationSettings) // callbacks in registration order
}

// Repository provides the controller's setting reads and writes.
type Repository interface {
	// GetValue reads the persisted runtime JSON. A missing key returns
	// settings.ErrSettingNotFound so the controller can use deployment defaults;
	// other read failures remain distinguishable for its cached recovery path.
	GetValue(context.Context, string) (string, error)
	// Set persists the supplied runtime JSON before the controller publishes its
	// cache or listeners. A returned error leaves that Update's publication phases
	// uncalled, and the storage implementation owns write completion.
	Set(context.Context, string, string) error
}

// Options contains deployment-level usage and operations aggregation switches.
type Options struct {
	Usage UsageOptions // usage deployment capability and defaults
	// OpsEnabled and OpsAggregationEnabled jointly allow the Ops runtime switch.
	// A false value in either option makes Availability reject Ops enablement
	// and causes normalization to publish that task as disabled.
	// OpsEnabled: Ops service capability; OpsAggregationEnabled: Ops aggregation capability
	OpsEnabled, OpsAggregationEnabled bool
}

// UsageOptions controls usage aggregation, its interval, and manual backfills.
type UsageOptions struct {
	// Enabled supplies a deployment ceiling: false forces runtime Usage.Enabled
	// false during normalization even when the persisted JSON requests enablement.
	Enabled bool // usage deployment capability switch
	// IntervalSeconds supplies a positive deployment cadence for defaults.
	// Nonpositive values select sixty seconds before the controller clamps its result.
	IntervalSeconds int  // default usage cadence seconds
	BackfillEnabled bool // deployment manual backfill permission
	// BackfillMaxDays replaces Availability's 31-day default when positive.
	// A nonpositive value retains that limit rather than selecting unbounded admission.
	BackfillMaxDays int // maximum manual backfill days
}

// NewPreAggregationSettingsService binds storage and retains deployment options
// for later reads and updates. A nil repository makes Get return defaults, while
// nil options select permissive capability defaults. Callers keep supplied options
// stable during the controller's use.
func NewPreAggregationSettingsService(settingRepo Repository, cfg *Options) *PreAggregationSettingsService {
	return &PreAggregationSettingsService{settingRepo: settingRepo, cfg: cfg}
}

// Availability returns aggregation and manual-backfill capabilities derived from
// deployment options. A nil receiver or nil options permit usage, Ops, and manual
// backfill with a 31-day limit, while configured usage follows its deployment switch
// and configured Ops requires both deployment switches.
//
// Ops service disablement supplies its reason before aggregation disablement.
// Manual backfill additionally requires usage availability and backfill permission,
// while a positive configured maximum replaces the default day limit. These
// capabilities describe admission ceilings; the resolved runtime switches can
// still disable an available task.
func (s *PreAggregationSettingsService) Availability() PreAggregationAvailability {
	availability := PreAggregationAvailability{
		UsageAvailable:          true,
		OpsAvailable:            true,
		ManualBackfillAvailable: true,
		ManualBackfillMaxDays:   31,
	}
	if s == nil || s.cfg == nil {
		return availability
	}
	availability.UsageAvailable = s.cfg.Usage.Enabled
	if !availability.UsageAvailable {
		availability.UsageDisabledReason = "dashboard_aggregation_disabled"
	}
	availability.OpsAvailable = s.cfg.OpsEnabled && s.cfg.OpsAggregationEnabled
	if !s.cfg.OpsEnabled {
		availability.OpsDisabledReason = "ops_disabled"
	} else if !s.cfg.OpsAggregationEnabled {
		availability.OpsDisabledReason = "ops_aggregation_disabled"
	}
	availability.ManualBackfillAvailable = availability.UsageAvailable && s.cfg.Usage.BackfillEnabled
	if s.cfg.Usage.BackfillMaxDays > 0 {
		availability.ManualBackfillMaxDays = s.cfg.Usage.BackfillMaxDays
	}
	return availability
}

// Get returns normalized runtime settings, using deployment defaults when the
// key is absent or storage is unbound. An unexpired atomic record is returned
// immediately; otherwise concurrent cache misses share one singleflight load
// using its first caller's context.
//
// Missing storage records cache the defaults. Other read or parse failures return
// defaults with the error and leave cache publication to successful loads. Duplicate
// Do callers wait for the shared result even if their own context is cancelled.
// Get does not coordinate publication with Update, so a load begun earlier can
// replace the update's cached value after that update completes.
func (s *PreAggregationSettingsService) Get(ctx context.Context) (PreAggregationSettings, error) {
	defaults := s.defaults()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	if cached := s.cache.Load(); cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.settings, nil
	}

	// Coalesce cache misses around the first caller's storage request; each
	// waiter receives that result, and this load publishes without an Update generation check.
	value, err, _ := s.load.Do(SettingKeyPreAggregationSettings, func() (any, error) {
		raw, getErr := s.settingRepo.GetValue(ctx, SettingKeyPreAggregationSettings)
		if getErr != nil {
			if errors.Is(getErr, settings.ErrSettingNotFound) {
				// Cache deployment defaults after confirmed key absence so subsequent
				// readers can reuse them until the short cutoff triggers another storage read.
				s.storeCache(defaults)
				return defaults, nil
			}
			return PreAggregationSettings{}, getErr
		}
		parsed, parseErr := parsePreAggregationSettings(raw, defaults)
		if parseErr != nil {
			return PreAggregationSettings{}, parseErr
		}
		parsed = s.normalize(parsed)
		s.storeCache(parsed)
		return parsed, nil
	})
	if err != nil {
		return defaults, err
	}
	settings, ok := value.(PreAggregationSettings)
	if !ok {
		return defaults, errors.New("invalid pre-aggregation settings load result type")
	}
	return settings, nil
}

// Resolve returns configuration for query and task selection after attempting
// Get. A failure selects any existing cache record, including an expired one;
// when none exists, it logs the read failure and returns deployment defaults.
func (s *PreAggregationSettingsService) Resolve(ctx context.Context) PreAggregationSettings {
	settings, err := s.Get(ctx)
	if err == nil {
		return settings
	}
	// A failed reload can still use the last observed configuration after its
	// ordinary reuse cutoff; deployment defaults are selected only when no record exists.
	if cached := s.cache.Load(); cached != nil {
		return cached.settings
	}
	slog.Info("[PreAggregation] Failed to read runtime configuration, using deployment defaults", "error", err)
	return s.defaults()
}

// Update persists a normalized replacement, publishes the cache, and invokes
// local listeners in registration order. It requires bound storage and holds
// updateMu through reading previous settings, persistence, and notification, so
// listeners cannot call Update reentrantly.
//
// Previous values come from Resolve and may be cached or deployment defaults
// after a read failure. Persistence error stops publication for this update.
// After successful storage, listener errors are outside this signature because
// callbacks return no result; panics propagate, and listener execution remains
// synchronous. Concurrent Get loads can still publish an earlier read afterward.
func (s *PreAggregationSettingsService) Update(ctx context.Context, next PreAggregationSettings) (PreAggregationSettings, error) {
	if s == nil || s.settingRepo == nil {
		return PreAggregationSettings{}, errors.New("pre-aggregation configuration service is unavailable")
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	previous := s.Resolve(ctx)
	next = s.normalize(next)
	payload, err := json.Marshal(next)
	if err != nil {
		return PreAggregationSettings{}, fmt.Errorf("encode pre-aggregation configuration: %w", err)
	}
	// Save the normalized replacement before publishing next or notifying listeners.
	// A failed write skips those phases after previous settings were resolved.
	if err := s.settingRepo.Set(ctx, SettingKeyPreAggregationSettings, string(payload)); err != nil {
		return PreAggregationSettings{}, fmt.Errorf("save pre-aggregation configuration: %w", err)
	}
	// Publish after storage succeeds and before listeners run so ordinary cached
	// reads can use the replacement; independent Get loads can still republish an earlier read.
	s.storeCache(next)
	s.notify(previous, next)
	return next, nil
}

// RegisterListener appends a synchronous update callback in registration order.
// A nil receiver or callback is ignored; registrations remain installed for
// this service's lifetime, and callbacks follow Update's reentrancy calling rule.
func (s *PreAggregationSettingsService) RegisterListener(listener func(PreAggregationSettings, PreAggregationSettings)) {
	if s == nil || listener == nil {
		return
	}
	s.listenersMu.Lock()
	s.listeners = append(s.listeners, listener)
	s.listenersMu.Unlock()
}

// UsageEnabled reports whether deployment availability and the resolved usage
// switch both permit aggregation. Read failures follow Resolve's cache/default
// selection, so callers receive a boolean decision without a separate settings error.
func (s *PreAggregationSettingsService) UsageEnabled(ctx context.Context) bool {
	availability := s.Availability()
	return availability.UsageAvailable && s.Resolve(ctx).Usage.Enabled
}

// OpsEnabled reports whether deployment availability and the resolved Ops switch
// both permit aggregation. Read failures use Resolve's cache/default selection,
// while the Ops service can additionally check its monitoring setting before work.
func (s *PreAggregationSettingsService) OpsEnabled(ctx context.Context) bool {
	availability := s.Availability()
	return availability.OpsAvailable && s.Resolve(ctx).Ops.Enabled
}

// defaults derives runtime controls from retained deployment options before any
// persisted overrides are read. A nil receiver or nil options enable usage and Ops
// with a sixty-second interval. Configured usage follows its switch, while Ops
// requires both service and aggregation enablement.
//
// A positive configured usage cadence replaces sixty seconds. Nonpositive input
// retains that default, and normalize then clamps the cadence and applies deployment
// ceilings. These values are also used after absent storage or in Resolve's recovery
// when no previously cached runtime configuration is available.
func (s *PreAggregationSettingsService) defaults() PreAggregationSettings {
	settings := PreAggregationSettings{
		Usage: PreAggregationUsageSettings{Enabled: true, IntervalSeconds: 60},
		Ops:   PreAggregationOpsSettings{Enabled: true},
	}
	if s == nil || s.cfg == nil {
		return settings
	}
	settings.Usage.Enabled = s.cfg.Usage.Enabled
	settings.Ops.Enabled = s.cfg.OpsEnabled && s.cfg.OpsAggregationEnabled
	if s.cfg.Usage.IntervalSeconds > 0 {
		settings.Usage.IntervalSeconds = s.cfg.Usage.IntervalSeconds
	}
	return s.normalize(settings)
}

// normalize returns runtime controls after cadence limits and deployment
// permissions are applied. Usage cadence is clamped to 30 through 3,600 seconds,
// then each requested enablement flag is combined with that task's deployment
// availability before the value can be published.
//
// A disabled deployment task remains disabled even when persisted JSON requests
// it. A permitted task still follows its runtime false value, and its interval
// remains normalized while disabled so a later enablement has a valid cadence.
// The function returns a value copy and leaves its caller's input unchanged.
func (s *PreAggregationSettingsService) normalize(settings PreAggregationSettings) PreAggregationSettings {
	// Clamp both interval bounds even while usage is disabled so later
	// enablement receives a cadence within the controller's admitted range.
	if settings.Usage.IntervalSeconds < PreAggregationMinIntervalSeconds {
		settings.Usage.IntervalSeconds = PreAggregationMinIntervalSeconds
	}
	if settings.Usage.IntervalSeconds > PreAggregationMaxIntervalSeconds {
		settings.Usage.IntervalSeconds = PreAggregationMaxIntervalSeconds
	}
	availability := s.Availability()
	settings.Usage.Enabled = settings.Usage.Enabled && availability.UsageAvailable
	settings.Ops.Enabled = settings.Ops.Enabled && availability.OpsAvailable
	return settings
}

// storeCache publishes the supplied immutable value with a fifteen-second
// Unix-nanosecond reuse cutoff through the atomic pointer. A nil receiver is
// ignored; publication does not compare update generations or prior cache state.
func (s *PreAggregationSettingsService) storeCache(settings PreAggregationSettings) {
	if s == nil {
		return
	}
	s.cache.Store(&cachedPreAggregationSettings{
		settings: settings,
		// Cache comparisons use Unix nanoseconds for the fixed reuse cutoff.
		expiresAt: time.Now().Add(preAggregationSettingsCacheTTL).UnixNano(),
	})
}

// notify snapshots the listener list under its lock and invokes callbacks
// in registration order after releasing that list lock. Update calls it while
// holding updateMu, so callback reentrancy follows Update's calling rules.
func (s *PreAggregationSettingsService) notify(previous, next PreAggregationSettings) {
	s.listenersMu.RLock()
	// Copy registrations before callbacks so listeners can register another
	// listener without changing this notification's iteration or holding its registration lock.
	listeners := append([]func(PreAggregationSettings, PreAggregationSettings){}, s.listeners...)
	s.listenersMu.RUnlock()
	for _, listener := range listeners {
		listener(previous, next)
	}
}

// parsePreAggregationSettings overlays optional persisted JSON values onto
// supplied deployment defaults. Missing or null objects and nested fields retain
// their defaults, while present false and zero values remain supplied choices
// for later normalization.
//
// Pointer fields preserve that difference during decoding, preventing an omitted
// legacy switch from becoming disabled merely because its Go zero value is false.
// Invalid JSON returns an error. The parser itself leaves interval limits and
// deployment ceilings to normalize, so its returned result still needs that phase
// before publication or task selection.
func parsePreAggregationSettings(raw string, defaults PreAggregationSettings) (PreAggregationSettings, error) {
	var persisted persistedPreAggregationSettings
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		return PreAggregationSettings{}, fmt.Errorf("parse pre-aggregation configuration: %w", err)
	}
	result := defaults
	if persisted.Usage != nil {
		if persisted.Usage.Enabled != nil {
			result.Usage.Enabled = *persisted.Usage.Enabled
		}
		if persisted.Usage.IntervalSeconds != nil {
			result.Usage.IntervalSeconds = *persisted.Usage.IntervalSeconds
		}
	}
	if persisted.Ops != nil && persisted.Ops.Enabled != nil {
		result.Ops.Enabled = *persisted.Ops.Enabled
	}
	return result, nil
}
