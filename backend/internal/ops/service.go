package ops

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// service.go records request errors and publishes runtime monitoring
// preferences for Ops readers.
// Ops request ingestion uses the supplied mutable error entry through
// preparation and persistence. Monitoring gates can skip recording, while
// preparation defaults required fields, selects retained provider-auth
// evidence, and bounds supported diagnostics. Structured JSON credentials are
// redacted by key; plain text and supplied endpoint strings still require
// producer preparation.
//
// Monitoring and advanced preferences are published together through an atomic
// snapshot after serialized loads or updates. Request readers use that
// snapshot, and a restartable polling loop keeps it current without synchronous
// hot-path queries. Read failures retain the existing pair, but malformed
// advanced JSON replaces that preference with defaults after a successful
// settings read. User-facing historical error queries use a supplied
// authenticated owner identity independently of the monitoring gate;
// administrator reads and writes apply that gate. Filter copies and JSON
// shrinking preserve only their stated sharing and identity rules.

// opsMaxStoredErrorBodyBytes limits prepared diagnostic body/detail bytes;
// runtime refresh constants govern polling, jitter, query budget, and
// failure-log frequency independently of capture configuration.
const (
	opsMaxStoredErrorBodyBytes = 20 * 1024 // prepared stored body byte cap
	// OpsErrorLogQueueBodyMaxBytes supplies the eight-KiB preparation budget for
	// queued bodies and attempt detail, before waiting-byte estimates and storage
	// preparation read them.
	OpsErrorLogQueueBodyMaxBytes = 8 * 1024

	opsRuntimeSettingsRefreshInterval = 30 * time.Second // default polling wait duration
	opsRuntimeSettingsRefreshJitter   = 20               // default polling jitter percent
	opsRuntimeSettingsRefreshTimeout  = 3 * time.Second  // default refresh attempt budget
	opsRuntimeSettingsFailureLogEvery = time.Minute      // shared refresh log gate duration
)

// ErrOpsDisabled supplies the not-found monitoring denial returned before
// administrator Ops reads and writes when their shared monitoring gate is
// closed.
var ErrOpsDisabled = infraerrors.NotFound("OPS_DISABLED", "Ops monitoring is disabled")

// opsRuntimeSettingsSnapshot publishes monitoring and advanced preferences as
// one immutable reader-visible value.
type opsRuntimeSettingsSnapshot struct {
	// monitoringEnabled and advanced publish together through runtimeSettings.
	// Writers build a replacement rather than changing the entry; advanced can
	// retain caller slice storage, which stays immutable after publication.
	monitoringEnabled bool                // cached monitoring switch value
	advanced          OpsAdvancedSettings // cached advanced preference value
}

// OpsService coordinates observation preparation, cached preferences,
// monitoring reads, and bound runtime consumers. Construct and bind mutable
// dependencies before concurrent use; settings snapshots have separate
// publication synchronization, while dependency assignment itself is not
// synchronized.
type OpsService struct {
	// snapshotOnce initializes snapshotCache once for GetDashboardSnapshot. Cached
	// results follow that method's copy and lifetime rules, while initialization
	// is independent of runtimeSettings preference publication.
	snapshotOnce  sync.Once         // single dashboard cache initializer
	snapshotCache *querycache.Cache // short-lived dashboard snapshot cache
	// realtimeOnce initializes realtime on first live access. RealtimeRuntime owns
	// sampling and idle shutdown; its resource lifecycle is independent of the
	// preference refresh loop.
	realtimeOnce sync.Once        // single live runtime initializer
	realtime     *RealtimeRuntime // lazily constructed live runtime
	logging      LogControl       // assembled process logger control
	opsRepo      OpsRepository    // observation and alert persistence
	settingRepo  Settings         // runtime preference storage reader
	// cfg retains deployment options, logger callbacks, and hard monitoring
	// policy. Callers keep this object immutable while request readers and
	// background jobs consult it.
	cfg *Options
	// preAggregationSettings is bound before concurrent queries and consulted by
	// resolveOpsQueryMode. Nil leaves deployment-derived availability in control,
	// and the field assignment itself has no synchronization.
	preAggregationSettings PreAggregationReader

	providerRepo ProviderReader // provider observation page reader
	userRepo     UserReader     // active user observation reader

	// fixture availability lookup override
	// getProviderAvailability overrides normal provider reads for fixture use.
	// Bind it before concurrent requests; production availability otherwise reads
	// providerRepo and concurrencyService.
	getProviderAvailability func(ctx context.Context, platformFilter string, groupIDFilter *int64) (*OpsProviderAvailability, error)

	concurrencyService ConcurrencyReader // runtime provider/user load reader

	systemLogSink               *OpsSystemLogSink           // assembled system-log queue observer
	ingressRejectAggregator     *OpsIngressRejectAggregator // bound ingress aggregate observer
	authCacheInvalidationWorker AuthHealthReader            // bound auth invalidation health reader
	apiKeyService               KeyHealthReader             // bound key-cache health reader

	// cleanupReloader is assigned after cleanup construction because cleanup reads
	// these preferences. UpdateOpsAdvancedSettings invokes it synchronously after
	// persistence and suppresses its returned failure.
	cleanupReloader CleanupReloader // late-bound cleanup schedule callback

	// late-bound quota preference callback
	// quotaAutoPauseSink publishes accepted defaults to the scheduler after
	// persistence and snapshot publication. Bind it before concurrent updates;
	// assignment has no separate mutex.
	quotaAutoPauseSink func(OpsOpenAIProviderQuotaAutoPauseSettings)

	// published immutable preference entry
	// runtimeSettings publishes the completed monitoring/advanced entry
	// atomically. runtimeSettingsMu serializes database loads and component
	// updates through publication; readers treat its nested exclusion slice as
	// immutable.
	runtimeSettings   atomic.Pointer[opsRuntimeSettingsSnapshot]
	runtimeSettingsMu sync.Mutex // preference load/publication writer mutex

	// runtimeRefreshMu protects runtimeRefreshCancel and runtimeRefreshDone as the
	// current loop identity. Stop waits outside the mutex, then clears handles
	// only if the done channel still identifies its captured loop.
	runtimeRefreshMu     sync.Mutex         // poll-loop handle publication mutex
	runtimeRefreshCancel context.CancelFunc // current loop cancellation handle
	runtimeRefreshDone   chan struct{}      // current loop completion signal
	// runtimeRefreshRunning, runtimeRefreshSuccess, runtimeRefreshFailure, and
	// runtimeRefreshLastFailureLog are independently sampled atomics. Counters and
	// the Unix-second log gate survive loop restarts and do not constitute one
	// synchronized health snapshot.
	runtimeRefreshRunning        atomic.Bool   // sampled polling running flag
	runtimeRefreshSuccess        atomic.Uint64 // cumulative successful refresh attempts
	runtimeRefreshFailure        atomic.Uint64 // cumulative failed refresh attempts
	runtimeRefreshLastFailureLog atomic.Int64  // Unix-second failure-log gate
}

// CleanupReloader applies stored cleanup preferences after successful
// advanced-settings persistence.
type CleanupReloader interface {
	// Reload rebuilds cleanup scheduling using the supplied context and returns
	// any failure. OpsService invokes it after settings and snapshot publication,
	// routes its error through the optional service logger, and still returns accepted preferences.
	Reload(ctx context.Context) error
}

// NewOpsService constructs retained dependencies, initializes default/runtime
// preferences, and applies resolved saved/default logger settings synchronously
// with background contexts. Startup settings errors are suppressed by those
// helpers. Construction installs no overall timeout or polling loop; dependency
// reads can delay assembly, and app binds remaining consumers before later
// lifecycle startup.
func NewOpsService(
	opsRepo OpsRepository,
	settingRepo Settings,
	cfg *Options,
	providerRepo ProviderReader,
	userRepo UserReader,
	concurrencyService ConcurrencyReader,

	systemLogSink *OpsSystemLogSink,
	logging LogControl,
) *OpsService {
	svc := BuildOpsService(opsRepo, settingRepo, cfg, providerRepo, userRepo, concurrencyService, systemLogSink, logging)
	svc.initRuntimeSettings(context.Background())
	svc.ApplyRuntimeLogConfigOnStartup(context.Background())
	return svc
}

// SetPreAggregationSettings retains the shared query-mode reader, replacing any
// prior binding. Nil service is ignored; bind or replace before concurrent
// requests because this dependency field has no separate synchronization.
func (s *OpsService) SetPreAggregationSettings(settings PreAggregationReader) {
	if s == nil {
		return
	}
	s.preAggregationSettings = settings
}

// SetCleanupReloader retains the schedule consumer after cleanup construction
// and accepts nil to clear it. Nil service is ignored; complete this binding
// before concurrent settings updates, which call Reload synchronously after
// persistence.
func (s *OpsService) SetCleanupReloader(r CleanupReloader) {
	if s == nil {
		return
	}
	s.cleanupReloader = r
}

// SetOpenAIQuotaAutoPauseSettingsSink retains a synchronous
// scheduler-preference callback and accepts nil to clear it. Bind before
// concurrent settings writes; the callback runs after successful advanced
// persistence and snapshot publication, outside the settings transaction.
func (s *OpsService) SetOpenAIQuotaAutoPauseSettingsSink(sink func(OpsOpenAIProviderQuotaAutoPauseSettings)) {
	if s == nil {
		return
	}
	s.quotaAutoPauseSink = sink
}

// BuildOpsService retains the supplied repositories, readers, logger control,
// sink, and options without initializing preference snapshots or applying
// logger settings. Callers initialize and bind remaining consumers before
// concurrent use; absent snapshot reads use service defaults.
func BuildOpsService(
	opsRepo OpsRepository,
	settingRepo Settings,
	cfg *Options,
	providerRepo ProviderReader,
	userRepo UserReader,
	concurrencyService ConcurrencyReader,

	systemLogSink *OpsSystemLogSink,
	logging LogControl,
) *OpsService {
	svc := &OpsService{
		opsRepo:     opsRepo,
		settingRepo: settingRepo,
		cfg:         cfg,

		providerRepo: providerRepo,
		userRepo:     userRepo,

		concurrencyService: concurrencyService,

		systemLogSink: systemLogSink,
		logging:       logging,
	}
	return svc
}

// RequireMonitoringEnabled returns the shared not-found error when
// IsMonitoringEnabled denies monitoring, otherwise nil. It requires a nonnil
// service and inherits cached/default timing from that gate rather than reading
// settings directly.
func (s *OpsService) RequireMonitoringEnabled(ctx context.Context) error {
	if s.IsMonitoringEnabled(ctx) {
		return nil
	}
	return ErrOpsDisabled
}

// IsMonitoringEnabled reports the cached monitoring choice unless deployment
// options hard-disable Ops. A nonnil service is required, ctx is ignored, and a
// missing runtime snapshot defaults to enabled without querying storage.
func (s *OpsService) IsMonitoringEnabled(ctx context.Context) bool {
	_ = ctx
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return false
	}
	if snapshot := s.runtimeSettings.Load(); snapshot != nil {
		return snapshot.monitoringEnabled
	}
	// Keep missing snapshot reads on the enabled default so hot-path requests do
	// not wait for synchronous settings storage; deployment denial was checked
	// first.
	return true
}

// parseOpsMonitoringEnabled recognizes trimmed case-insensitive false, zero,
// off, and disabled as denial. Empty, malformed, or other supplied text selects
// enabled, matching the service's missing-setting default.
func parseOpsMonitoringEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

// initRuntimeSettings installs enabled/default preferences and attempts a
// serialized storage refresh, ignoring its error. Nil service is skipped; call
// during initialization before concurrent component updates, because the
// initial default store itself does not use the writer mutex.
func (s *OpsService) initRuntimeSettings(ctx context.Context) {
	if s == nil {
		return
	}
	defaults := defaultOpsAdvancedSettings()
	s.runtimeSettings.Store(&opsRuntimeSettingsSnapshot{monitoringEnabled: true, advanced: *defaults})
	// Publish initialized defaults before the first storage attempt, so
	// construction can continue with a coherent enabled/default entry when the
	// refresh fails.
	_ = s.RefreshRuntimeSettings(ctx)
}

// RefreshRuntimeSettings loads monitoring and advanced settings and publishes
// one computed reader entry while holding the settings-writer mutex. Nil
// service or repository is skipped and nil context selects background. A
// storage error returns before publication, retaining the previous entry.
//
// Advanced JSON overlays new defaults; malformed JSON discards that preference
// and resets it to defaults, while the successfully read monitoring value still
// applies. Normalization retains explicit empty exclusions and fills missing
// tuning. The atomic publication keeps readers from seeing a partially assigned
// pair, but does not establish a database transaction across an arbitrary
// Settings implementation. The writer mutex also serializes direct component
// updates while storage reads are pending.
func (s *OpsService) RefreshRuntimeSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Serialize storage loading and replacement against component updates. Keep
	// the old entry published until a complete computed monitoring/advanced pair
	// is ready.
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyOpsMonitoringEnabled,
		SettingKeyOpsAdvancedSettings,
	})
	if err != nil {
		return err
	}

	monitoringEnabled := true
	if raw, ok := values[SettingKeyOpsMonitoringEnabled]; ok {
		monitoringEnabled = parseOpsMonitoringEnabled(raw)
	}
	advanced := defaultOpsAdvancedSettings()
	if raw, ok := values[SettingKeyOpsAdvancedSettings]; ok {
		// Decode over new defaults rather than the previous advanced value. A
		// malformed record restores defaults for this preference while the separately
		// loaded monitoring value still applies.
		if err := json.Unmarshal([]byte(raw), advanced); err != nil {
			advanced = defaultOpsAdvancedSettings()
		}
	}
	normalizeOpsAdvancedSettings(advanced)

	// Publish the completed immutable pair in one atomic store. Its exclusion
	// slice belongs to this new entry, while direct administrative snapshots can
	// retain caller storage.
	s.runtimeSettings.Store(&opsRuntimeSettingsSnapshot{monitoringEnabled: monitoringEnabled, advanced: *advanced})
	return nil
}

// StartRuntimeSettingsRefresh starts default thirty-second polling with
// twenty-percent jitter and three-second attempt budgets. Missing service or
// settings skips startup, nil context selects background, and an existing
// retained cancellation handle suppresses another start.
//
// The first attempt follows the initial wait, while later waits follow
// completed attempts. Parent cancellation stops that loop, but its handle
// remains installed until StopRuntimeSettingsRefresh joins and clears it.
// Callers serialize startup against shutdown when a returned stop must mean no
// polling loop remains; unlike permanent queue shutdown, a cleared refresh loop
// can be started again.
func (s *OpsService) StartRuntimeSettingsRefresh(ctx context.Context) {
	s.startRuntimeSettingsRefresh(ctx, opsRuntimeSettingsRefreshInterval, opsRuntimeSettingsRefreshJitter, opsRuntimeSettingsRefreshTimeout)
}

// startRuntimeSettingsRefresh publishes one cancellable polling loop unless an
// earlier loop handle remains installed. Nonpositive interval/budget select
// defaults and jitter clamps from zero to one hundred. Nil context selects
// background; each attempt inherits cancellation and its own timeout, with
// waits after attempt completion.
//
// The mutex publishes cancel and done before launching work, allowing Stop to
// cancel and wait even if goroutine scheduling is delayed. Running becomes
// false before done closes, while success/failure counters persist across
// restarts. A canceled loop retains its handle until a Stop clears it, so later
// starts do not automatically reopen it. Lifecycle callers serialize new starts
// with stop when they require an idle service at return.
func (s *OpsService) startRuntimeSettingsRefresh(ctx context.Context, interval time.Duration, jitterPercent int, timeout time.Duration) {
	if s == nil || s.settingRepo == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = opsRuntimeSettingsRefreshInterval
	}
	if timeout <= 0 {
		timeout = opsRuntimeSettingsRefreshTimeout
	}
	// Limit jitter to a fractional spread no larger than the polling interval.
	if jitterPercent < 0 {
		jitterPercent = 0
	}
	if jitterPercent > 100 {
		jitterPercent = 100
	}

	// Publish cancellation and completion handles under one mutex before starting
	// the goroutine. Stop can capture that identity and wait outside the lock.
	s.runtimeRefreshMu.Lock()
	if s.runtimeRefreshCancel != nil {
		s.runtimeRefreshMu.Unlock()
		return
	}
	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.runtimeRefreshCancel = cancel
	s.runtimeRefreshDone = done
	s.runtimeRefreshRunning.Store(true)
	s.runtimeRefreshMu.Unlock()

	// Start one polling worker after its handles are visible. Deferred
	// running-state publication precedes done closure, while attempt counters
	// remain available across later restarts.
	go func() {
		defer close(done)
		defer s.runtimeRefreshRunning.Store(false)
		for {
			delay := jitterDuration(interval, jitterPercent)
			timer := time.NewTimer(delay)
			// Wait before each refresh and release a pending timer on cancellation. No
			// initial synchronous query is performed by this polling loop.
			select {
			case <-refreshCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}

			// Give this settings read a child budget of the loop context, then release
			// it. Failed attempts keep the published entry and update failure
			// diagnostics.
			attemptCtx, attemptCancel := context.WithTimeout(refreshCtx, timeout)
			err := s.RefreshRuntimeSettings(attemptCtx)
			attemptCancel()
			if err != nil {
				s.runtimeRefreshFailure.Add(1)
				s.logRuntimeSettingsRefreshFailure(err)
				continue
			}
			s.runtimeRefreshSuccess.Add(1)
		}
	}()
}

// jitterDuration scales positive base by a uniform factor around one using
// supplied percentage, truncating fractional nanoseconds. Nonpositive
// base/percentage or a nonpositive sampled factor returns base; callers
// normalize percentage themselves, and the polling starter caps it at one
// hundred.
func jitterDuration(base time.Duration, percent int) time.Duration {
	if base <= 0 || percent <= 0 {
		return base
	}
	// Express the configured percent as a fraction for interval scaling.
	delta := float64(percent) / 100
	factor := 1 - delta + rand.Float64()*(2*delta)
	if factor <= 0 {
		return base
	}
	// Floating-point scaling truncates fractional nanoseconds in the returned
	// delay.
	return time.Duration(float64(base) * factor)
}

// logRuntimeSettingsRefreshFailure emits one process-log diagnostic per
// Unix-second minute gate after a nonnil error. Its atomic compare-and-swap
// chooses the winning timestamp across callers; successes and restarts do not
// reset the gate, and a backward clock can prolong suppression.
//
// The timestamp is stored before LegacyPrintf executes, so a concurrent failure
// can see the gate while the winning message is still being written. Polling
// counts each failed attempt separately; suppression limits messages without
// reducing the failure counter.
func (s *OpsService) logRuntimeSettingsRefreshFailure(err error) {
	if s == nil || err == nil {
		return
	}
	now := time.Now().Unix()
	// Use Unix seconds for the shared minute log gate, including across restarts.
	// Compare-and-swap admits one diagnostic while ordinary failure counters still
	// count suppressed attempts.
	for {
		last := s.runtimeRefreshLastFailureLog.Load()
		if last != 0 && now-last < int64(opsRuntimeSettingsFailureLogEvery/time.Second) {
			return
		}
		if s.runtimeRefreshLastFailureLog.CompareAndSwap(last, now) {
			logger.LegacyPrintf("service.ops", "[Ops] runtime settings refresh failed: %v", err)
			return
		}
	}
}

// StopRuntimeSettingsRefresh captures and cancels the current loop, waits for
// its completion outside the handle mutex, and clears that identity if still
// current. Nil service and missing handles are harmless, and repeated stops can
// wait for the same loop.
//
// Stopping does not permanently seal future startup. When Stop captures no
// handles and Start installs a loop afterward, that loop lies outside the
// completed stop's wait. Callers serialize lifecycle operations when shutdown
// requires an idle return. Identity comparison prevents a delayed stop from
// clearing handles belonging to a newly started loop; waiting itself has no
// caller timeout.
func (s *OpsService) StopRuntimeSettingsRefresh() {
	if s == nil {
		return
	}
	s.runtimeRefreshMu.Lock()
	cancel := s.runtimeRefreshCancel
	done := s.runtimeRefreshDone
	s.runtimeRefreshMu.Unlock()
	// Cancel the captured loop and wait after releasing its handle mutex. Later
	// identity comparison keeps a concurrently replaced loop's handles intact.
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.runtimeRefreshMu.Lock()
	if s.runtimeRefreshDone == done {
		s.runtimeRefreshCancel = nil
		s.runtimeRefreshDone = nil
	}
	s.runtimeRefreshMu.Unlock()
}

// SetMonitoringEnabled publishes an in-memory monitoring choice while retaining
// current advanced preferences under the writer mutex. Nil service is ignored
// and absent advanced state uses defaults. Persistence belongs to the caller;
// this method does not change the settings record.
//
// Readers load the replacement as one entry, so the unchanged advanced value
// belongs to the same captured publication rather than a separate load. Its
// exclusion slice stays shared read-only, and a later successful refresh can
// replace this memory-only monitoring choice with persisted settings.
func (s *OpsService) SetMonitoringEnabled(enabled bool) {
	if s == nil {
		return
	}
	// Build a replacement while holding the writer mutex so changing monitoring
	// keeps the advanced value from the same current entry, then publish
	// atomically.
	s.runtimeSettingsMu.Lock()
	current := s.runtimeSettings.Load()
	next := &opsRuntimeSettingsSnapshot{monitoringEnabled: enabled, advanced: *defaultOpsAdvancedSettings()}
	if current != nil {
		next.advanced = current.advanced
	}
	s.runtimeSettings.Store(next)
	s.runtimeSettingsMu.Unlock()
}

// storeAdvancedSettingsSnapshot publishes an advanced value while retaining the
// current monitoring choice under the writer mutex. Nil service/config is
// ignored and absent monitoring defaults to enabled. The copied advanced value
// shares IgnoredStatusCodes with cfg, which callers leave immutable after
// publication.
//
// Readers see one replacement pair, and direct monitoring updates serialize
// against the same writer lock so their current choice can survive this
// component change. The method performs no persistence;
// UpdateOpsAdvancedSettings calls it after a successful write, while later
// refresh can replace the entry from storage.
func (s *OpsService) storeAdvancedSettingsSnapshot(cfg *OpsAdvancedSettings) {
	if s == nil || cfg == nil {
		return
	}
	// Retain monitoring while replacing advanced preferences under the writer
	// mutex. The value copy shares the supplied exclusion slice, which stays
	// immutable after publication.
	s.runtimeSettingsMu.Lock()
	current := s.runtimeSettings.Load()
	next := &opsRuntimeSettingsSnapshot{monitoringEnabled: true, advanced: *cfg}
	if current != nil {
		next.monitoringEnabled = current.monitoringEnabled
	}
	s.runtimeSettings.Store(next)
	s.runtimeSettingsMu.Unlock()
}

// SanitizeOpsErrorBodyForQueue trims and prepares diagnostic text within the
// queue's eight-KiB budget, reporting whether a size-reduction path was used.
// JSON credential keys are redacted before shrinking; redaction alone leaves
// that flag false, and non-JSON text needs producer redaction.
func SanitizeOpsErrorBodyForQueue(raw string) (string, bool) {
	return sanitizeErrorBodyForStorage(raw, OpsErrorLogQueueBodyMaxBytes)
}

// SanitizeOpsUpstreamErrorsForQueue prepares a mutable entry's retained
// attempts and releases the raw slice after serialization. Nil or empty
// attempts leave supplied serialized JSON unchanged, and the delegated helper
// currently returns nil rather than exposing marshal failure.
func SanitizeOpsUpstreamErrorsForQueue(entry *OpsInsertErrorLogInput) error {
	return sanitizeOpsUpstreamErrors(entry)
}

// RecordError prepares and persists the supplied mutable entry synchronously
// with ctx. Nil input, disabled monitoring, or missing repository skips
// recording successfully. Preparation can change timestamps, classification,
// and diagnostics in place; write failures are logged and returned.
func (s *OpsService) RecordError(ctx context.Context, entry *OpsInsertErrorLogInput) error {
	prepared, ok, err := s.prepareErrorLogInput(ctx, entry)
	if err != nil {
		logger.LegacyPrintf("service.ops", "[Ops] RecordError prepare failed: %v", err)
		return err
	}
	if !ok {
		return nil
	}

	if _, err := s.opsRepo.InsertErrorLog(ctx, prepared); err != nil {
		// A synchronous write failure remains visible to the caller after preparation
		// has changed the input. Log it once here and return the same repository
		// error.
		logger.LegacyPrintf("service.ops", "[Ops] RecordError failed: %v", err)
		return err
	}
	return nil
}

// RecordErrorBatch prepares each supplied mutable entry and writes eligible
// observations once, using single insertion for one survivor and batch
// insertion for more. Empty input or no survivors returns nil. Preparation
// errors are logged and skipped, so a successful batch result need not mean
// every supplied item was written.
//
// Repository failures propagate after one write attempt, with no replay through
// single inserts. Cancellation-aware logging can suppress a diagnostic while
// the error still returns. The service leaves rollback and committed-count
// interpretation to storage, and callers keep entries stable while preparation
// and persistence read them.
func (s *OpsService) RecordErrorBatch(ctx context.Context, entries []*OpsInsertErrorLogInput) error {
	if len(entries) == 0 {
		return nil
	}
	prepared := make([]*OpsInsertErrorLogInput, 0, len(entries))
	for _, entry := range entries {
		item, ok, err := s.prepareErrorLogInput(ctx, entry)
		// Skip an entry whose preparation fails while retaining other eligible
		// observations. Logging the preparation error does not make the final batch
		// call fail by itself.
		if err != nil {
			logger.LegacyPrintf("service.ops", "[Ops] RecordErrorBatch prepare failed: %v", err)
			continue
		}
		if ok {
			prepared = append(prepared, item)
		}
	}
	if len(prepared) == 0 {
		return nil
	}
	// Choose one write path from the surviving prepared count. A failed multi-row
	// batch is not replayed singly, preventing this layer from duplicating
	// uncertain persistence.
	if len(prepared) == 1 {
		_, err := s.opsRepo.InsertErrorLog(ctx, prepared[0])
		if err != nil {
			logOpsErrorBatchWriteFailure(ctx, "[Ops] RecordErrorBatch single insert failed: %v", err)
		}
		return err
	}

	if _, err := s.opsRepo.BatchInsertErrorLogs(ctx, prepared); err != nil {
		logOpsErrorBatchWriteFailure(ctx, "[Ops] RecordErrorBatch failed: %v", err)
		return err
	}
	return nil
}

// logOpsErrorBatchWriteFailure logs a repository write error unless the error
// is cancellation or ctx already reports cancellation/deadline. Error-only
// deadline expiry with a live/nil context is still logged, matching
// isContextDoneError.
func logOpsErrorBatchWriteFailure(ctx context.Context, format string, err error) {
	// Recognized request termination suppresses this process message while
	// RecordErrorBatch still returns the write error; context state can identify
	// drivers lacking standard cancellation errors.
	if isContextDoneError(ctx, err) {
		return
	}
	logger.LegacyPrintf("service.ops", format, err)
}

// prepareErrorLogInput returns the supplied entry after in-place preparation
// when monitoring and storage permit recording. Nil entry or unavailable
// ingestion returns false without mutation. Missing time, phase, and type
// receive defaults, while other classification fields remain producer-supplied
// except retained provider-auth precedence.
//
// Only the newest nonnil raw attempt is inspected: provider_auth replaces
// phase, owner/source, upstream zero, and optional text before serialization.
// An entry already prepared by the queue can have no raw attempts, so this path
// retains its producer attribution. JSON body/detail receives key redaction and
// shrinking, messages mask supported credential queries, and plain
// text/endpoint strings remain producer responsibility. Nonpositive upstream
// status is cleared outside provider_auth; raw attempts are then copied,
// bounded, serialized, and released.
func (s *OpsService) prepareErrorLogInput(ctx context.Context, entry *OpsInsertErrorLogInput) (*OpsInsertErrorLogInput, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if !s.IsMonitoringEnabled(ctx) {
		return nil, false, nil
	}
	if s.opsRepo == nil {
		return nil, false, nil
	}

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	entry.ErrorPhase = strings.TrimSpace(entry.ErrorPhase)
	entry.ErrorType = strings.TrimSpace(entry.ErrorType)
	if entry.ErrorPhase == "" {
		entry.ErrorPhase = "internal"
	}
	if entry.ErrorType == "" {
		entry.ErrorType = "api_error"
	}

	// Inspect only the newest nonnil raw attempt before serialization. A
	// credential failure replaces earlier inference attribution and stores
	// explicit upstream zero; already serialized queue entries have no raw list to
	// inspect.
	for _, last := range slices.Backward(entry.UpstreamErrors) {
		if last == nil {
			continue
		}
		if last.Stage == string("provider_auth") {
			entry.ErrorPhase = string("provider_auth")
			entry.ErrorOwner = "provider"
			entry.ErrorSource = "gateway"
			code := 0
			entry.UpstreamStatusCode = &code
			entry.UpstreamErrorMessage = nil
			if message := strings.TrimSpace(last.Message); message != "" {
				entry.UpstreamErrorMessage = &message
			}
			entry.UpstreamErrorDetail = nil
			if detail := strings.TrimSpace(last.Detail); detail != "" {
				entry.UpstreamErrorDetail = &detail
			}
		}
		break
	}

	// Prepare supported JSON credential keys and shrink oversized diagnostics
	// before storage. Plain text follows bounded pass-through, so producers handle
	// credentials outside recognized JSON fields.
	if strings.TrimSpace(entry.ErrorBody) != "" {
		sanitized, _ := sanitizeErrorBodyForStorage(entry.ErrorBody, opsMaxStoredErrorBodyBytes)
		entry.ErrorBody = sanitized
	}

	// Keep explicit upstream zero for credential acquisition while clearing
	// nonpositive ordinary status. Prepare selected message queries and JSON
	// details independently of the final client status.
	if entry.UpstreamStatusCode != nil && *entry.UpstreamStatusCode <= 0 && entry.ErrorPhase != string("provider_auth") {
		entry.UpstreamStatusCode = nil
	}
	if entry.UpstreamErrorMessage != nil {
		msg := strings.TrimSpace(*entry.UpstreamErrorMessage)
		msg = logredact.SanitizeUpstreamQueries(msg)
		// Keep the selected message within its UTF-8 byte budget after query redaction.
		msg = truncateString(msg, 2048)
		if strings.TrimSpace(msg) == "" {
			entry.UpstreamErrorMessage = nil
		} else {
			entry.UpstreamErrorMessage = &msg
		}
	}
	if entry.UpstreamErrorDetail != nil {
		detail := strings.TrimSpace(*entry.UpstreamErrorDetail)
		if detail == "" {
			entry.UpstreamErrorDetail = nil
		} else {
			sanitized, _ := sanitizeErrorBodyForStorage(detail, opsMaxStoredErrorBodyBytes)
			if strings.TrimSpace(sanitized) == "" {
				entry.UpstreamErrorDetail = nil
			} else {
				entry.UpstreamErrorDetail = &sanitized
			}
		}
	}

	if err := sanitizeOpsUpstreamErrors(entry); err != nil {
		return nil, false, err
	}

	return entry, true, nil
}

// sanitizeOpsUpstreamErrors copies and prepares at most the trailing sixteen
// raw attempt entries before replacing their serialized representation. Nil
// entry or an empty raw list returns nil without changing a supplied JSON
// value. Nil events and events lacking status/message/detail after preparation
// are omitted, even when they carry endpoint or provider metadata.
//
// Known text fields are trimmed and byte-bounded, negative
// IDs/status/timestamps become zero, JSON body/detail credential keys are
// redacted, and message queries use their supported sanitizer. UpstreamURL is
// bounded rather than cleaned of query/path credentials here. Serialization
// replaces UpstreamErrorsJSON and clears UpstreamErrors without mutating the
// caller's event objects. Marshal failure can produce an absent JSON value
// through the serializer, while this helper still returns nil.
func sanitizeOpsUpstreamErrors(entry *OpsInsertErrorLogInput) error {
	if entry == nil || len(entry.UpstreamErrors) == 0 {
		return nil
	}

	// Keep at most sixteen trailing raw attempt slots; nil or empty events within
	// those slots do not cause earlier attempts to be reintroduced.
	const maxEvents = 16
	events := entry.UpstreamErrors
	if len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}

	sanitized := make([]*OpsUpstreamErrorEvent, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		// Copy this event before trimming and bounding fields so preparation leaves
		// its caller-owned object intact, while the enclosing entry replaces raw and
		// serialized references.
		out := *ev

		// Bound attribution and endpoint labels by UTF-8 bytes before serialization.
		// Endpoint text is shortened here without redacting credentials inside it.
		out.Platform = truncateString(strings.TrimSpace(out.Platform), 32)
		out.ProviderName = truncateString(strings.TrimSpace(out.ProviderName), 128)
		out.UpstreamRequestID = truncateString(strings.TrimSpace(out.UpstreamRequestID), 128)
		out.UpstreamURL = truncateString(strings.TrimSpace(out.UpstreamURL), 2048)
		if body := strings.TrimSpace(out.UpstreamResponseBody); body != "" {
			out.UpstreamResponseBody, _ = sanitizeErrorBodyForStorage(body, OpsErrorLogQueueBodyMaxBytes)
		} else {
			out.UpstreamResponseBody = ""
		}
		out.Kind = truncateString(strings.TrimSpace(out.Kind), 64)
		out.Stage = truncateString(strings.TrimSpace(out.Stage), 64)
		out.Scope = truncateString(strings.TrimSpace(out.Scope), 64)
		out.Reason = truncateString(strings.TrimSpace(out.Reason), 128)

		if out.ProviderID < 0 {
			out.ProviderID = 0
		}
		if out.UpstreamStatusCode < 0 {
			out.UpstreamStatusCode = 0
		}
		if out.AtUnixMs < 0 {
			out.AtUnixMs = 0
		}

		// Attempt messages keep a separate UTF-8 byte budget after query redaction.
		msg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(out.Message))
		msg = truncateString(msg, 2048)
		out.Message = msg

		detail := strings.TrimSpace(out.Detail)
		if detail != "" {
			// Apply the queue byte budget to supported JSON detail even during storage
			// preparation, keeping serialized attempt costs bounded before the raw slice
			// is released.
			sanitizedDetail, _ := sanitizeErrorBodyForStorage(detail, OpsErrorLogQueueBodyMaxBytes)
			out.Detail = sanitizedDetail
		} else {
			out.Detail = ""
		}

		// Keep an event only when status, message, or detail remains diagnostic.
		// Provider/endpoint metadata alone does not retain an event, including a
		// zero-status credential entry with no text.
		if out.UpstreamStatusCode == 0 && out.Message == "" && out.Detail == "" {
			continue
		}

		evCopy := out
		sanitized = append(sanitized, &evCopy)
	}

	// Replace serialized attempts before releasing the raw slice. An empty or
	// failed serialized result becomes nil through marshalOpsUpstreamErrors, while
	// this helper returns successful preparation.
	entry.UpstreamErrorsJSON = marshalOpsUpstreamErrors(sanitized)
	entry.UpstreamErrors = nil
	return nil
}

// GetErrorLogs returns administrator observations after monitoring validation,
// using an empty one/twenty page when storage is absent. A shallow filter copy
// receives fresh normalized ignored statuses; pointer/slice selections remain
// caller-owned read-only inputs, and repository errors are logged and returned.
func (s *OpsService) GetErrorLogs(ctx context.Context, filter *OpsErrorLogFilter) (*OpsErrorLogList, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if s.opsRepo == nil {
		return &OpsErrorLogList{Errors: []*OpsErrorLog{}, Total: 0, Page: 1, PageSize: 20}, nil
	}
	if filter == nil {
		filter = &OpsErrorLogFilter{}
	} else {
		filterCopy := *filter
		filter = &filterCopy
	}
	if filter.IgnoredStatusCodes == nil {
		filter.IgnoredStatusCodes = s.resolveOpsIgnoredStatusCodes(ctx)
	} else {
		filter.IgnoredStatusCodes = NormalizeOpsIgnoredStatusCodes(filter.IgnoredStatusCodes)
	}
	result, err := s.opsRepo.ListErrorLogs(ctx, filter)
	if err != nil {
		logger.LegacyPrintf("service.ops", "[Ops] GetErrorLogs failed: %v", err)
		return nil, err
	}

	return result, nil
}

// ListUserErrorRequests returns user-facing categories and requested-model
// display values for a supplied authenticated positive user identity. A usable
// nonnil service and repository are required; this method does not validate
// that user ID, and PostgreSQL omits nonpositive identity predicates. User
// error visibility is enforced by the HTTP caller independently of monitoring.
//
// A shallow filter copy replaces UserID, keeps the optional key restriction,
// selects all categories, excludes probes, and enables escaped fuzzy model
// matching. Administrator email/owner/source and explicit phase restrictions
// are cleared, recovered-provider opt-in is disabled, and category arrays
// remain supplied. Shared input pointers/slices are read-only during the query;
// count/page reflect the repository's separate statements and conversion uses
// normalized user labels rather than exposing administrator fields.
func (s *OpsService) ListUserErrorRequests(ctx context.Context, userID int64, filter *OpsErrorLogFilter) (*UserErrorRequestList, error) {
	if filter == nil {
		filter = &OpsErrorLogFilter{}
	}
	f := *filter // Copy scalar filters; query code treats shared slices as read-only.
	filter = &f
	uid := userID
	filter.UserID = &uid
	// Keep the supplied key restriction conjunctive with this positive user
	// identity. PostgreSQL ignores nonpositive identity predicates, so the HTTP
	// caller supplies the authenticated positive owner.
	filter.View = "all"
	filter.ExcludeCountTokens = true
	filter.ModelFuzzy = true // escaped user model substring selection
	// Clear administrator-only identity search and attribution selectors before
	// querying. User categories still pass their supplied phase/type arrays, while
	// the explicit phase is cleared below.
	filter.UserQuery = ""
	filter.Owner = ""
	filter.Source = ""
	// Use category arrays without the separate explicit phase, and disable
	// recovered-provider admission. PostgreSQL still retains cyber_policy status
	// exemptions for streamed policy rejection visibility.
	filter.Phase = ""
	filter.IncludeRecoveredUpstream = false

	list, err := s.opsRepo.ListErrorLogs(ctx, filter)
	if err != nil {
		return nil, err
	}
	items := make([]*UserErrorRequest, 0, len(list.Errors))
	for _, e := range list.Errors {
		if r := ToUserErrorRequest(e); r != nil {
			items = append(items, r)
		}
	}
	return &UserErrorRequestList{
		Items:    items,
		Total:    list.Total,
		Page:     list.Page,
		PageSize: list.PageSize,
	}, nil
}

// GetErrorLogByID loads administrator diagnostic detail after monitoring
// validation. Missing storage or a classified no-rows error returns not found;
// other read failures gain an internal-error cause. The repository is expected
// to return detail on successful reads.
func (s *OpsService) GetErrorLogByID(ctx context.Context, id int64) (*OpsErrorLogDetail, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if s.opsRepo == nil {
		return nil, infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
	}
	detail, err := s.opsRepo.GetErrorLogByID(ctx, id)
	if err != nil {
		if s.isNotFound(err) {
			return nil, infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
		}
		return nil, infraerrors.InternalServer("OPS_ERROR_LOAD_FAILED", "Failed to load ops error log").WithCause(err)
	}
	return detail, nil
}

// GetUserErrorRequestDetail loads one positive error ID and compares its
// recorded UserID directly with the caller identity before conversion. The HTTP
// caller supplies the authenticated user and enforces user-view visibility;
// this method does not apply the monitoring gate. Missing storage, missing
// record, or another owner returns not found.
//
// A successful repository read supplies nonnil detail. Ownership is not derived
// from API-key metadata or deleted-key attribution. The owner receives
// user-facing classification with the retained diagnostic body and upstream
// fields supplied to ToUserErrorRequestDetail; that conversion is not a new
// universal body-redaction pass. Other storage failures return an internal
// error with cause.
func (s *OpsService) GetUserErrorRequestDetail(ctx context.Context, userID, id int64) (*UserErrorRequestDetail, error) {
	if s.opsRepo == nil {
		return nil, infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
	}
	if id <= 0 {
		return nil, infraerrors.BadRequest("OPS_ERROR_INVALID_ID", "invalid error id")
	}
	detail, err := s.opsRepo.GetErrorLogByID(ctx, id)
	if err != nil {
		if s.isNotFound(err) {
			return nil, infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
		}
		return nil, infraerrors.InternalServer("OPS_ERROR_LOAD_FAILED", "Failed to load ops error log").WithCause(err)
	}
	// Compare the stored user association directly before returning diagnostic
	// body and upstream fields. Current API-key ownership or deleted-key
	// attribution does not replace this record-level check.
	ownedDirectly := detail.UserID != nil && *detail.UserID == userID
	if !ownedDirectly {
		return nil, infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
	}
	return ToUserErrorRequestDetail(detail), nil
}

// UpdateErrorResolution validates monitoring and a positive error identity
// before a separate existence read and resolution write. Missing storage is
// unavailable; classified absence becomes not found and other lookup failures
// gain internal cause. Deletion between read and write can still make the
// PostgreSQL update affect no row.
//
// The actor pointer passes through as supplied, while storage selects current
// UTC resolution time for true and NULL for false because this service passes
// no timestamp. Setting false does not independently clear a supplied actor
// identity.
func (s *OpsService) UpdateErrorResolution(ctx context.Context, errorID int64, resolved bool, resolvedByUserID *int64) error {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return err
	}
	if s.opsRepo == nil {
		return infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}
	if errorID <= 0 {
		return infraerrors.BadRequest("OPS_ERROR_INVALID_ID", "invalid error id")
	}
	// Read existence before changing resolution, but keep the read and UPDATE
	// independent. Concurrent deletion can still make the later PostgreSQL write
	// affect no rows.
	if _, err := s.opsRepo.GetErrorLogByID(ctx, errorID); err != nil {
		if s.isNotFound(err) {
			return infraerrors.NotFound("OPS_ERROR_NOT_FOUND", "ops error log not found")
		}
		return infraerrors.InternalServer("OPS_ERROR_LOAD_FAILED", "Failed to load ops error log").WithCause(err)
	}
	return s.opsRepo.UpdateErrorResolution(ctx, errorID, resolved, resolvedByUserID, nil)
}

// sanitizeAndTrimJSONPayload decodes JSON, redacts recognized keys, and selects
// a compact valid representation with original input-byte count. Empty input
// returns empty, and decode/initial encode failure returns empty for the
// caller's plain-text path. Standard decoding uses JSON values such as float64
// numbers rather than preserving source number spelling.
//
// Oversized objects first keep a conversation suffix, then selected essential
// fields, then replace large named payload fields with empty placeholders. If
// none fits, a minimal payload_truncated object is returned, even when maxBytes
// is smaller than that object. Redaction itself does not set truncated; the
// flag marks size-reduction handling. Callers use the queue/storage budgets
// that can hold the marker, and input byte count remains separate from encoded
// output length.
func sanitizeAndTrimJSONPayload(raw []byte, maxBytes int) (jsonString string, truncated bool, bytesLen int) {
	bytesLen = len(raw)
	if len(raw) == 0 {
		return "", false, 0
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// Leave malformed JSON to the caller's bounded plain-text path. That path
		// does not infer credential keys, so producers prepare sensitive text
		// separately.
		return "", false, bytesLen
	}

	decoded = redactSensitiveJSON(decoded)

	// Re-encode decoded JSON after key redaction so replacements remain valid
	// JSON. Numeric spelling follows ordinary decoder values, and redaction alone
	// does not mark truncation.
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", false, bytesLen
	}
	if len(encoded) <= maxBytes {
		return string(encoded), false, bytesLen
	}

	// Try a conversation suffix before discarding other object fields. Its
	// smallest suffix can still exceed budget, so subsequent essential-field and
	// placeholder phases perform separate checks.
	if root, ok := decoded.(map[string]any); ok {
		if trimmed, ok := trimConversationArrays(root, maxBytes); ok {
			encoded2, err2 := json.Marshal(trimmed)
			if err2 == nil && len(encoded2) <= maxBytes {
				return string(encoded2), true, bytesLen
			}
			// The selected suffix still cannot fit or encode, so retain it for the later
			// placeholder phase; essential-field selection reads the original root
			// below.
			decoded = trimmed
		}

		essential := shrinkToEssentials(root)
		encoded3, err3 := json.Marshal(essential)
		if err3 == nil && len(encoded3) <= maxBytes {
			return string(encoded3), true, bytesLen
		}
	}

	// Retain named payload keys while replacing selected large arrays/strings with
	// empty placeholders and adding the truncation marker. This still must pass
	// the same encoded byte check.
	if root, ok := decoded.(map[string]any); ok {
		placeholder := querycache.ShallowMap(root)
		placeholder["payload_truncated"] = true

		for _, k := range []string{"messages", "contents", "input", "prompt"} {
			if _, exists := placeholder[k]; exists {
				placeholder[k] = []any{}
			}
		}
		for _, k := range []string{"text"} {
			if _, exists := placeholder[k]; exists {
				placeholder[k] = ""
			}
		}

		encoded4, err4 := json.Marshal(placeholder)
		if err4 == nil {
			if len(encoded4) <= maxBytes {
				return string(encoded4), true, bytesLen
			}
		}
	}

	// Use a minimal valid marker after object candidates fail. Its fixed encoding
	// can exceed an unusually tiny requested budget; production queue/storage
	// budgets can hold it.
	encoded4, err4 := json.Marshal(map[string]any{"payload_truncated": true})
	if err4 != nil {
		return "", true, bytesLen
	}
	return string(encoded4), true, bytesLen
}

// redactSensitiveJSON recursively copies concrete JSON-decoder maps and slices,
// replacing recognized key values with [REDACTED]. Keys retain their original
// spelling even though isSensitiveKey normalizes names for its decision.
// Callers supply the finite acyclic values produced by JSON decoding.
//
// Other values are returned as supplied, including strings and separately
// defined map or slice types. Those values can remain shared and are not
// recursively transformed. This distinction makes ordinary decoded JSON the
// input for nested credential-key preparation rather than establishing a
// generic deep-copy or leaf-string redaction guarantee.
func redactSensitiveJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			if isSensitiveKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactSensitiveJSON(vv)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, vv := range t {
			out = append(out, redactSensitiveJSON(vv))
		}
		return out
	default:
		return v
	}
}

// isSensitiveKey tests trimmed case-folded JSON names against credential
// patterns after a token-count/budget allowlist. Empty names are accepted as
// nonsensitive. The allowlisted telemetry fields remain visible even though
// broader suffix/substring matching would otherwise treat token text as
// sensitive.
//
// Exact credential names are followed by suffix and substring matching. The
// decision inspects names rather than value contents, so a matching nonsecret
// field can still be redacted while a secret inside an unrelated string field
// remains supplied. redactSensitiveJSON retains the original key spelling when
// applying this result.
func isSensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}

	// Keep known token-count and generation-budget fields observable before
	// broader credential-name matching, so operational token measurements are not
	// replaced just because their names contain token.
	switch k {
	case "max_tokens",
		"max_output_tokens",
		"max_input_tokens",
		"max_completion_tokens",
		"max_tokens_to_sample",
		"budget_tokens",
		"prompt_tokens",
		"completion_tokens",
		"input_tokens",
		"output_tokens",
		"total_tokens",
		"token_count",
		"cache_creation_input_tokens",
		"cache_read_input_tokens":
		return false
	}

	switch k {
	case "authorization",
		"proxy-authorization",
		"x-api-key",
		"api_key",
		"apikey",
		"access_token",
		"refresh_token",
		"id_token",
		"session_token",
		"token",
		"password",
		"passwd",
		"passphrase",
		"secret",
		"client_secret",
		"private_key",
		"jwt",
		"signature",
		"accesskeyid",
		"secretaccesskey":
		return true
	}

	for _, suffix := range []string{
		"_secret",
		"_token",
		"_id_token",
		"_session_token",
		"_password",
		"_passwd",
		"_passphrase",
		"_key",
		"secret_key",
		"private_key",
	} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}

	for _, sub := range []string{
		"secret",
		"token",
		"password",
		"passwd",
		"passphrase",
		"privatekey",
		"private_key",
		"apikey",
		"api_key",
		"accesskeyid",
		"secretaccesskey",
		"bearer",
		"cookie",
		"credential",
		"session",
		"jwt",
		"signature",
	} {
		if strings.Contains(k, sub) {
			return true
		}
	}

	return false
}

// trimConversationArrays tries messages before contents and returns the first
// usable suffix representation from trimArrayField. A usable array is selected
// even when its smallest suffix still exceeds maxBytes, so the caller continues
// later shrinking phases.
func trimConversationArrays(root map[string]any, maxBytes int) (map[string]any, bool) {
	// Try the messages representation before contents. A selected messages array
	// wins even if its smallest suffix requires the caller's later shrinking
	// phases.
	if out, ok := trimArrayField(root, "messages", maxBytes); ok {
		return out, true
	}
	if out, ok := trimArrayField(root, "contents", maxBytes); ok {
		return out, true
	}
	return root, false
}

// trimArrayField returns a shallow-map candidate holding the earliest fitting
// suffix of a concrete nonempty []any field. Missing or differently typed
// arrays return false; callers use JSON-compatible values and a stable root
// during repeated serialization. At least the last element remains in each
// candidate.
//
// Binary search assumes dropping earlier array elements reduces encoded size,
// and marshal errors try a shorter suffix. The returned map has new top-level
// storage but shares nested values and the chosen array backing storage. If
// even one element cannot fit, that smallest suffix returns true for the owning
// shrink sequence to continue rather than treating the budget as satisfied.
func trimArrayField(root map[string]any, field string, maxBytes int) (map[string]any, bool) {
	raw, ok := root[field]
	if !ok {
		return nil, false
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}

	// Binary search drops earlier conversation elements while retaining at least
	// the final one. JSON-compatible stable values make suffix size decrease as
	// elements are removed.
	lo := 0
	hi := len(arr) - 1 // inclusive final-element retention limit

	var best map[string]any
	found := false

	for lo <= hi {
		mid := (lo + hi) / 2
		candidateArr := arr[mid:]
		if len(candidateArr) == 0 {
			lo = mid + 1
			continue
		}

		next := querycache.ShallowMap(root)
		next[field] = candidateArr
		encoded, err := json.Marshal(next)
		if err != nil {
			// Try a shorter suffix after encoding failure. This can remove an
			// unsupported earlier value, while a failing retained value may still reach
			// later fallback handling.
			lo = mid + 1
			continue
		}

		if len(encoded) <= maxBytes {
			best = next
			found = true
			// A fitting suffix becomes the current best candidate; search earlier starts
			// to retain more context while the caller's encoded budget still fits.
			hi = mid - 1
			continue
		}

		// This suffix remains oversized, so move its start forward. Keep the final
		// element as the smallest candidate for the owning helper's next shrinking
		// phase.
		lo = mid + 1
	}

	if found {
		return best, true
	}

	// Return the final-element candidate even when it exceeds budget so the caller
	// can retain context through essential-field selection before using empty
	// placeholders.
	next := querycache.ShallowMap(root)
	next[field] = arr[len(arr)-1:]
	return next, true
}

// shrinkToEssentials returns a new top-level map retaining selected model,
// generation, and thinking fields plus the final messages/contents elements
// when present. Nested values remain shared read-only, and the result is a
// candidate for later size checking rather than a guaranteed byte-bounded
// payload.
//
// Messages and contents are handled independently, so a payload carrying both
// representations keeps one newest element from each. Their new one-element
// slices still contain the original shared values.
func shrinkToEssentials(root map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range []string{
		"model",
		"stream",
		"max_tokens",
		"max_output_tokens",
		"max_input_tokens",
		"max_completion_tokens",
		"thinking",
		"temperature",
		"top_p",
		"top_k",
	} {
		if v, ok := root[key]; ok {
			out[key] = v
		}
	}

	// Carry the newest element of each available conversation representation into
	// essential output. Other nested chosen values remain shared until the caller
	// serializes its candidate.
	if v, ok := root["messages"]; ok {
		if arr, ok := v.([]any); ok && len(arr) > 0 {
			out["messages"] = []any{arr[len(arr)-1]}
		}
	}
	if v, ok := root["contents"]; ok {
		if arr, ok := v.([]any); ok && len(arr) > 0 {
			out["contents"] = []any{arr[len(arr)-1]}
		}
	}
	return out
}

// sanitizeErrorBodyForStorage trims diagnostic text and prefers credential-key
// redaction with valid-JSON shrinking. Blank text returns empty without
// truncation. Non-JSON text uses a UTF-8 prefix only for a positive exceeded
// byte limit, while nonpositive limits leave plain text supplied.
//
// Plain content and JSON string values receive no generic credential-pattern
// scrub here, so producers prepare sensitive text before this helper. The JSON
// branch can return its minimal marker above an unusually tiny or nonpositive
// maxBytes; queue/storage callers supply larger fixed budgets. The boolean
// reports size-reduction handling, not whether credential keys were replaced.
func sanitizeErrorBodyForStorage(raw string, maxBytes int) (sanitized string, truncated bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	// Prefer valid JSON output so recognized credential keys can be replaced
	// before size reduction. JSON strings and other leaf values keep their
	// supplied diagnostic content.
	if out, trunc, _ := sanitizeAndTrimJSONPayload([]byte(raw), maxBytes); out != "" {
		return out, trunc
	}

	// Bound non-JSON text only when a positive limit is exceeded. The UTF-8 prefix
	// path retains fitting content, so producer redaction remains necessary for
	// plain credentials.
	if maxBytes > 0 && len(raw) > maxBytes {
		return truncateString(raw, maxBytes), true
	}
	return raw, false
}

// isContextDoneError recognizes an error wrapping context.Canceled or a context
// already canceled/deadlined. Nil context otherwise returns false, and an error
// wrapping DeadlineExceeded alone is insufficient. Batch logging calls it after
// a write failure.
func isContextDoneError(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx == nil {
		return false
	}
	ctxErr := ctx.Err()
	// Driver errors can omit standard cancellation identity. Consult context state
	// as a second signal, but do not classify an error-only deadline when that
	// context remains live.
	return errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded)
}

// truncateString delegates a byte prefix to logredact.TruncateUTF8, returning
// empty for nonpositive limits. Fitting input stays supplied even with invalid
// UTF-8; the shortened path removes trailing bytes until the whole prefix is
// valid.
func truncateString(s string, max int) string { return logredact.TruncateUTF8(s, max) }
