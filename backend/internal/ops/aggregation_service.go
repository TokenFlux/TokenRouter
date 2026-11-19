package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

// aggregation_service.go schedules hourly and daily Ops aggregation and
// coordinates each refresh with leadership and shutdown.
// Ops aggregation refreshes the latest stable UTC hour and the latest closed
// UTC day, rather than scanning an entire missing history. Hourly selection
// subtracts a five-minute safety delay before flooring its end, while daily
// selection closes at today's UTC midnight. A saved latest-bucket hint can
// advance the chosen start but cannot extend the default window backward.
//
// Hourly and daily loops progress independently and can run together, with
// separate local mutexes, Redis keys, and database advisory identities. Startup
// runs both immediately; buffered triggers request prompt later work. Stop
// closes the loop signal and joins registered work, but each aggregation
// attempt uses background contexts and continues its own queries and heartbeat
// write. Redis contention skips a run, while Redis failure or absence selects
// the database lock; those separate lock mechanisms do not coordinate with one
// another.

const (
	// opsAggHourlyJobName and opsAggDailyJobName label separate persisted job
	// histories; the runtime status view reads the hourly job as the Ops
	// aggregation indicator.
	opsAggHourlyJobName = "ops_preaggregation_hourly" // hourly heartbeat job identity
	opsAggDailyJobName  = "ops_preaggregation_daily"  // daily heartbeat job identity

	// opsAggHourlyInterval and opsAggDailyInterval trigger periodic runs after
	// startup, while the same loops also consume coalesced manual setting-change
	// triggers.
	opsAggHourlyInterval = 10 * time.Minute // hourly-loop tick cadence
	opsAggDailyInterval  = 1 * time.Hour    // daily-loop tick cadence

	// opsAggBackfillWindow supplies the initial recent range. opsAggHourlyOverlap
	// and opsAggDailyOverlap subtract from a saved hint only when that candidate
	// advances this initial start.
	opsAggBackfillWindow = 1 * time.Hour // default recent window before flooring

	opsAggHourlyOverlap = 2 * time.Hour  // hourly saved-hint lookback duration
	opsAggDailyOverlap  = 48 * time.Hour // daily saved-hint lookback duration

	// opsAggHourlyChunk and opsAggDailyChunk cap individual repository ranges
	// inside a chosen window; successful earlier chunks remain committed when a
	// later chunk fails.
	opsAggHourlyChunk = 24 * time.Hour     // maximum hourly call range
	opsAggDailyChunk  = 7 * 24 * time.Hour // maximum daily call range

	// opsAggSafeDelay moves the hourly end before flooring so the most recent
	// boundary receives a delay; daily end selection uses current UTC midnight.
	opsAggSafeDelay = 5 * time.Minute // hourly end safety lag

	// opsAggHourlyTimeout and opsAggDailyTimeout govern attempts.
	// opsAggMaxQueryTimeout bounds independent latest-bucket reads, whose
	// background contexts do not inherit those attempt deadlines.
	opsAggMaxQueryTimeout = 5 * time.Second // detached latest-bucket read budget
	opsAggHourlyTimeout   = 5 * time.Minute // hourly attempt query budget
	opsAggDailyTimeout    = 2 * time.Minute // daily attempt query budget

	// opsAggHourlyLeaderLockKey and opsAggDailyLeaderLockKey distinguish
	// independent jobs. opsAggHourlyLeaderLockTTL and opsAggDailyLeaderLockTTL
	// bound Redis ownership without lease renewal during the run.
	opsAggHourlyLeaderLockKey = "ops:aggregation:hourly:leader" // hourly distributed lock identity
	opsAggDailyLeaderLockKey  = "ops:aggregation:daily:leader"  // daily distributed lock identity

	opsAggHourlyLeaderLockTTL = 15 * time.Minute // unrenewed hourly Redis lease
	opsAggDailyLeaderLockTTL  = 10 * time.Minute // unrenewed daily Redis lease
)

// OpsAggregationService coordinates independent hourly and daily refresh loops
// and their persisted job status.
type OpsAggregationService struct {
	// lifecycleMu, lifecycleStopped, and loopWG keep loop registration ahead of
	// joining. Start adds loops while holding the mutex; Stop seals future starts
	// before waiting outside it.
	lifecycleMu      sync.Mutex            // startup and shutdown registration mutex
	lifecycleStopped bool                  // permanent startup refusal flag
	loopWG           sync.WaitGroup        // registered aggregation loops join
	opsRepo          AggregationRepository // aggregation and heartbeat persistence
	settingRepo      SettingValueReader    // persisted monitoring and classification settings
	// cfg retains deployment options for hard switches and logging. Callers keep
	// those options immutable while aggregation loops read the same object.
	cfg *Options
	// preAggregationSettings supplies the runtime switch and retains a constructor
	// callback for its lifetime. Stop leaves that registration installed, so later
	// updates can still queue triggers.
	preAggregationSettings PreAggregationRuntimeReader // shared aggregation settings reader

	// db supplies the advisory alternative, redisClient supplies preferred Redis
	// claims, and instanceID identifies Redis ownership. Both jobs use separate
	// keys; neither lock system excludes holders of the other system.
	db          AdvisoryLocker   // database advisory lock adapter
	redisClient LeaderLeaseCache // Redis leader lock adapter
	instanceID  string           // Redis lock owner identity

	// stopCh broadcasts permanent shutdown once through stopOnce, while startOnce
	// limits registration. hourlyTriggerCh and dailyTriggerCh each retain at most
	// one wake request, including requests queued before Start or after Stop.
	stopCh          chan struct{} // loop shutdown broadcast channel
	hourlyTriggerCh chan struct{} // coalesced hourly wake channel
	dailyTriggerCh  chan struct{} // coalesced daily wake channel
	startOnce       sync.Once     // single loop registration guard
	stopOnce        sync.Once     // single stop-channel close guard

	// hourlyMu and dailyMu serialize their respective private attempts within this
	// instance. They are independent, so hourly and daily persistence can overlap
	// while each has its own distributed identity.
	hourlyMu sync.Mutex // hourly attempt serialization mutex
	dailyMu  sync.Mutex // daily attempt serialization mutex

	// skipLogMu protects skipLogAt across both jobs. A skip from either loop can
	// suppress the other's diagnostic during the shared minute interval,
	// irrespective of its prefix.
	skipLogMu sync.Mutex // shared skip-diagnostic timing mutex
	skipLogAt time.Time  // last skip diagnostic instant
}

// NewOpsAggregationService constructs inactive loops with single-slot wake
// channels and a Redis owner identity. Dependencies and options are retained;
// when settings are supplied, their lifetime callback queues both jobs on an
// Ops disabled-to-enabled transition. Call Start separately after application
// binding.
func NewOpsAggregationService(
	opsRepo AggregationRepository,
	settingRepo SettingValueReader,
	db AdvisoryLocker,
	redisClient LeaderLeaseCache,
	cfg *Options,
	preAggregationSettings PreAggregationRuntimeReader,
) *OpsAggregationService {
	svc := &OpsAggregationService{
		opsRepo:                opsRepo,
		settingRepo:            settingRepo,
		cfg:                    cfg,
		preAggregationSettings: preAggregationSettings,
		db:                     db,
		redisClient:            redisClient,
		instanceID:             uuid.NewString(),
		hourlyTriggerCh:        make(chan struct{}, 1),
		dailyTriggerCh:         make(chan struct{}, 1),
	}
	// Retain the enable-transition listener for the settings service's lifetime.
	// It queues bounded wake requests, so synchronous settings publication does
	// not wait for aggregation.
	if preAggregationSettings != nil {
		preAggregationSettings.RegisterListener(func(previous, next preaggregation.PreAggregationSettings) {
			if !previous.Ops.Enabled && next.Ops.Enabled {
				svc.TriggerNow()
			}
		})
	}
	return svc
}

// TriggerNow queues at most one pending wake for each loop and returns
// immediately when a channel is full. It can run before Start or after Stop;
// queued requests do not establish that gates or locks permit aggregation.
func (s *OpsAggregationService) TriggerNow() {
	if s == nil {
		return
	}
	// Each channel keeps one pending request. Dropping an additional wake
	// preserves the queued request, and stopped loops can leave that request
	// unconsumed.
	select {
	case s.hourlyTriggerCh <- struct{}{}:
	default:
	}
	select {
	case s.dailyTriggerCh <- struct{}{}:
	default:
	}
}

// RuntimeStatus derives Ops aggregation health from the first saved hourly
// heartbeat. Missing service or repository reports unavailable; a disabled
// shared aggregation switch reports disabled before storage lookup. Read
// failure reports error and its text, while no hourly heartbeat reports
// pending.
//
// An available heartbeat reports idle unless a present error timestamp is
// strictly later than success, or that error has no success timestamp. This
// phase describes persisted recency, not a running job or continuous historical
// coverage; daily status is
// not consulted. Timestamp pointers are retained from the returned heartbeat,
// and duration and error text are copied when present.
func (s *OpsAggregationService) RuntimeStatus(ctx context.Context) preaggregation.PreAggregationRuntimeStatus {
	status := preaggregation.PreAggregationRuntimeStatus{Phase: "unavailable"}
	if s == nil || s.opsRepo == nil {
		return status
	}
	if s.preAggregationSettings != nil && !s.preAggregationSettings.OpsEnabled(ctx) {
		status.Phase = "disabled"
		return status
	}
	heartbeats, err := s.opsRepo.ListJobHeartbeats(ctx)
	if err != nil {
		status.Phase = "error"
		status.LastError = err.Error()
		return status
	}
	status.Phase = "pending"
	for _, heartbeat := range heartbeats {
		if heartbeat == nil || heartbeat.JobName != opsAggHourlyJobName {
			continue
		}
		status.LastRunAt = heartbeat.LastRunAt
		status.LastSuccessAt = heartbeat.LastSuccessAt
		status.LastErrorAt = heartbeat.LastErrorAt
		if heartbeat.LastDurationMs != nil {
			status.LastDurationMS = *heartbeat.LastDurationMs
		}
		if heartbeat.LastError != nil {
			status.LastError = *heartbeat.LastError
		}
		// A present error wins only when newer than success or success is missing.
		// Equal timestamps remain idle even when stored error text is present.
		status.Phase = "idle"
		if heartbeat.LastErrorAt != nil && (heartbeat.LastSuccessAt == nil || heartbeat.LastErrorAt.After(*heartbeat.LastSuccessAt)) {
			status.Phase = "error"
		}
		break
	}
	return status
}

// Start registers both aggregation loops once unless Stop has permanently
// sealed startup. The lifecycle mutex keeps WaitGroup registration ahead of a
// concurrent Stop wait, so a successful start has both loops included in
// shutdown joining.
//
// Each loop attempts immediate aggregation before checking its stop channel.
// Later ticks and buffered triggers request more attempts subject to their own
// gates and locks. Callers configure retained dependencies before startup;
// repeated Start calls add no work, and a call after Stop cannot reopen the
// service.
func (s *OpsAggregationService) Start() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped {
		return
	}

	// Register both WaitGroup tasks while holding the lifecycle mutex. Stop seals
	// registration under the same mutex before joining outside it.
	s.startOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		s.loopWG.Go(func() { ; s.hourlyLoop() })
		s.loopWG.Go(func() { ; s.dailyLoop() })
	})
}

// Stop permanently refuses future Start calls, closes the shared stop signal
// once, and waits for registered loops. Stop before Start also seals the
// instance, and repeated calls wait for the same work without closing the
// channel again.
//
// Joining happens after releasing the lifecycle mutex so loops can finish.
// Active aggregation uses background contexts and is not canceled by this
// signal; its queries, heartbeat, and lock release can still run. If a tick or
// trigger is ready with shutdown, the loop select can choose another attempt
// before observing Stop.
func (s *OpsAggregationService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.lifecycleStopped = true

	// Seal future starts before closing the shared signal; release the lifecycle
	// mutex before joining so in-flight loops can finish their selected attempts.
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.lifecycleMu.Unlock()
	s.loopWG.Wait()
}

// hourlyLoop attempts aggregation immediately, then responds to ten-minute
// ticks and single-slot triggers until it selects shutdown. Its ticker is
// stopped on return, and one attempt completes synchronously before the loop
// selects another event.
//
// Trigger bursts coalesce while work is running. Closing stopCh does not cancel
// the current attempt or give shutdown priority over ready ticks and triggers,
// so Stop joins this loop only after its selected work completes. The
// aggregation method decides runtime enablement and lock admission separately.
func (s *OpsAggregationService) hourlyLoop() {
	// Attempt initial refresh before selecting stop. An immediately concurrent
	// Stop still waits for this attempt's gates, lock acquisition, and database
	// work.
	s.aggregateHourly()

	ticker := time.NewTicker(opsAggHourlyInterval)
	// Release ticker resources when the loop returns. Tick delivery coalesces
	// while synchronous aggregation work is running and can be ready alongside
	// shutdown.
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.aggregateHourly()
		case <-s.hourlyTriggerCh:
			s.aggregateHourly()
		case <-s.stopCh:
			return
		}
	}
}

// dailyLoop attempts a daily refresh immediately, then responds to hourly ticks
// and single-slot triggers until it selects shutdown. Its ticker is stopped on
// return, and each selected attempt finishes before the next event is read.
//
// Hourly aggregation has its own loop, so daily work can overlap its source
// refresh. Buffered wakeups coalesce during a run, and a ready stop signal has
// no select priority over other ready events. Stop waits for this loop while
// the current attempt retains its independent background budgets.
func (s *OpsAggregationService) dailyLoop() {
	// Attempt initial refresh before selecting stop. An immediately concurrent
	// Stop still waits for this attempt's gates, lock acquisition, and database
	// work.
	s.aggregateDaily()

	ticker := time.NewTicker(opsAggDailyInterval)
	// Release ticker resources when the loop returns. Tick delivery coalesces
	// while synchronous aggregation work is running and can be ready alongside
	// shutdown.
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.aggregateDaily()
		case <-s.dailyTriggerCh:
			s.aggregateDaily()
		case <-s.stopCh:
			return
		}
	}
}

// aggregateHourly refreshes a recent stable-hour window when repository, hard
// switches, monitoring, runtime settings, and leader admission permit work. The
// default range is one hour ending at the UTC hour obtained after subtracting
// the safety delay. A saved latest bucket minus overlap replaces the start only
// when later, so the hint cannot expand this default backward and a future hint
// can leave no range.
//
// The leader claim precedes the local hourly mutex. Settings and writes use the
// attempt budget, while latest-bucket lookup, heartbeat, and release use
// independent background contexts. Successful chunks remain persisted if a
// later chunk fails; a lookup failure keeps the default start. The heartbeat
// measures wall-clock work after local admission through chunk completion,
// excludes admission and later heartbeat/release time, and silently tolerates heartbeat
// failure. Disabled or empty-range paths record no run.
func (s *OpsAggregationService) aggregateHourly() {
	if s == nil || s.opsRepo == nil {
		return
	}
	if s.cfg != nil {
		if !s.cfg.Ops.Enabled {
			return
		}
		if !s.cfg.Ops.Aggregation.Enabled {
			return
		}
	}

	// Keep the attempt independent of the loop stop signal. Stop waits for
	// selected work, while this timeout supplies the queries' cancellation
	// context.
	ctx, cancel := context.WithTimeout(context.Background(), opsAggHourlyTimeout)
	defer cancel()

	if !s.isMonitoringEnabled(ctx) {
		return
	}
	if s.preAggregationSettings != nil && !s.preAggregationSettings.OpsEnabled(ctx) {
		return
	}

	release, ok := s.tryAcquireLeaderLock(ctx, opsAggHourlyLeaderLockKey, opsAggHourlyLeaderLockTTL, "[OpsAggregation][hourly]")
	if !ok {
		return
	}
	// Retain distributed ownership through the attempt and later heartbeat.
	// Deferred release uses its separate cleanup policy even after this attempt
	// context expires.
	if release != nil {
		defer release()
	}

	s.hourlyMu.Lock()
	defer s.hourlyMu.Unlock()

	startedAt := time.Now().UTC()
	runAt := startedAt

	// Floor after subtracting the safety delay so a boundary reached less than
	// five minutes ago remains excluded from this hour's stable window.
	end := utcFloorToHour(time.Now().UTC().Add(-opsAggSafeDelay))
	start := end.Add(-opsAggBackfillWindow)

	// Read the saved hint with an independent background budget. Failure retains
	// the default recent window, and only a later candidate advances its start.
	{
		ctxMax, cancelMax := context.WithTimeout(context.Background(), opsAggMaxQueryTimeout)
		latest, ok, err := s.opsRepo.GetLatestHourlyBucketStart(ctxMax)
		cancelMax()
		if err != nil {
			reportOptions(s.cfg, "[OpsAggregation][hourly] failed to read latest bucket: %v", err)
		} else if ok {
			candidate := latest.Add(-opsAggHourlyOverlap)
			if candidate.After(start) {
				start = candidate
			}
		}
	}

	start = utcFloorToHour(start)
	if !start.Before(end) {
		return
	}

	ignoredStatusCodes := resolveOpsIgnoredStatusCodesFromRepo(ctx, s.settingRepo)
	var aggErr error
	// Persist selected chunks in order and stop at the first failure. Prior
	// successful chunks remain committed, and heartbeat reports the selected
	// window afterward.
	for cursor := start; cursor.Before(end); cursor = cursor.Add(opsAggHourlyChunk) {
		chunkEnd := minTime(cursor.Add(opsAggHourlyChunk), end)
		if err := s.opsRepo.UpsertHourlyMetrics(ctx, cursor, chunkEnd, ignoredStatusCodes); err != nil {
			aggErr = err
			reportOptions(s.cfg, "[OpsAggregation][hourly] upsert failed (%s..%s): %v", cursor.Format(time.RFC3339), chunkEnd.Format(time.RFC3339), err)
			break
		}
	}

	finishedAt := time.Now().UTC()
	// UTC timestamps discard monotonic readings; heartbeat duration uses
	// wall-clock difference and truncates fractional milliseconds before separate
	// status and release work.
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	dur := durationMs

	if aggErr != nil {
		// Keep a UTF-8-safe 2048-byte diagnostic prefix for the heartbeat. Report the
		// first chunk error with an independent budget, suppressing heartbeat
		// persistence failure.
		msg := truncateString(aggErr.Error(), 2048)
		errAt := finishedAt
		hbCtx, hbCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hbCancel()
		_ = s.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
			JobName:        opsAggHourlyJobName,
			LastRunAt:      &runAt,
			LastErrorAt:    &errAt,
			LastError:      &msg,
			LastDurationMs: &dur,
		})
		return
	}

	// Write the completed window as successful using an independent heartbeat
	// budget. A failed status write is ignored and leaves prior persisted health
	// available.
	successAt := finishedAt
	hbCtx, hbCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer hbCancel()
	result := truncateString(fmt.Sprintf("window=%s..%s", start.Format(time.RFC3339), end.Format(time.RFC3339)), 2048)
	_ = s.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAggHourlyJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &successAt,
		LastDurationMs: &dur,
		LastResult:     &result,
	})
}

// aggregateDaily refreshes the latest closed UTC date when all configured gates
// and leader admission permit it. It starts one hour before current UTC
// midnight, then floors that start to the previous day's midnight. A saved
// daily date minus overlap can advance this start but cannot widen it backward;
// an empty selected range records no heartbeat.
//
// Daily and hourly jobs use independent local and distributed locks, so hourly
// source rows may change while the daily statement reads them. Attempt queries
// use one background budget, latest-date reads use another, and
// heartbeat/release continue with their own budgets. Earlier successful chunks
// survive later failure. Lookup failure retains the recent-day default, while
// heartbeat write errors are suppressed after recording success or the first
// chunk failure.
func (s *OpsAggregationService) aggregateDaily() {
	if s == nil || s.opsRepo == nil {
		return
	}
	if s.cfg != nil {
		if !s.cfg.Ops.Enabled {
			return
		}
		if !s.cfg.Ops.Aggregation.Enabled {
			return
		}
	}

	// Keep the attempt independent of the loop stop signal. Stop waits for
	// selected work, while this timeout supplies the queries' cancellation
	// context.
	ctx, cancel := context.WithTimeout(context.Background(), opsAggDailyTimeout)
	defer cancel()

	if !s.isMonitoringEnabled(ctx) {
		return
	}
	if s.preAggregationSettings != nil && !s.preAggregationSettings.OpsEnabled(ctx) {
		return
	}

	release, ok := s.tryAcquireLeaderLock(ctx, opsAggDailyLeaderLockKey, opsAggDailyLeaderLockTTL, "[OpsAggregation][daily]")
	if !ok {
		return
	}
	// Retain distributed ownership through the attempt and later heartbeat.
	// Deferred release uses its separate cleanup policy even after this attempt
	// context expires.
	if release != nil {
		defer release()
	}

	s.dailyMu.Lock()
	defer s.dailyMu.Unlock()

	startedAt := time.Now().UTC()
	runAt := startedAt

	// Use UTC midnight as the exclusive end. Flooring the start one hour earlier
	// selects the previous complete UTC date before any saved hint advances it.
	end := utcFloorToDay(time.Now().UTC())
	start := end.Add(-opsAggBackfillWindow)

	// Read the saved hint with an independent background budget. Failure retains
	// the default recent window, and only a later candidate advances its start.
	{
		ctxMax, cancelMax := context.WithTimeout(context.Background(), opsAggMaxQueryTimeout)
		latest, ok, err := s.opsRepo.GetLatestDailyBucketDate(ctxMax)
		cancelMax()
		if err != nil {
			reportOptions(s.cfg, "[OpsAggregation][daily] failed to read latest bucket: %v", err)
		} else if ok {
			candidate := latest.Add(-opsAggDailyOverlap)
			if candidate.After(start) {
				start = candidate
			}
		}
	}

	start = utcFloorToDay(start)
	if !start.Before(end) {
		return
	}

	var aggErr error
	// Persist selected chunks in order and stop at the first failure. Prior
	// successful chunks remain committed, and heartbeat reports the selected
	// window afterward.
	for cursor := start; cursor.Before(end); cursor = cursor.Add(opsAggDailyChunk) {
		chunkEnd := minTime(cursor.Add(opsAggDailyChunk), end)
		if err := s.opsRepo.UpsertDailyMetrics(ctx, cursor, chunkEnd); err != nil {
			aggErr = err
			reportOptions(s.cfg, "[OpsAggregation][daily] upsert failed (%s..%s): %v", cursor.Format("2006-01-02"), chunkEnd.Format("2006-01-02"), err)
			break
		}
	}

	finishedAt := time.Now().UTC()
	// UTC timestamps discard monotonic readings; heartbeat duration uses
	// wall-clock difference and truncates fractional milliseconds before separate
	// status and release work.
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	dur := durationMs

	if aggErr != nil {
		// Keep a UTF-8-safe 2048-byte diagnostic prefix for the heartbeat. Report the
		// first chunk error with an independent budget, suppressing heartbeat
		// persistence failure.
		msg := truncateString(aggErr.Error(), 2048)
		errAt := finishedAt
		hbCtx, hbCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hbCancel()
		_ = s.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
			JobName:        opsAggDailyJobName,
			LastRunAt:      &runAt,
			LastErrorAt:    &errAt,
			LastError:      &msg,
			LastDurationMs: &dur,
		})
		return
	}

	// Write the completed window as successful using an independent heartbeat
	// budget. A failed status write is ignored and leaves prior persisted health
	// available.
	successAt := finishedAt
	hbCtx, hbCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer hbCancel()
	result := truncateString(fmt.Sprintf("window=%s..%s", start.Format(time.RFC3339), end.Format(time.RFC3339)), 2048)
	_ = s.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAggDailyJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &successAt,
		LastDurationMs: &dur,
		LastResult:     &result,
	})
}

// isMonitoringEnabled resolves the persisted monitoring flag after the hard Ops
// switch. Nil settings, missing keys, and read errors all permit monitoring;
// only trimmed case-insensitive false, zero, off, or disabled turn it off.
func (s *OpsAggregationService) isMonitoringEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return false
	}
	if s.settingRepo == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpsMonitoringEnabled)
	// Missing or failed monitoring reads select enabled, so this attempt can
	// continue to runtime settings and leader admission despite unavailable
	// settings.
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return true
		}
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

// tryAcquireLeaderLock selects Redis ownership first and returns release after
// successful admission. Redis contention skips work without trying the
// database. Redis absence or claim error tries the named database advisory
// lock; unavailable database locking or refusal also skips.
//
// The two systems use independent ownership and cannot exclude a holder using
// the other system. Redis leases are not renewed; callers release once after
// work, with owner-checked deletion under a separate two-second background
// budget and suppressed cleanup errors. Database release follows its adapter's
// reserved-session cleanup, while the boolean admission API hides acquisition
// diagnostics.
func (s *OpsAggregationService) tryAcquireLeaderLock(ctx context.Context, key string, ttl time.Duration, logPrefix string) (func(), bool) {
	if s == nil {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Redis contention retains the selected Redis admission decision. Only Redis
	// absence or error reaches the independent database alternative, whose refusal
	// has no separate diagnostic result.
	if s.redisClient != nil {
		ok, err := s.redisClient.Claim(ctx, key, s.instanceID, ttl)
		if err == nil {
			if !ok {
				s.maybeLogSkip(logPrefix)
				return nil, false
			}
			release := func() {
				ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				// Attempt owner-checked release after the job even if its work deadline
				// expired. Cleanup errors are ignored and the unrenewed lease can expire
				// independently.
				_ = s.redisClient.Release(ctx2, key, s.instanceID)
			}
			return release, true
		}
		// A failed Redis claim selects database advisory admission; a successful
		// Redis holder elsewhere is independent of that database lock.
	}

	release, ok := acquireAdvisory(ctx, s.db, key)
	if !ok {
		s.maybeLogSkip(logPrefix)
		return nil, false
	}
	return release, true
}

// maybeLogSkip emits at most one skip diagnostic per minute across both
// aggregation jobs under a shared mutex. Empty prefix uses the aggregation
// label; refusal can also mean acquisition failure, even though the fixed log
// text mentions another holder.
func (s *OpsAggregationService) maybeLogSkip(prefix string) {
	s.skipLogMu.Lock()
	defer s.skipLogMu.Unlock()

	now := time.Now()
	// One instance shares this minute gate between hourly and daily skips. The
	// diagnostic records refusal, which can also represent unavailable or failed
	// lock acquisition.
	if !s.skipLogAt.IsZero() && now.Sub(s.skipLogAt) < time.Minute {
		return
	}
	s.skipLogAt = now
	if prefix == "" {
		prefix = "[OpsAggregation]"
	}
	reportOptions(s.cfg, "%s leader lock held by another instance; skipping", prefix)
}

// utcFloorToHour returns the preceding whole UTC hour for bucket selection.
func utcFloorToHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// utcFloorToDay returns UTC midnight for the date containing t.
func utcFloorToDay(t time.Time) time.Time {
	u := t.UTC()
	y, m, d := u.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// minTime returns the earlier supplied timestamp, choosing b on equality.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
