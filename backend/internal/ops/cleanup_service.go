package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// cleanup_service.go schedules retention cleanup and coordinates cron
// replacement, maintenance ownership, and shutdown.
// Ops cleanup schedules observation deletion from deployment values overlaid
// with saved retention preferences. A valid decoded advanced record can disable
// scheduling and set zero-day clearing even when fields were omitted, while a
// missing or malformed record retains deployment choices. System-log retention
// comes separately from positive runtime logger retention.
//
// Reload retires the old cron before creating a replacement and keeps its
// completion signal after a local wait times out. Stop waits for current and
// retired cron jobs outside the settings lock; it does not cancel their
// background run contexts. Every selected run refreshes retention, obtains
// leader admission and then the common database-maintenance lock, and processes
// trusted targets in order. Successful earlier targets remain committed when a
// later target fails, and counters include only targets that returned
// successfully rather than all partial persisted work.

const (
	// opsCleanupDefaultSchedule supplies the five-field schedule when effective
	// text is blank; timezone resolution happens when the service builds cron.
	opsCleanupDefaultSchedule = "0 3 * * *"

	// opsCleanupDefaultBatchSize and opsCleanupDefaultBatchPause supply bounded
	// batch pacing defaults. OpsCleanupDefaultBatchSize and
	// OpsCleanupDefaultBatchPause expose the same values to adapters needing this
	// policy.
	opsCleanupDefaultBatchSize  = 1000
	OpsCleanupDefaultBatchSize  = opsCleanupDefaultBatchSize
	opsCleanupDefaultBatchPause = 200 * time.Millisecond
	OpsCleanupDefaultBatchPause = opsCleanupDefaultBatchPause

	// opsCleanupJobName identifies cleanup heartbeats independently of leader
	// ownership and the shared database-maintenance lock used during destructive
	// target work.
	opsCleanupJobName = "ops_cleanup"

	// opsCleanupLeaderLockKeyDefault and opsCleanupLeaderLockTTLDefault identify
	// the unrenewed Redis lease. The same key selects advisory admission when
	// Redis is absent or fails.
	opsCleanupLeaderLockKeyDefault = "ops:cleanup:leader"
	opsCleanupLeaderLockTTLDefault = 30 * time.Minute
)

// ErrDatabaseMaintenanceBusy reports refusal by the common maintenance lock;
// opsCleanupCronParser accepts minute/hour/day/month/weekday text for
// replacement schedules, with no seconds field.
var (
	ErrDatabaseMaintenanceBusy = infraerrors.Conflict("MAINTENANCE_BUSY", "another database maintenance task is running")
	opsCleanupCronParser       = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
)

// CleanupBackend supplies leader advisory admission, shared maintenance
// ownership, and trusted target deletion.
type CleanupBackend interface {
	// AdvisoryLocker supplies the named database alternative for cleanup leader
	// admission.
	AdvisoryLocker
	// AcquireMaintenance attempts the shared database-maintenance lock and returns
	// release after successful ownership. Callers release once after target work;
	// refusal skips cleanup and error records a failed job heartbeat.
	AcquireMaintenance(context.Context) (func(), bool, error)
	// RunTarget applies truncation or cutoff deletion to one trusted
	// table/time-column pair, with optional date casting and supplied batch/pause
	// controls. PostgreSQL counts committed CTID batches and completed throttle
	// waits, or estimated rows for truncation.
	//
	// Later failures can return partial progress with an error because earlier
	// statements committed independently. Callers keep identifiers internal and
	// use ctx for target cancellation. OpsCleanupService records a target result
	// in its aggregate only after a nil error, so a failed target can leave
	// persisted deletion outside reported totals.
	RunTarget(context.Context, bool, time.Time, string, string, bool, int, time.Duration) (CleanupTargetResult, error)
}

// CleanupTargetResult carries one backend target's reported deletion, batch,
// and throttle observations.
type CleanupTargetResult struct {
	// Deleted and Batches can include estimated truncation values or committed
	// partial delete progress. Throttled sums completed waits; an errored target's
	// observations are not added by runCleanupOnce.
	Deleted, Batches int64         // Deleted: returned row count; Batches: completed batch count
	Throttled        time.Duration // completed target throttle duration
}

// OpsCleanupService owns schedule replacement and joins retired cleanup jobs
// across reloads. Dependencies and options remain bound before startup; current
// target work obtains database maintenance ownership separately from its leader
// decision.
type OpsCleanupService struct {
	// drainingCrons retains retired-runner completion contexts for later pruning
	// and Stop waiting. Stop waits but leaves this collection retained; copied
	// contexts keep their independent signals.
	drainingCrons []context.Context  // retained retired-cron completion contexts
	opsRepo       JobHeartbeatWriter // cleanup heartbeat persistence dependency
	db            CleanupBackend     // maintenance and target cleanup backend
	redisClient   LeaderLeaseCache   // preferred Redis leader adapter
	// cfg retains deployment cleanup, timezone, and reporting options. Callers
	// keep it immutable during cron work; runCleanupOnce needs nonnil options
	// before processing any target.
	cfg         *Options
	settingRepo SettingValueReader // saved retention preference reader

	instanceID string // Redis leader ownership identity

	// mu protects cron, started, stopped, effective, and drainingCrons during
	// replacement and refresh. Stop seals replacement before copying drains, then waits
	// outside the lock so jobs can finish preference reads.
	mu        sync.Mutex     // schedule and effective-settings mutex
	cron      *cron.Cron     // current scheduled cleanup runner
	started   bool           // startup registration has occurred
	stopped   bool           // schedule lifecycle permanently closed
	effective CleanupOptions // current effective retention/pacing values

	warnNoRedisOnce sync.Once // single Redis alternative diagnostic
}

// reportCleanupCompleted synchronously invokes the optional successful-cleanup
// callback with its formatted counts. Nil options or callback is skipped;
// callback execution follows heartbeat attempt and remains outside the
// backend's target transactions.
func reportCleanupCompleted(o *Options, counts string) {
	if o != nil && o.CleanupCompleted != nil {
		o.CleanupCompleted(counts)
	}
}

// NewOpsCleanupService retains repositories, leader adapter, options, and
// settings with a Redis owner identity. It allocates no cron or background
// work; bind usable persistence/backend dependencies before Start and keep
// options stable during operation.
func NewOpsCleanupService(
	opsRepo JobHeartbeatWriter,
	db CleanupBackend,
	redisClient LeaderLeaseCache,
	cfg *Options,
	settingRepo SettingValueReader,
) *OpsCleanupService {
	return &OpsCleanupService{
		opsRepo:     opsRepo,
		db:          db,
		redisClient: redisClient,
		cfg:         cfg,
		settingRepo: settingRepo,
		instanceID:  uuid.NewString(),
	}
}

// Start marks scheduling started once when hard Ops availability and required
// persistence/backend permit construction. It holds mu through settings
// computation and schedule replacement using a background context. Initial
// schedule failure is reported but the started flag remains set.
//
// Repeated Start then does not retry that failed registration; Reload supplies
// later retry while the service remains open. Stop permanently seals future
// startup. The constructor is inert, and app calls Start before scheduled work
// is expected; runtime monitoring soft-switch state is not consulted by this
// cleanup lifecycle.
func (s *OpsCleanupService) Start() {
	if s == nil {
		return
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}
	if s.opsRepo == nil || s.db == nil {
		reportOptions(s.cfg, "[OpsCleanup] not started (missing deps)")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	// Keep started true before applying the schedule so one startup cannot
	// register twice. A failed initial replacement then requires Reload rather
	// than another Start call.
	s.started = true
	if err := s.applyScheduleLocked(context.Background()); err != nil {
		reportOptions(s.cfg, "[OpsCleanup] not started: %v", err)
	}
}

// Stop permanently seals schedule replacement, retires the current cron, and
// waits for captured current/retired job completion outside mu. Repeated calls
// can wait again on retained contexts. Jobs that needed mu for preference
// refresh can proceed once the lock is released.
//
// The method supplies no caller deadline and does not cancel background run
// contexts. Retired jobs from earlier Reload remain included after local
// retirement waits timed out. Completion joins cron-owned callbacks, while
// direct private calls to runScheduled have their own caller ownership; the
// service cannot restart after this lifecycle closure.
func (s *OpsCleanupService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	if s.cron != nil {
		s.drainingCrons = append(s.drainingCrons, s.cron.Stop())
		s.cron = nil
	}
	// Copy every retained completion context before releasing mu, then join
	// outside it so cron callbacks waiting for preference refresh can proceed.
	pending := append([]context.Context(nil), s.drainingCrons...)
	s.mu.Unlock()
	for _, ctx := range pending {
		<-ctx.Done()
	}
}

// stopCronLocked retires the current cron and preserves its completion context
// while the caller holds mu. Completed earlier retired contexts are pruned, and
// this retirement waits locally for up to three seconds before reporting
// timeout.
//
// The timeout leaves active callbacks running and their contexts retained for
// final Stop. An old callback waiting to refresh preferences under the same
// mutex may need that local wait to expire before it can finish. Setting cron
// nil retires scheduling, rather than canceling the callback's separate
// background run context.
func (s *OpsCleanupService) stopCronLocked() {
	if s.cron == nil {
		return
	}
	// Retire future scheduling but retain cron's job-completion signal. Prune
	// finished older runners and preserve unfinished ones for Stop after a local
	// timeout.
	ctx := s.cron.Stop()
	pending := s.drainingCrons[:0]
	for _, previous := range s.drainingCrons {
		select {
		case <-previous.Done():
		default:
			pending = append(pending, previous)
		}
	}
	s.drainingCrons = append(pending, ctx)
	select {
	case <-ctx.Done():
	case <-time.After(opsCleanupCronStopTimeout):
		reportOptions(s.cfg, "[OpsCleanup] cron stop timed out")
	}
	s.cron = nil
}

// applyScheduleLocked computes current preferences, retires the old cron, and
// builds a new enabled schedule while the caller holds mu. Disabled settings
// return after retirement. Blank schedule selects the default, and invalid
// configured timezone leaves time.Local through the existing location policy.
//
// Schedule parsing follows retirement, so invalid new text leaves no
// replacement even though effective preferences were already updated. A
// successful cron starts future minute-resolution jobs and becomes the current
// runner. The old timed-out jobs can still finish alongside later callbacks;
// all destructive runs attempt the common maintenance lock. ctx governs
// settings reads rather than canceling previously admitted run contexts.
func (s *OpsCleanupService) applyScheduleLocked(ctx context.Context) error {
	// Compute and publish effective choices before retiring the old cron. Parsing
	// follows retirement, so invalid replacement text does not leave the old
	// schedule installed.
	s.computeEffectiveLocked(ctx)
	s.stopCronLocked()

	if !s.effective.Enabled {
		reportOptions(s.cfg, "[OpsCleanup] cron disabled by settings")
		return nil
	}

	schedule := strings.TrimSpace(s.effective.Schedule)
	if schedule == "" {
		schedule = opsCleanupDefaultSchedule
	}

	// Use the process timezone unless configured location parsing succeeds.
	// Invalid location text is suppressed here, leaving the existing
	// process-calendar choice for cron.
	loc := time.Local
	if s.cfg != nil && strings.TrimSpace(s.cfg.Timezone) != "" {
		if parsed, err := time.LoadLocation(strings.TrimSpace(s.cfg.Timezone)); err == nil && parsed != nil {
			loc = parsed
		}
	}

	c := cron.New(cron.WithParser(opsCleanupCronParser), cron.WithLocation(loc))
	if _, err := c.AddFunc(schedule, func() { s.runScheduled() }); err != nil {
		return fmt.Errorf("invalid schedule %q: %w", schedule, err)
	}
	// Start minute-resolution future scheduling only after successful parsing,
	// then publish this cron as current. Timed-out retired callbacks retain their
	// separate completion contexts.
	c.Start()
	s.cron = c
	reportOptions(s.cfg,
		"[OpsCleanup] scheduled (schedule=%q tz=%s retention_days=err:%d/system:%d/min:%d/hour:%d batch=%d pause_ms=%d)",
		schedule, loc.String(),
		s.effective.ErrorLogRetentionDays,
		s.effective.SystemLogRetentionDays,
		s.effective.MinuteMetricsRetentionDays,
		s.effective.HourlyMetricsRetentionDays,
		s.effective.BatchSize,
		s.effective.BatchPauseMS,
	)
	return nil
}

// Reload rebuilds cleanup from saved preferences after an accepted setting
// change, while holding mu. Nil service, calls before Start, or calls after
// Stop succeed without work. An invalid schedule can fail after retiring the
// prior runner, and target retention also refreshes per run.
func (s *OpsCleanupService) Reload(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return nil
	}
	return s.applyScheduleLocked(ctx)
}

// computeEffectiveLocked overlays saved preferences on deployment cleanup
// values while the caller holds mu or exclusively owns an unstarted fixture.
// Missing/nonpositive pacing receives defaults. Absent repository, failed
// advanced reads, blank text, or malformed advanced JSON retains deployment
// choices. A nil read context selects background settings reads.
//
// Valid advanced JSON decodes into zero values rather than the
// advanced-settings default object. Thus valid null/empty records disable
// cleanup and make omitted nonnegative retention fields zero, while negative
// fields keep deployment retention. Nonblank schedule and positive pacing
// override their base values without another range check. System-log retention
// is then read independently and overrides only when parsed positive; no
// overall monitoring soft switch is read.
func (s *OpsCleanupService) computeEffectiveLocked(ctx context.Context) {
	base := CleanupOptions{}
	if s.cfg != nil {
		base = s.cfg.Ops.Cleanup
	}
	if base.BatchSize <= 0 {
		base.BatchSize = opsCleanupDefaultBatchSize
	}
	if base.BatchPauseMS <= 0 {
		// Store the default throttle delay as integer milliseconds.
		base.BatchPauseMS = int(opsCleanupDefaultBatchPause / time.Millisecond)
	}

	if s.settingRepo == nil {
		s.effective = base
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsAdvancedSettings)
	if err != nil {
		if !errors.Is(err, ErrSettingNotFound) {
			reportOptions(s.cfg,
				"[OpsCleanup] read advanced settings failed, using cfg: %v", err)
		}
	} else if strings.TrimSpace(raw) != "" {
		var adv OpsAdvancedSettings
		if err := json.Unmarshal([]byte(raw), &adv); err != nil {
			reportOptions(s.cfg,
				"[OpsCleanup] parse advanced settings failed, using cfg: %v", err)
		} else {
			dr := adv.DataRetention
			base.Enabled = dr.CleanupEnabled
			if sched := strings.TrimSpace(dr.CleanupSchedule); sched != "" {
				base.Schedule = sched
			}
			if dr.CleanupBatchSize > 0 {
				base.BatchSize = dr.CleanupBatchSize
			}
			if dr.CleanupPauseMS > 0 {
				base.BatchPauseMS = dr.CleanupPauseMS
			}
			if dr.ErrorLogRetentionDays >= 0 {
				base.ErrorLogRetentionDays = dr.ErrorLogRetentionDays
			}
			if dr.MinuteMetricsRetentionDays >= 0 {
				base.MinuteMetricsRetentionDays = dr.MinuteMetricsRetentionDays
			}
			if dr.HourlyMetricsRetentionDays >= 0 {
				base.HourlyMetricsRetentionDays = dr.HourlyMetricsRetentionDays
			}
		}
	}

	// Read logger retention separately after advanced overlay, and apply only a
	// positive parsed override. A zero runtime value leaves deployment system-log
	// retention unchanged, unlike advanced zero-day clearing.
	runtimeRaw, runtimeErr := s.settingRepo.GetValue(ctx, SettingKeyOpsRuntimeLogConfig)
	if runtimeErr != nil && !errors.Is(runtimeErr, ErrSettingNotFound) {
		reportOptions(s.cfg,
			"[OpsCleanup] read runtime log config failed, using cfg: %v", runtimeErr)
	} else if strings.TrimSpace(runtimeRaw) != "" {
		var runtimeCfg OpsRuntimeLogConfig
		if err := json.Unmarshal([]byte(runtimeRaw), &runtimeCfg); err != nil {
			reportOptions(s.cfg,
				"[OpsCleanup] parse runtime log config failed, using cfg: %v", err)
		} else if runtimeCfg.RetentionDays > 0 {
			base.SystemLogRetentionDays = runtimeCfg.RetentionDays
		}
	}
	s.effective = base
}

// snapshotEffective returns the current scalar cleanup-options value while
// holding mu. The service must be nonnil; callers use the returned copy for
// target decisions independently of later retention refresh or schedule
// replacement.
func (s *OpsCleanupService) snapshotEffective() CleanupOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effective
}

// refreshEffectiveBeforeRun recomputes effective retention/pacing under mu
// using current stored values. It requires a nonnil service and does not
// replace cron; changed schedule timing requires Reload, while an admitted
// callback can continue with newly disabled settings.
func (s *OpsCleanupService) refreshEffectiveBeforeRun(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.computeEffectiveLocked(ctx)
}

// runScheduled refreshes preferences and attempts cleanup under a thirty-minute
// background context when persistence/backend exist. Leader admission precedes
// common database-maintenance ownership, so refusal prevents target work even
// if another lock system granted leadership. Acquisition error/busy refusal
// records a zero-duration error heartbeat.
//
// Admitted work does not recheck effective Enabled, stopped state, or
// monitoring soft-switch state; already scheduled callbacks can therefore
// finish after disable/reload/stop. runCleanupOnce processes targets with
// current copied retention, retaining earlier committed work on error. Duration
// starts after both locks and uses a UTC timestamp with wall-clock elapsed
// calculation; heartbeat and completion callback follow target work, and
// maintenance release precedes leader release through deferred ordering.
func (s *OpsCleanupService) runScheduled() {
	if s == nil || s.db == nil || s.opsRepo == nil {
		return
	}

	// Keep admitted cron work independent of scheduler retirement. Stop waits for
	// callbacks while this run context budgets settings reads, lock attempts, and
	// target operations.
	ctx, cancel := context.WithTimeout(context.Background(), opsCleanupRunTimeout)
	defer cancel()

	s.refreshEffectiveBeforeRun(ctx)

	release, ok := s.tryAcquireLeaderLock(ctx)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}

	// Obtain common database-heavy maintenance ownership after leader admission,
	// so both Redis and advisory leaders still exclude concurrent protected target
	// work through the backend.
	releaseMaintenance, acquired, lockErr := s.db.AcquireMaintenance(ctx)
	if lockErr != nil {
		s.recordHeartbeatError(time.Now().UTC(), 0, lockErr)
		reportOptions(s.cfg, "[OpsCleanup] acquire maintenance lock failed: %v", lockErr)
		return
	}
	if !acquired {
		s.recordHeartbeatError(time.Now().UTC(), 0, ErrDatabaseMaintenanceBusy)
		reportOptions(s.cfg, "[OpsCleanup] skipped: database maintenance task is running")
		return
	}
	// Keep maintenance ownership through targets and status/reporting. Deferred
	// release runs before leader cleanup because it was registered later. Stop
	// leaves the job context active and waits for its callback completion.
	defer releaseMaintenance()

	// Start duration after admission using UTC, which discards monotonic time.
	// Heartbeat elapsed duration therefore reflects wall-clock difference and
	// excludes preceding preference/lock work.
	startedAt := time.Now().UTC()
	runAt := startedAt

	counts, err := s.runCleanupOnce(ctx)
	if err != nil {
		s.recordHeartbeatError(runAt, time.Since(startedAt), err)
		reportOptions(s.cfg, "[OpsCleanup] cleanup failed: %v", err)
		return
	}
	s.recordHeartbeatSuccess(runAt, time.Since(startedAt), counts)
	// Invoke the optional completion callback after the successful heartbeat
	// attempt. Failed heartbeat persistence was suppressed, so callback execution
	// still describes completed target work.
	reportCleanupCompleted(s.cfg, counts.String())
}

// runCleanupOnce processes eight trusted observation targets in their fixed
// order using one UTC now and a copied effective config. Nil
// service/backend/options returns successful zero counts. Negative retention
// skips, zero truncates, and positive retention supplies a calendar-day cutoff;
// the daily target asks the backend to cast cutoff to date.
//
// Each target completes independently. A later error returns totals for earlier
// successful targets and ignores the failed target's partial result, so
// returned counts can understate already committed deletion. Batch size/pause
// use defaults for nonpositive values, with pause milliseconds converted to
// duration. Enabled and monitoring gates are not checked here; admission and
// schedule ownership belong to the caller.
func (s *OpsCleanupService) runCleanupOnce(ctx context.Context) (opsCleanupDeletedCounts, error) {
	out := opsCleanupDeletedCounts{}
	if s == nil || s.db == nil || s.cfg == nil {
		return out, nil
	}

	effective := s.snapshotEffective()
	now := time.Now().UTC()

	targets := []opsCleanupTarget{
		{effective.ErrorLogRetentionDays, "ops_error_logs", "created_at", false, &out.errorLogs},
		{effective.ErrorLogRetentionDays, "ops_ingress_reject_aggregates", "bucket_start", false, &out.ingressRejects},
		{effective.ErrorLogRetentionDays, "ops_alert_events", "fired_at", false, &out.alertEvents},
		{effective.SystemLogRetentionDays, "ops_system_logs", "created_at", false, &out.systemLogs},
		{effective.ErrorLogRetentionDays, "ops_system_log_cleanup_audits", "created_at", false, &out.logAudits},
		{effective.MinuteMetricsRetentionDays, "ops_system_metrics", "created_at", false, &out.systemMetrics},
		{effective.HourlyMetricsRetentionDays, "ops_metrics_hourly", "bucket_start", false, &out.hourlyPreagg},
		{effective.HourlyMetricsRetentionDays, "ops_metrics_daily", "bucket_date", true, &out.dailyPreagg},
	}
	batchSize := effective.BatchSize
	if batchSize <= 0 {
		batchSize = opsCleanupDefaultBatchSize
	}
	// Convert configured pause milliseconds for backend throttling, replacing
	// nonpositive results with the standard batch delay after supplied duration
	// arithmetic.
	batchPause := time.Duration(effective.BatchPauseMS) * time.Millisecond
	if batchPause <= 0 {
		batchPause = opsCleanupDefaultBatchPause
	}

	// Process trusted targets in order and stop on the first error. Prior
	// successful deletions remain committed; only nil-error results enter
	// aggregate counters.
	for _, t := range targets {
		cutoff, truncate, ok := opsCleanupPlan(now, t.retentionDays)
		if !ok {
			continue
		}
		result, err := s.db.RunTarget(ctx, truncate, cutoff, t.table, t.timeCol, t.castDate, batchSize, batchPause)
		if err != nil {
			return out, err
		}
		*t.counter = result.Deleted
		out.batches += result.Batches
		out.throttled += result.Throttled
		reportOptions(s.cfg,
			"[OpsCleanup] table complete: table=%s deleted=%d batches=%d throttled_ms=%d",
			t.table, result.Deleted, result.Batches, result.Throttled.Milliseconds())
	}

	return out, nil
}

// tryAcquireLeaderLock attempts fixed cleanup Redis ownership, skipping
// contention and trying database advisory admission after Redis absence/error.
// It requires a usable context for configured Redis calls. The two leader
// mechanisms are independent; runScheduled subsequently attempts common
// maintenance ownership before target writes.
//
// The Redis lease is not renewed. Successful callers release once afterward
// using this same run context; its deadline expiry or cancellation can make
// release fail, and that error is ignored. Advisory release follows the backend
// adapter. Redis-alternative diagnostics are emitted once per service, and
// unavailable/refused advisory admission skips without a run heartbeat.
func (s *OpsCleanupService) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if s == nil {
		return nil, false
	}

	key := opsCleanupLeaderLockKeyDefault
	ttl := opsCleanupLeaderLockTTLDefault

	// Redis contention preserves the no-run result, while absence or claim failure
	// selects the separate advisory leader identity. Target work still needs
	// common maintenance ownership afterward.
	if s.redisClient != nil {
		ok, err := s.redisClient.Claim(ctx, key, s.instanceID, ttl)
		if err == nil {
			if !ok {
				return nil, false
			}
			return func() {
				// Reuse the run context for owner-checked release and suppress its failure.
				// A canceled or expired context can leave the lease until Redis TTL
				// expiration.
				_ = s.redisClient.Release(ctx, key, s.instanceID)
			}, true
		}
		// A failed Redis claim attempts advisory leadership; report this alternative
		// once, while leaving common maintenance admission to the owning scheduled
		// run.
		s.warnNoRedisOnce.Do(func() {
			reportOptions(s.cfg, "[OpsCleanup] leader lock SetNX failed; falling back to DB advisory lock: %v", err)
		})
	} else {
		s.warnNoRedisOnce.Do(func() {
			reportOptions(s.cfg, "[OpsCleanup] redis not configured; using DB advisory lock")
		})
	}

	release, ok := acquireAdvisory(ctx, s.db, key)
	if !ok {
		return nil, false
	}
	return release, true
}

// recordHeartbeatSuccess writes the supplied successful counts and
// integer-millisecond duration using a separate two-second background budget.
// Nil service/repository is skipped; the result text is UTF-8 bounded to 2048
// bytes and write failure is suppressed after completed target work.
func (s *OpsCleanupService) recordHeartbeatSuccess(runAt time.Time, duration time.Duration, counts opsCleanupDeletedCounts) {
	if s == nil || s.opsRepo == nil {
		return
	}
	now := time.Now().UTC()
	// Store whole milliseconds from the supplied duration and bound diagnostic
	// text to a UTF-8-safe 2048-byte prefix; heartbeat work uses its independent
	// cleanup budget.
	durMs := duration.Milliseconds()
	result := truncateString(counts.String(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), opsCleanupHeartbeatTimeout)
	defer cancel()
	// Suppress heartbeat write failure because target work or lock refusal already
	// determined the run outcome; this separate status update cannot undo prior
	// deletion.
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsCleanupJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &now,
		LastDurationMs: &durMs,
		LastResult:     &result,
	})
}

// recordHeartbeatError writes supplied error text and integer-millisecond
// duration with a separate two-second background budget. Nil dependencies/error
// are skipped, text is UTF-8 bounded to 2048 bytes, and failed health
// persistence does not undo partial target deletion.
func (s *OpsCleanupService) recordHeartbeatError(runAt time.Time, duration time.Duration, err error) {
	if s == nil || s.opsRepo == nil || err == nil {
		return
	}
	now := time.Now().UTC()
	// Store whole milliseconds from the supplied duration and bound diagnostic
	// text to a UTF-8-safe 2048-byte prefix; heartbeat work uses its independent
	// cleanup budget.
	durMs := duration.Milliseconds()
	msg := truncateString(err.Error(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), opsCleanupHeartbeatTimeout)
	defer cancel()
	// Suppress heartbeat write failure because target work or lock refusal already
	// determined the run outcome; this separate status update cannot undo prior
	// deletion.
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsCleanupJobName,
		LastRunAt:      &runAt,
		LastErrorAt:    &now,
		LastError:      &msg,
		LastDurationMs: &durMs,
	})
}
