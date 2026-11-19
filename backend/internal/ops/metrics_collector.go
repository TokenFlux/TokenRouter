package ops

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// metrics_collector.go collects and persists periodic system metrics under
// monitoring and leadership controls.
// System metrics combine the previous complete UTC minute of request records
// with host and connection observations made during collection. Those reads
// occur separately, so one stored row can contain values observed at different
// moments. Missing optional queue or Redis pool observations stay null, while
// successful observations can report zero.
//
// A collector has one permanent start/stop lifecycle and waits for its loop
// during shutdown. Each attempt checks the monitoring setting, then selects
// Redis leadership or a database advisory lock after a Redis error. These lock
// mechanisms have separate ownership, and an absent Redis client permits an
// unlocked run. Metric insertion precedes a separate best-effort heartbeat
// write; neither leadership nor heartbeat persistence makes a minute an
// exactly-once transaction.

// These names, delays, and budgets govern collector scheduling, leader
// ownership, and separate job-heartbeat persistence attempts.
const (
	opsMetricsCollectorJobName     = "ops_metrics_collector" // heartbeat job identifier
	opsMetricsCollectorMinInterval = 60 * time.Second        // minimum post-collection delay
	opsMetricsCollectorMaxInterval = 1 * time.Hour           // maximum post-collection delay

	opsMetricsCollectorTimeout = 10 * time.Second // collection context lifetime

	opsMetricsCollectorLeaderLockKey = "ops:metrics:collector:leader" // shared leadership key
	opsMetricsCollectorLeaderLockTTL = 90 * time.Second               // unrenewed Redis lease lifetime

	opsMetricsCollectorHeartbeatTimeout = 2 * time.Second // independent heartbeat context lifetime
)

// opsSchedulableProviderLoadRepository supplies the optional dedicated
// provider-load listing used by the collector.
type opsSchedulableProviderLoadRepository interface {
	// ListSchedulableProviderLoads returns schedulable provider IDs and their load
	// limits. The collector prefers this method when available and forwards its
	// result to the concurrency reader; errors omit the queue-depth observation.
	ListSchedulableProviderLoads(ctx context.Context) ([]ProviderWithConcurrency, error)
}

// OpsMetricsCollector owns periodic system-metric collection and its permanent
// shutdown state.
type OpsMetricsCollector struct {
	// lifecycleMu serializes lifecycleStopped and loopWG admission. Stop marks
	// admission closed before waiting, so a later Start cannot add a loop to that
	// wait.
	lifecycleMu      sync.Mutex     // start and stop serialization
	lifecycleStopped bool           // permanent stop admission flag
	loopWG           sync.WaitGroup // started collection-loop join
	// opsRepo receives metric rows and job heartbeats. collectOnce skips attempts
	// when this dependency is absent.
	opsRepo MetricsCollectorRepository // metric and heartbeat writer
	// settingRepo supplies interval and monitoring settings. An absent reader
	// selects the minimum interval and permits monitoring.
	settingRepo SettingValueReader // runtime collector settings reader
	// cfg is retained from construction. A nonnil Options with Ops.Enabled false
	// suppresses collection before querying settings or leadership.
	cfg *Options // process Ops switch source

	// providerRepo and concurrencyService jointly supply queue depth. Either
	// dependency being absent leaves that metric null while other system metrics
	// can still be written.
	providerRepo       ProviderLoadSource // schedulable provider listing source
	concurrencyService ConcurrencyReader  // provider queue-depth batch reader

	// db supplies request aggregates and advisory locking. An absent source
	// suppresses collection even when host sampling is available.
	db MetricsSource // historical metric query source
	// redisClient owns lease acquisition and release. When it is absent, the
	// collector runs without selecting an advisory lock.
	redisClient LeaderLeaseCache // Redis leadership lease adapter
	instanceID  string           // per-collector lease owner identity

	// observer supplies live measurements after leadership succeeds. Collection
	// requires a nonnil sampler whose returned system stats remain nonnil on
	// error.
	observer HostSampler // live host and dependency sampler

	// stopCh is created inside startOnce and closed through stopOnce under
	// lifecycleMu. These barriers prevent duplicate workers or repeated channel
	// closes across Start and Stop.
	stopCh    chan struct{} // collection-loop termination signal
	startOnce sync.Once     // single loop start barrier
	stopOnce  sync.Once     // single channel close barrier

	// skipLogMu protects skipLogAt across skipped attempts. The last emitted
	// diagnostic time decides whether another leadership failure can log within
	// the next minute.
	skipLogMu sync.Mutex // skip diagnostic state mutex
	skipLogAt time.Time  // latest emitted skip diagnostic time
}

// NewOpsMetricsCollector retains the supplied query, persistence, leadership,
// settings, and observation dependencies and assigns a fresh lease-owner ID.
// Call Start after dependencies are ready; construction leaves the loop and
// stop channel inactive. A running collection requires a HostSampler that
// returns nonnil system stats even when reporting a sampling error.
func NewOpsMetricsCollector(
	opsRepo MetricsCollectorRepository,
	settingRepo SettingValueReader,
	providerRepo ProviderLoadSource,
	concurrencyService ConcurrencyReader,
	db MetricsSource,
	redisClient LeaderLeaseCache,
	cfg *Options,
	observer HostSampler,
) *OpsMetricsCollector {
	return &OpsMetricsCollector{
		opsRepo:            opsRepo,
		settingRepo:        settingRepo,
		cfg:                cfg,
		providerRepo:       providerRepo,
		concurrencyService: concurrencyService,
		db:                 db,
		redisClient:        redisClient,
		instanceID:         uuid.NewString(),
		observer:           observer,
	}
}

// Start admits one collection loop unless Stop has already closed lifecycle
// admission. A nil receiver is ignored, and repeated calls reuse the first
// start barrier.
//
// The lifecycle mutex covers the stopped check, channel creation, and WaitGroup
// admission so Stop cannot begin waiting while another Start adds work. Once
// admitted, the worker performs its initial collection before inspecting the
// stop channel; an immediate Stop can therefore wait for that initial attempt.
// @project-doc docs/operations/ops_monitoring_and_alerting.md#ops_signal_pipeline
func (c *OpsMetricsCollector) Start() {
	if c == nil {
		return
	}
	// Keep stopped-state inspection, channel setup, and loop admission in one
	// critical section so Stop cannot wait before this Start registers its worker.
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.lifecycleStopped {
		return
	}

	c.startOnce.Do(func() {
		if c.stopCh == nil {
			c.stopCh = make(chan struct{})
		}
		c.loopWG.Go(func() { ; c.run() })
	})
}

// Stop permanently prevents another Start and waits for the admitted collection
// loop to finish. A nil receiver is ignored, and repeated callers can wait on
// the same loop.
//
// Closing stopCh wakes a loop waiting between attempts, but active collection
// uses its own background-derived contexts. Stop does not cancel those calls.
// The channel close occurs under lifecycleMu before the mutex is released for
// waiting, allowing a concurrent Start to observe closed admission rather than
// adding new work.
func (c *OpsMetricsCollector) Stop() {
	if c == nil {
		return
	}
	// Close lifecycle admission before signaling the worker, then release the
	// mutex before joining so concurrent Start observes a stopped collector.
	c.lifecycleMu.Lock()
	c.lifecycleStopped = true

	c.stopOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
		}
	})
	c.lifecycleMu.Unlock()
	c.loopWG.Wait()
}

// run performs an initial collection, then waits for the configured delay after
// each completed attempt. Interval reads and collection duration therefore
// contribute to the spacing between runs; collection is not scheduled at fixed
// minute boundaries.
//
// The stop channel is observed during the timer wait. If the timer and stop
// signal are both ready, select can choose another collection before exiting.
// Stop joins this loop, including any active interval read or collection,
// rather than canceling those dependency contexts.
func (c *OpsMetricsCollector) run() {
	// Perform the startup attempt before creating an interval timer so available
	// dashboard metrics need not wait for the first configured delay.
	c.collectOnce()

	for {
		interval := c.getInterval()
		// Create a fresh delay after the preceding attempt; stopping discards this
		// timer and joins any collection already selected.
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			c.collectOnce()
		case <-c.stopCh:
			timer.Stop()
			return
		}
	}
}

// getInterval reads the current collection delay in seconds and returns a
// duration between one minute and one hour. An absent settings reader, failed
// read, blank value, or failed integer parse selects the minimum delay.
//
// Successfully parsed numbers are clamped, so negative and zero values also
// produce the minimum. The settings read has an independent background-derived
// context; a collection's context and a closed stop channel do not cancel this
// read. run consults the setting before each timer.
func (c *OpsMetricsCollector) getInterval() time.Duration {
	interval := opsMetricsCollectorMinInterval

	if c.settingRepo == nil {
		return interval
	}

	// Give the settings lookup its own short context because this loop reads the
	// interval outside any metric-collection attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, err := c.settingRepo.GetValue(ctx, SettingKeyOpsMetricsIntervalSeconds)
	if err != nil {
		// A failed settings read uses the minimum delay, keeping the collector
		// scheduled while the runtime setting is unavailable.
		return interval
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return interval
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil {
		// Unparseable text uses the same minimum delay as an absent interval instead
		// of interrupting the background loop.
		return interval
	}
	// Clamp parsed seconds before duration conversion so valid out-of-range
	// settings cannot select a delay outside the collector's scheduling limits.
	if seconds < int(opsMetricsCollectorMinInterval.Seconds()) {
		seconds = int(opsMetricsCollectorMinInterval.Seconds())
	}
	if seconds > int(opsMetricsCollectorMaxInterval.Seconds()) {
		seconds = int(opsMetricsCollectorMaxInterval.Seconds())
	}
	// Convert configured seconds into the timer's duration unit.
	return time.Duration(seconds) * time.Second
}

// collectOnce attempts one metric row when Ops, monitoring, and required
// persistence/query dependencies allow it. Leadership refusal and disabled
// collection return before writing a heartbeat.
//
// After leadership succeeds, the row attempt runs within a background-derived
// collection context. Its start and finish times describe collection and
// insertion, excluding setting reads, lock acquisition, heartbeat writing, and
// release. A separate background-derived context writes the outcome even after
// collection expires. Heartbeat failures are ignored, so a successful row can
// coexist with an older job status; deferred leadership release runs after
// outcome handling.
func (c *OpsMetricsCollector) collectOnce() {
	if c == nil {
		return
	}
	if c.cfg != nil && !c.cfg.Ops.Enabled {
		return
	}
	if c.opsRepo == nil {
		return
	}
	if c.db == nil {
		return
	}

	// Use a fresh attempt context so interval delays and prior attempts consume
	// none of this run's collection budget.
	ctx, cancel := context.WithTimeout(context.Background(), opsMetricsCollectorTimeout)
	defer cancel()

	if !c.isMonitoringEnabled(ctx) {
		return
	}

	release, ok := c.tryAcquireLeaderLock(ctx)
	if !ok {
		return
	}
	// Keep the acquired release callback until metric insertion and heartbeat
	// handling finish; a Redis lease can still expire independently during that
	// work.
	if release != nil {
		defer release()
	}

	startedAt := time.Now().UTC()
	err := c.collectAndPersist(ctx)
	finishedAt := time.Now().UTC()

	// Record collection and insertion time in milliseconds for the heartbeat.
	// Setting reads and leadership acquisition preceded this measured interval.
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	dur := durationMs
	runAt := startedAt

	if err != nil {
		// Bound the persisted diagnostic to 2048 bytes with UTF-8 prefix truncation;
		// the log still receives the complete collection error.
		msg := truncateString(err.Error(), 2048)
		errAt := finishedAt
		// Record the failed attempt using an independent context after collection
		// ends. A heartbeat error is suppressed and leaves prior job state possible.
		hbCtx, hbCancel := context.WithTimeout(context.Background(), opsMetricsCollectorHeartbeatTimeout)
		defer hbCancel()
		_ = c.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
			JobName:        opsMetricsCollectorJobName,
			LastRunAt:      &runAt,
			LastErrorAt:    &errAt,
			LastError:      &msg,
			LastDurationMs: &dur,
		})
		logger.LegacyPrintf("service.ops_metrics", "[OpsMetricsCollector] collect failed: %v", err)
		return
	}

	successAt := finishedAt
	// Write success after the metric insert with a separate context; heartbeat
	// failure cannot undo the inserted row and is ignored.
	hbCtx, hbCancel := context.WithTimeout(context.Background(), opsMetricsCollectorHeartbeatTimeout)
	defer hbCancel()
	_ = c.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsMetricsCollectorJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &successAt,
		LastDurationMs: &dur,
	})
}

// isMonitoringEnabled combines the hard Ops switch with the current monitoring
// setting. A nil receiver or disabled hard switch rejects collection; an absent
// reader or any read error permits it. Trimmed, case-folded false, 0, off, and
// disabled disable monitoring, and all other values permit collection. A nil
// context uses Background.
func (c *OpsMetricsCollector) isMonitoringEnabled(ctx context.Context) bool {
	if c == nil {
		return false
	}
	if c.cfg != nil && !c.cfg.Ops.Enabled {
		return false
	}
	if c.settingRepo == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	value, err := c.settingRepo.GetValue(ctx, SettingKeyOpsMonitoringEnabled)
	if err != nil {
		// A missing setting selects enabled monitoring, allowing collection before
		// administrators persist a runtime monitoring preference in the settings
		// repository.
		if errors.Is(err, ErrSettingNotFound) {
			return true
		}
		// Treat setting storage failures like missing configuration, so a transient
		// read problem does not suppress the entire monitoring attempt.
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

// collectAndPersist queries the previous complete UTC minute and inserts its
// request counts, rates, latency, and live system observations. A nil context
// uses Background, and any required request query failure stops the attempt
// before insertion.
//
// Host sampling errors are logged while its nonnil result remains usable.
// Health checks, pool reads, SQL aggregates, and queue sampling happen
// sequentially with separate observation times. Redis pool and queue
// availability determine null versus observed-zero fields. QPS includes
// usage observations and final client errors, while TPS uses consumed tokens over
// the full minute; both are rounded to one decimal place.
func (c *OpsMetricsCollector) collectAndPersist(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Query the last closed minute so both request counts and their per-second
	// denominators describe the same complete historical window.
	now := time.Now().UTC()
	windowEnd := now.Truncate(time.Minute)
	windowStart := windowEnd.Add(-1 * time.Minute)

	sys, err := c.observer.CollectSystemStats(ctx)
	if err != nil {
		// Keep the sampler's available fields when host observation fails;
		// HostSampler requires a nonnil result because insertion reads that result
		// below.
		logger.LegacyPrintf("service.ops_metrics", "[OpsMetricsCollector] system stats error: %v", err)
	}

	dbOK := c.observer.CheckDB(ctx)
	redisOK := c.observer.CheckRedis(ctx)
	active, idle := c.observer.DbPoolStats()
	redisTotal, redisIdle, redisStatsOK := c.observer.RedisPoolStats()

	successCount, tokenConsumed, err := c.db.QueryUsageCounts(ctx, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("query usage counts: %w", err)
	}

	duration, ttft, err := c.db.QueryUsageLatency(ctx, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("query usage latency: %w", err)
	}

	ignoredStatusCodes := resolveOpsIgnoredStatusCodesFromRepo(ctx, c.settingRepo)
	errorTotal, businessLimited, errorSLA, upstreamExcl, upstream429, upstream529, err := c.db.QueryErrorCounts(ctx, windowStart, windowEnd, ignoredStatusCodes)
	if err != nil {
		return fmt.Errorf("query error counts: %w", err)
	}

	providerSwitchCount, err := c.db.QueryProviderSwitchCount(ctx, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("query provider switch counts: %w", err)
	}

	windowSeconds := windowEnd.Sub(windowStart).Seconds()
	// Use a minute denominator for an unusable window length.
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	requestTotal := successCount + errorTotal
	// Divide totals by elapsed window seconds, including idle seconds. QPS
	// combines usage observations with final errors; TPS uses all counted consumed
	// tokens.
	qps := float64(requestTotal) / windowSeconds
	tps := float64(tokenConsumed) / windowSeconds

	goroutines := c.observer.GoroutineCount()
	concurrencyQueueDepth := c.collectConcurrencyQueueDepth(ctx)

	input := &OpsInsertSystemMetricsInput{
		CreatedAt:     windowEnd,
		WindowMinutes: 1,

		SuccessCount:         successCount,
		ErrorCountTotal:      errorTotal,
		BusinessLimitedCount: businessLimited,
		ErrorCountSLA:        errorSLA,

		UpstreamErrorCountExcl429529: upstreamExcl,
		Upstream429Count:             upstream429,
		Upstream529Count:             upstream529,

		TokenConsumed:       tokenConsumed,
		ProviderSwitchCount: providerSwitchCount,
		QPS:                 float64Ptr(RoundTo1DP(qps)),
		TPS:                 float64Ptr(RoundTo1DP(tps)),

		DurationP50Ms: duration.P50,
		DurationP90Ms: duration.P90,
		DurationP95Ms: duration.P95,
		DurationP99Ms: duration.P99,
		DurationAvgMs: duration.Avg,
		DurationMaxMs: duration.Max,

		TTFTP50Ms: ttft.P50,
		TTFTP90Ms: ttft.P90,
		TTFTP95Ms: ttft.P95,
		TTFTP99Ms: ttft.P99,
		TTFTAvgMs: ttft.Avg,
		TTFTMaxMs: ttft.Max,

		CPUUsagePercent:    sys.CpuUsagePercent,
		MemoryUsedMB:       sys.MemoryUsedMB,
		MemoryTotalMB:      sys.MemoryTotalMB,
		MemoryUsagePercent: sys.MemoryUsagePercent,
		DiskUsedMB:         sys.DiskUsedMB,
		DiskTotalMB:        sys.DiskTotalMB,
		DiskUsagePercent:   sys.DiskUsagePercent,

		DBOK:    boolPtr(dbOK),
		RedisOK: boolPtr(redisOK),

		// Keep unavailable Redis pool counts null while successful reads can report
		// zero; both fields use the same availability flag.
		RedisConnTotal: func() *int {
			if !redisStatsOK {
				return nil
			}
			return intPtr(redisTotal)
		}(),
		RedisConnIdle: func() *int {
			if !redisStatsOK {
				return nil
			}
			return intPtr(redisIdle)
		}(),

		DBConnActive:          intPtr(active),
		DBConnIdle:            intPtr(idle),
		GoroutineCount:        intPtr(goroutines),
		ConcurrencyQueueDepth: concurrencyQueueDepth,
	}

	// Persist the assembled observation after required queries succeed. Job
	// outcome recording occurs later in collectOnce as a separate write.
	return c.opsRepo.InsertSystemMetrics(ctx, input)
}

// collectConcurrencyQueueDepth returns the sum of positive waiting counts, or
// nil when required readers are absent or either query fails. An empty provider
// list or a successful batch with no positive counts returns an observed zero.
//
// The child context requests a two-second sampling budget. Readers can
// implement their own cancellation behavior: scheduler.ConcurrencyService can
// reuse cached results or detach a batch fetch into its own context. Every
// returned load-map entry contributes regardless of its key. The accumulated
// int64 is clamped before conversion to int, although that final clamp does not
// prevent overflow during addition.
func (c *OpsMetricsCollector) collectConcurrencyQueueDepth(parentCtx context.Context) *int {
	if c == nil || c.providerRepo == nil || c.concurrencyService == nil {
		return nil
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	// Request a short child context for optional queue sampling. The scheduler
	// reader may use cached data or an independently budgeted fetch.
	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
	defer cancel()

	providerLoads, err := c.listSchedulableProviderLoads(ctx)
	// Provider-list failure makes queue depth unavailable, so return nil while the
	// collector can still persist its other observations.
	if err != nil {
		return nil
	}
	if len(providerLoads) == 0 {
		zero := 0
		return &zero
	}

	// Read all provider loads in one batch after a successful listing; this
	// optional sample does not change the historical request window.
	loadMap, err := c.concurrencyService.GetProvidersLoadBatch(ctx, providerLoads)
	// A failed load batch also leaves queue depth null, keeping query failure
	// distinct from a measured empty queue.
	if err != nil {
		return nil
	}

	var total int64
	for _, info := range loadMap {
		if info == nil || info.WaitingCount <= 0 {
			continue
		}
		total += int64(info.WaitingCount)
	}
	// Clamp a negative aggregate before storing queue depth; this catches negative
	// wrapped totals but does not make the additions overflow-safe.
	if total < 0 {
		total = 0
	}

	// Cap the aggregate at the native int's maximum before narrowing it for the
	// metric DTO.
	maxInt := int64(^uint(0) >> 1)
	if total > maxInt {
		total = maxInt
	}
	v := int(total)
	return &v
}

// listSchedulableProviderLoads prefers the optional dedicated load listing and
// otherwise derives a fresh slice from provider observations. A dedicated
// listing error is returned directly and does not trigger an observation-list
// retry.
//
// The observation path skips nonpositive IDs and copies each positive ID with
// its EffectiveLoadFactor, which is the already resolved LoadFactor on
// ProviderObservation. The dedicated path forwards its rows as supplied. This
// distinction lets the collector use a smaller listing while retaining the load
// limits consumed by the concurrency reader.
func (c *OpsMetricsCollector) listSchedulableProviderLoads(ctx context.Context) ([]ProviderWithConcurrency, error) {
	if repo, ok := c.providerRepo.(opsSchedulableProviderLoadRepository); ok {
		return repo.ListSchedulableProviderLoads(ctx)
	}

	// Readers lacking the dedicated capability use provider observations; copying
	// their resolved load factors keeps queue sampling aligned with supplied
	// scheduler limits.
	providers, err := c.providerRepo.ListSchedulable(ctx)
	if err != nil {
		return nil, err
	}
	loads := make([]ProviderWithConcurrency, 0, len(providers))
	for _, provider := range providers {
		if provider.ID <= 0 {
			continue
		}
		loads = append(loads, ProviderWithConcurrency{
			ID:             provider.ID,
			MaxConcurrency: provider.EffectiveLoadFactor(),
		})
	}
	return loads, nil
}

// tryAcquireLeaderLock selects the collector's leadership mechanism and returns
// cleanup when ownership is acquired. A nil receiver or absent Redis adapter
// permits an unlocked run, and a nil context uses Background.
//
// A Redis error attempts an independent database advisory lock. Redis
// contention skips the attempt directly, and advisory refusal also skips it.
// The two mechanisms do not share a common lock state, and the Redis lease is
// not renewed. Redis cleanup uses a separate short context and ignores release
// errors; its owner comparison prevents deleting a successor's lease.
func (c *OpsMetricsCollector) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if c == nil || c.redisClient == nil {
		return nil, true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ok, err := c.redisClient.Claim(ctx, opsMetricsCollectorLeaderLockKey, c.instanceID, opsMetricsCollectorLeaderLockTTL)
	if err != nil {
		// Attempt the database lock after a Redis error; its session ownership is
		// independent of Redis, so mixed mechanisms can still overlap.
		release, ok := acquireAdvisory(ctx, c.db, opsMetricsCollectorLeaderLockKey)
		if !ok {
			c.maybeLogSkip()
			return nil, false
		}
		return release, true
	}
	if !ok {
		c.maybeLogSkip()
		return nil, false
	}

	// Release the lease after outcome handling with a new context so expired
	// collection cancellation cannot immediately cancel this cleanup attempt.
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Suppress cleanup failure; the lease can remain until expiry, and owner
		// checking keeps a successor's lease intact.
		_ = c.redisClient.Release(ctx, opsMetricsCollectorLeaderLockKey, c.instanceID)
	}
	return release, true
}

// maybeLogSkip emits at most one leadership-skip diagnostic per minute for this
// collector. It requires a nonnil receiver and serializes the time check,
// timestamp update, and logging under skipLogMu.
//
// The first skip emits immediately. A recent timestamp suppresses another
// message until the elapsed interval reaches one minute. Callers use this
// diagnostic for both Redis contention and failed advisory acquisition, so its fixed message
// does not establish which locking condition occurred. It has no effect on
// heartbeat persistence.
func (c *OpsMetricsCollector) maybeLogSkip() {
	// Serialize checking and recording the diagnostic time so overlapping skipped
	// attempts cannot each emit within the same minute.
	c.skipLogMu.Lock()
	defer c.skipLogMu.Unlock()

	now := time.Now()
	if !c.skipLogAt.IsZero() && now.Sub(c.skipLogAt) < time.Minute {
		return
	}
	c.skipLogAt = now
	logger.LegacyPrintf("service.ops_metrics", "[OpsMetricsCollector] leader lock held by another instance; skipping")
}

// float64Ptr returns a pointer to a copy of v.
func float64Ptr(v float64) *float64 {
	out := v
	return &out
}

// intPtr returns a pointer to a copy of v.
func intPtr(v int) *int {
	out := v
	return &out
}

// boolPtr returns a pointer to a copy of v.
func boolPtr(v bool) *bool {
	out := v
	return &out
}

// RoundTo1DP rounds a floating-point metric to one decimal place using
// math.Round.
func RoundTo1DP(v float64) float64 {
	// Scale before rounding and restore the original metric unit.
	return math.Round(v*10) / 10
}
