package ops

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// alert_evaluator_service.go evaluates alert thresholds, maintains firing
// events, and dispatches admitted email notifications.
// Alert evaluation combines stored system samples and returned availability
// with raw request windows and process-local consecutive-breach counts. Missing
// observations reset those counts and leave firing events unchanged. A valid
// evaluation creates an event only after sustained counts, scoped silence, and
// cooldown checks permit it; an existing firing event can instead be resolved
// when the combined breach condition fails.
//
// Event persistence precedes email callbacks, whose per-recipient failures are
// suppressed and whose rolling limit consumes admitted attempts. A successful
// heartbeat can therefore include failed rule actions or mail, while only
// failure to list rules takes the error-heartbeat path. Redis leadership is
// optional and uses an unrenewed lease; state continuity and email reservations
// remain per instance. Startup and shutdown require caller ordering because
// separate once guards do not serialize loop registration against waiting.

// opsAlertEvaluatorJobName and the evaluator constants govern job status, cycle
// timeout, Redis lease fallback values, and the shared contention-diagnostic
// interval.
const (
	// evaluator heartbeat identity.
	opsAlertEvaluatorJobName = "ops_alert_evaluator"

	// single cycle query budget.
	opsAlertEvaluatorTimeout = 45 * time.Second
	// blank runtime-key fallback.
	opsAlertEvaluatorLeaderLockKey = "ops:alert:evaluator:leader"
	// nonpositive runtime-TTL fallback.
	opsAlertEvaluatorLeaderLockTTL = 90 * time.Second
	// contention diagnostic cadence.
	opsAlertEvaluatorSkipLogInterval = 1 * time.Minute
)

// OpsAlertEvaluatorService applies alert rules, stores event transitions, and
// attempts configured email delivery.
type OpsAlertEvaluatorService struct {
	// opsService supplies monitoring, runtime preferences, and availability
	// reads when present. Without it, request metrics use a
	// repository-backed OpsService and email delivery is skipped.
	opsService *OpsService // runtime settings and metrics
	// opsRepo is required for cycle work and accepted-mail marking. A
	// missing repository makes evaluateOnce return before inspecting rules
	// or recording heartbeats.
	opsRepo AlertEvaluatorRepository // rules events and heartbeats
	// emailService can be absent. When configured, bind SendEmail and the
	// enabled template callbacks before evaluation; callbacks and
	// dependencies remain shared.
	emailService *EmailDelivery // alert delivery callbacks
	// proxyRepo supplies current global proxy counts for proxy metric
	// rules. Missing readers and query failures make those metrics
	// unavailable.
	proxyRepo ProxyStatsReader // proxy expiration counts

	// redisClient can be absent, permitting unlocked cycles. A configured
	// client's claim error or contention rejects evaluation; claimed leases
	// are not renewed.
	redisClient LeaderLeaseCache // optional evaluator lease cache
	// cfg is retained and read during cycles and diagnostics. Configure it
	// before starting; a nil cfg permits evaluation and suppresses
	// reportOptions output.
	cfg        *Options // deployment evaluator options
	instanceID string   // Redis lease owner

	// stopCh, startOnce, stopOnce, and wg coordinate the one registered
	// loop and its join. Callers serialize Start before Stop because
	// independent once guards provide no common lifecycle barrier.
	stopCh    chan struct{}  // loop termination signal
	startOnce sync.Once      // first loop registration
	stopOnce  sync.Once      // first signal closure
	wg        sync.WaitGroup // registered loop completion

	// mu protects ruleStates and the LastEvaluatedAt and
	// ConsecutiveBreaches fields in its state objects. Removed rules lose state,
	// while disabled rules retain entries until later evaluation or
	// pruning.
	mu         sync.Mutex                   // breach state mutex
	ruleStates map[int64]*opsAlertRuleState // per-rule process state

	// emailLimiter is initialized by the constructor and reserves recipient
	// attempts locally. Accepted mail failures retain reservations;
	// disabled limits admit without recording timestamps.
	emailLimiter *slidingWindowLimiter // process recipient attempt limiter

	// skipLogMu protects skipLogAt and the selected contention report as
	// one operation. All lease keys share the same diagnostic allowance,
	// which is updated even when configured reporting emits nothing.
	skipLogMu sync.Mutex // contention diagnostic mutex
	skipLogAt time.Time  // last selected skip diagnostic

	// warnNoRedisOnce is shared by missing-client and claim-error
	// diagnostics, so the first selected warning consumes both paths'
	// per-instance allowance.
	warnNoRedisOnce sync.Once // missing-or-failed Redis diagnostic guard
}

// opsAlertRuleState records one rule's local evaluation continuity and breach
// count.
type opsAlertRuleState struct {
	// LastEvaluatedAt and ConsecutiveBreaches are mutated under the owning
	// service mutex. Unavailable metrics reset the count, and a gap
	// exceeding twice the current interval breaks continuity.
	LastEvaluatedAt     time.Time // last local evaluation instant
	ConsecutiveBreaches int       // consecutive valid breach count
}

// slidingWindowLimiter counts local recipient admissions inside one rolling
// time window.
type slidingWindowLimiter struct {
	// mu serializes limit changes with pruning and admission into sent.
	// window is constructor-defined and read during Allow; the limiter does
	// not coordinate reservations across processes.
	mu sync.Mutex // admission state mutex
	// limit permits unrecorded admissions when nonpositive. SetLimit
	// changes this value while retaining the timestamps previously recorded
	// under positive limits.
	limit int // configured admission count cap
	// window is set at construction, defaults to one hour when nonpositive,
	// and stays unchanged while SetLimit adjusts admission capacity.
	window time.Duration // retained admission interval
	// sent retains admissions made with a positive limit, including
	// attempts that later fail delivery. Allow reuses its backing slice
	// while pruning.
	sent []time.Time // recorded limited admission instants
}

// NewOpsAlertEvaluatorService binds evaluator dependencies and initializes
// local breach state, a unique lease owner, and an initially unlimited hourly
// recipient limiter. Construction starts no work; optional query and delivery
// dependencies remain caller-owned references.
func NewOpsAlertEvaluatorService(
	opsService *OpsService,
	opsRepo AlertEvaluatorRepository,
	emailService *EmailDelivery,
	redisClient LeaderLeaseCache,
	cfg *Options,
	proxyRepo ProxyStatsReader,
) *OpsAlertEvaluatorService {
	return &OpsAlertEvaluatorService{
		opsService:   opsService,
		opsRepo:      opsRepo,
		emailService: emailService,
		proxyRepo:    proxyRepo,
		redisClient:  redisClient,
		cfg:          cfg,
		instanceID:   uuid.NewString(),
		ruleStates:   map[int64]*opsAlertRuleState{},
		emailLimiter: newSlidingWindowLimiter(0, time.Hour),
	}
}

// Start registers one evaluator loop, accepting a nil receiver as a no-op. It
// creates a stop channel when needed and registers the wait count before
// launching work; dependency and deployment gates are checked by each cycle.
//
// Callers finish startup before invoking Stop and serialize lifecycle calls.
// Separate once guards do not protect stop-channel publication or a first
// WaitGroup increment against shutdown. Stopping before this registration
// consumes the closure guard, so later startup can leave an open termination
// channel.
func (s *OpsAlertEvaluatorService) Start() {
	if s == nil {
		return
	}
	// Publish the stop channel and register the wait count before launch;
	// repeated starts share this guard, but Stop uses another guard.
	s.startOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		s.wg.Add(1)
		go s.run()
	})
}

// Stop closes the published termination channel once and waits for registered
// evaluator work. Call it after Start under serialized lifecycle ownership; it
// accepts a nil receiver and repeated calls share the completed closure
// attempt.
//
// Active queries use a background cycle timeout and are not cancelled by this
// channel; accepted-mail marker updates use Background without that deadline. A
// ready timer can also win the select before the closed channel is observed.
// Stop before registration consumes stopOnce with no channel, leaving a later
// loop outside this signal. Stop has no caller deadline.
func (s *OpsAlertEvaluatorService) Stop() {
	if s == nil {
		return
	}
	// Consume the first closure attempt even when registration has not
	// published a channel; the subsequent wait joins only work already
	// registered.
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.wg.Wait()
}

// run owns the registered loop's wait-count release and reusable timer. Start
// establishes stopCh and increments wg before launch; direct calls require that
// same setup.
//
// A zero-delay timer admits the first evaluation through select, where an
// already-closed stop channel can compete with readiness. Every admitted cycle
// reads its interval before evaluating and resets the timer after work
// completes. Thus the delay follows completion, and shutdown waits for ongoing
// background-budget evaluation before the loop can observe its signal.
func (s *OpsAlertEvaluatorService) run() {
	// Own both wait-count release and timer cleanup here; Stop's join
	// completes after admitted evaluation and loop termination finish in
	// this goroutine.
	defer s.wg.Done()

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			interval := s.getInterval()
			s.evaluateOnce(interval)
			// Schedule the next delay after evaluation returns, so
			// a slow query increases the interval between cycle
			// starts instead of overlapping local work.
			timer.Reset(interval)
		case <-s.stopCh:
			return
		}
	}
}

// getInterval reads runtime cadence under a detached two-second budget and
// returns an interval in seconds. Missing service, failed settings, nil
// configuration, and values outside one second through one day use sixty
// seconds.
func (s *OpsAlertEvaluatorService) getInterval() time.Duration {
	interval := 60 * time.Second

	if s == nil || s.opsService == nil {
		return interval
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Read failure keeps the ordinary cadence for this cycle; the detached
	// read can finish even after Stop signals loop termination.
	cfg, err := s.opsService.GetOpsAlertRuntimeSettings(ctx)
	if err != nil || cfg == nil {
		return interval
	}
	if cfg.EvaluationIntervalSeconds <= 0 {
		return interval
	}
	if cfg.EvaluationIntervalSeconds < 1 {
		return interval
	}
	// Reject cadences longer than the runtime settings' accepted daily
	// maximum.
	if cfg.EvaluationIntervalSeconds > int((24 * time.Hour).Seconds()) {
		return interval
	}
	// Convert configured whole seconds into the timer's duration unit.
	return time.Duration(cfg.EvaluationIntervalSeconds) * time.Second
}

// evaluateOnce applies one rule snapshot under a background query budget, using
// constructor-initialized breach state and email limiter. Missing storage,
// disabled deployment Ops, refused monitoring, or refused leadership ends the
// cycle before heartbeat recording. Only list-rule failure records an error
// heartbeat.
//
// Request-rate metrics use completed-minute raw windows; system rules use the
// latest stored sample without an age cutoff, and availability rules use the
// returned status. Unavailable metrics reset continuity and leave firing events
// untouched. Valid observations advance local breach counts before event
// lookup. A sustained breach retains an existing firing event; otherwise that
// event is resolved even when the current value breaches but its consecutive
// count is still insufficient.
//
// Creating an event follows scoped silence and persisted cooldown checks, then
// email is attempted for a returned positive identity. Per-rule storage errors
// are logged and skipped, and recipient failures are suppressed. Success
// summarizes these handled attempts; emails_sent counts events with at least
// one nil-returning delivery callback, not recipients or independently
// confirmed SMTP receipts.
func (s *OpsAlertEvaluatorService) evaluateOnce(interval time.Duration) {
	if s == nil || s.opsRepo == nil {
		return
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}

	// The cycle uses its own background budget, so a stop-channel closure
	// waits for active query work instead of cancelling this context.
	ctx, cancel := context.WithTimeout(context.Background(), opsAlertEvaluatorTimeout)
	defer cancel()

	if s.opsService != nil && !s.opsService.IsMonitoringEnabled(ctx) {
		return
	}

	// Keep normalized alert defaults when runtime preference loading fails,
	// allowing admitted cycles to use the stored settings reader's ordinary
	// fallback values.
	runtimeCfg := defaultOpsAlertRuntimeSettings()
	if s.opsService != nil {
		if loaded, err := s.opsService.GetOpsAlertRuntimeSettings(ctx); err == nil && loaded != nil {
			runtimeCfg = loaded
		}
	}

	release, ok := s.tryAcquireLeaderLock(ctx, runtimeCfg.DistributedLock)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}

	startedAt := time.Now().UTC()
	runAt := startedAt

	// A failed rule snapshot ends evaluation and takes its error-heartbeat
	// path; failures later inside one rule are logged while the cycle
	// continues.
	rules, err := s.opsRepo.ListAlertRules(ctx)
	if err != nil {
		s.recordHeartbeatError(runAt, time.Since(startedAt), err)
		reportOptions(s.cfg, "[OpsAlertEvaluator] list rules failed: %v", err)
		return
	}

	rulesTotal := len(rules)
	rulesEnabled := 0
	rulesEvaluated := 0
	eventsCreated := 0
	eventsResolved := 0
	emailsSent := 0

	now := time.Now().UTC()
	// Exclude the incomplete minute from request-rate windows.
	safeEnd := now.Truncate(time.Minute)
	if safeEnd.IsZero() {
		safeEnd = now
	}

	// Ignore sample-read errors for this cycle; system rules become
	// unavailable when their field is absent, while other rule kinds can
	// still evaluate.
	systemMetrics, _ := s.opsRepo.GetLatestSystemMetrics(ctx, 1)

	// Drop state only for absent positive identities; disabled listed rules
	// keep their prior evaluation instant and breach count until revisited.
	s.pruneRuleStates(rules)

	for _, rule := range rules {
		if rule == nil || !rule.Enabled || rule.ID <= 0 {
			continue
		}
		rulesEnabled++

		scopePlatform, scopeGroupID, scopeRegion := parseOpsAlertRuleScope(rule.Filters)

		windowMinutes := rule.WindowMinutes
		if windowMinutes <= 0 {
			windowMinutes = 1
		}
		windowStart := safeEnd.Add(-time.Duration(windowMinutes) * time.Minute)
		windowEnd := safeEnd

		// Unavailable observations reset breach continuity but skip
		// event lookup, leaving existing firing status available until
		// a later usable evaluation.
		metricValue, ok := s.computeRuleMetric(ctx, rule, systemMetrics, windowStart, windowEnd, scopePlatform, scopeGroupID)
		if !ok {
			s.resetRuleState(rule.ID, now)
			continue
		}
		rulesEvaluated++

		breachedNow := compareMetric(metricValue, rule.Operator, rule.Threshold)
		required := requiredSustainedBreaches(rule.SustainedMinutes, interval)
		consecutive := s.updateRuleBreaches(rule.ID, now, interval, breachedNow)

		// The local breach count has already advanced; an event-lookup
		// error skips this rule's actions while later rules and cycle
		// completion remain possible.
		activeEvent, err := s.opsRepo.GetActiveAlertEvent(ctx, rule.ID)
		if err != nil {
			reportOptions(s.cfg, "[OpsAlertEvaluator] get active event failed (rule=%d): %v", rule.ID, err)
			continue
		}

		if breachedNow && consecutive >= required {
			if activeEvent != nil {
				continue
			}

			// Repository silence applies only when a platform
			// selector exists; ignore its lookup failure and permit
			// cooldown/event selection to continue.
			if s.opsService != nil {
				platform := strings.TrimSpace(scopePlatform)
				region := scopeRegion
				if platform != "" {
					if ok, err := s.opsService.IsAlertSilenced(ctx, rule.ID, platform, scopeGroupID, region, now); err == nil && ok {
						continue
					}
				}
			}

			// Read the latest persisted fire time before insertion
			// for cooldown selection; this separate read provides
			// no atomic reservation of a firing event. Read failure
			// skips this rule's creation while later rules can
			// proceed.
			latestEvent, err := s.opsRepo.GetLatestAlertEvent(ctx, rule.ID)
			if err != nil {
				reportOptions(s.cfg, "[OpsAlertEvaluator] get latest event failed (rule=%d): %v", rule.ID, err)
				continue
			}
			if latestEvent != nil && rule.CooldownMinutes > 0 {
				cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
				if now.Sub(latestEvent.FiredAt) < cooldown {
					continue
				}
			}

			firedEvent := &OpsAlertEvent{
				RuleID:         rule.ID,
				Severity:       strings.TrimSpace(rule.Severity),
				Status:         OpsAlertStatusFiring,
				Title:          fmt.Sprintf("%s: %s", strings.TrimSpace(rule.Severity), strings.TrimSpace(rule.Name)),
				Description:    buildOpsAlertDescription(rule, metricValue, windowMinutes, scopePlatform, scopeGroupID),
				MetricValue:    float64Ptr(metricValue),
				ThresholdValue: float64Ptr(rule.Threshold),
				Dimensions:     buildOpsAlertDimensions(scopePlatform, scopeGroupID),
				FiredAt:        now,
				CreatedAt:      now,
			}

			// Persist the firing event before mail work; a later
			// callback or marker failure leaves the created
			// observation available for administrators. A returned
			// insertion error skips this rule's mail and continues
			// evaluation.
			created, err := s.opsRepo.CreateAlertEvent(ctx, firedEvent)
			if err != nil {
				reportOptions(s.cfg, "[OpsAlertEvaluator] create event failed (rule=%d): %v", rule.ID, err)
				continue
			}

			eventsCreated++
			if created != nil && created.ID > 0 {
				if s.maybeSendAlertEmail(ctx, runtimeCfg, rule, created) {
					emailsSent++
				}
			}
			continue
		}

		// The combined breach-and-count condition failed, including a
		// current breach with insufficient continuity; resolve the
		// existing firing event through a separate persistent update.
		if activeEvent != nil {
			resolvedAt := now
			// Log a returned status-write error and continue later
			// rules; the heartbeat's resolved count increases only
			// when this call returns nil.
			if err := s.opsRepo.UpdateAlertEventStatus(ctx, activeEvent.ID, OpsAlertStatusResolved, &resolvedAt); err != nil {
				reportOptions(s.cfg, "[OpsAlertEvaluator] resolve event failed (event=%d): %v", activeEvent.ID, err)
			} else {
				eventsResolved++
			}
		}
	}

	// Bound the summary under the job's UTF-8 byte cap; individual rule
	// failures already skipped can coexist with this success status.
	result := truncateString(fmt.Sprintf("rules=%d enabled=%d evaluated=%d created=%d resolved=%d emails_sent=%d", rulesTotal, rulesEnabled, rulesEvaluated, eventsCreated, eventsResolved, emailsSent), 2048)
	s.recordHeartbeatSuccess(runAt, time.Since(startedAt), result)
}

// pruneRuleStates removes local state for identities absent from the supplied
// rule list under mu. Positive disabled rules retain state; callers provide
// stable rule pointers while the method reads them.
func (s *OpsAlertEvaluatorService) pruneRuleStates(rules []*OpsAlertRule) {
	// Hold the state mutex through membership construction and deletion, so
	// other state updates cannot observe a partly pruned rule-state map.
	s.mu.Lock()
	defer s.mu.Unlock()

	live := map[int64]struct{}{}
	for _, r := range rules {
		if r != nil && r.ID > 0 {
			live[r.ID] = struct{}{}
		}
	}
	for id := range s.ruleStates {
		if _, ok := live[id]; !ok {
			delete(s.ruleStates, id)
		}
	}
}

// resetRuleState records an evaluation instant and clears the local breach
// count under mu. Nonpositive identities are ignored; positive identities
// require the constructor's initialized state map and create an entry when
// absent.
func (s *OpsAlertEvaluatorService) resetRuleState(ruleID int64, now time.Time) {
	if ruleID <= 0 {
		return
	}
	// Create or reset the pointed state while holding the owning mutex;
	// readers using the same mutex see timestamp and count together.
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.ruleStates[ruleID]
	if !ok {
		state = &opsAlertRuleState{}
		s.ruleStates[ruleID] = state
	}
	state.LastEvaluatedAt = now
	state.ConsecutiveBreaches = 0
}

// updateRuleBreaches updates one positive rule identity and returns its
// consecutive breach count under mu. Nonpositive identities return zero;
// callers use a service with an initialized state map and supply the current
// evaluation interval.
//
// A gap strictly greater than twice a positive interval resets previous
// continuity before the new observation. The supplied instant is then recorded,
// a breach increments the count, and a non-breach clears it. Nonpositive
// intervals skip gap detection, and changing a stored rule under the same
// identity does not itself reset its state.
func (s *OpsAlertEvaluatorService) updateRuleBreaches(ruleID int64, now time.Time, interval time.Duration, breached bool) int {
	if ruleID <= 0 {
		return 0
	}
	// Keep continuity checks, timestamp publication, and count changes
	// under one mutex so local evaluations of the same identity cannot
	// interleave them.
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.ruleStates[ruleID]
	if !ok {
		state = &opsAlertRuleState{}
		s.ruleStates[ruleID] = state
	}

	// A long gap invalidates prior consecutive observations; this threshold
	// uses the current configured interval and keeps equality within the
	// retained sequence.
	if !state.LastEvaluatedAt.IsZero() && interval > 0 {
		if now.Sub(state.LastEvaluatedAt) > interval*2 {
			state.ConsecutiveBreaches = 0
		}
	}

	state.LastEvaluatedAt = now
	if breached {
		state.ConsecutiveBreaches++
	} else {
		state.ConsecutiveBreaches = 0
	}
	return state.ConsecutiveBreaches
}

// requiredSustainedBreaches converts a requested sustained duration into a
// minimum number of observations. Nonpositive sustained minutes require one
// observation, while a nonpositive interval returns the positive minute count
// directly.
//
// For positive inputs, the ceiling of duration divided by cadence prevents a
// fractional observation from reducing the required count. The result is at
// least one, even when cadence exceeds the duration. This estimates sustained
// time from consecutive evaluations and does not measure continuous metric
// behavior between them; duration multiplication uses native int arithmetic.
func requiredSustainedBreaches(sustainedMinutes int, interval time.Duration) int {
	if sustainedMinutes <= 0 {
		return 1
	}
	if interval <= 0 {
		return sustainedMinutes
	}
	// Round fractional observation requirements upward for threshold
	// admission.
	required := int(math.Ceil(float64(sustainedMinutes*60) / interval.Seconds()))
	if required < 1 {
		return 1
	}
	return required
}

// parseOpsAlertRuleScope reads trimmed platform, group, and region selectors
// from filter values. Nil or unsupported entries leave their respective
// selector empty, and each returned scalar pointer is newly allocated.
//
// Group identity accepts positive float64, int64, int, or parsed decimal string
// values. Float conversion truncates without range validation, so a positive
// fraction below one produces a nonnil zero identity. String parse failures are
// ignored. Region is used for repository silence selection by evaluateOnce;
// metric computation and persisted event dimensions receive platform and group
// separately.
func parseOpsAlertRuleScope(filters map[string]any) (platform string, groupID *int64, region *string) {
	if filters == nil {
		return "", nil, nil
	}
	if v, ok := filters["platform"]; ok {
		if s, ok := v.(string); ok {
			platform = strings.TrimSpace(s)
		}
	}
	if v, ok := filters["group_id"]; ok {
		switch t := v.(type) {
		// JSON numbers arrive as float64; truncation accepts a positive
		// fraction as zero identity and does not check the integer
		// conversion range.
		case float64:
			if t > 0 {
				id := int64(t)
				groupID = &id
			}
		case int64:
			if t > 0 {
				id := t
				groupID = &id
			}
		case int:
			if t > 0 {
				id := int64(t)
				groupID = &id
			}
		// Accept positive decimal string identities for stored filter
		// compatibility; malformed strings leave the group selector
		// absent rather than failing the whole rule.
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			if err == nil && n > 0 {
				groupID = &n
			}
		}
	}
	if v, ok := filters["region"]; ok {
		if s, ok := v.(string); ok {
			vv := strings.TrimSpace(s)
			if vv != "" {
				region = &vv
			}
		}
	}
	return platform, groupID, region
}

// computeRuleMetric selects an observation for the rule's metric kind and
// reports whether it is usable. Nil rules, unavailable dependencies, query
// failures, and absent required values return false, causing evaluateOnce to
// reset continuity.
//
// System metrics use the supplied snapshot without inspecting freshness or
// scope. Group metrics require a positive identity before reading availability.
// Each availability rule performs its own query, then computeAvailabilityMetric
// selects the value from that observation. A returned missing group can be a
// valid zero count or ratio.
//
// Proxy metrics query current global proxy state. Remaining metric names query
// a raw dashboard window, using a repository-backed service when needed.
// Request ratios require positive SLA request count and are converted to
// percentages; unknown names still take that dashboard path before returning
// false.
func (s *OpsAlertEvaluatorService) computeRuleMetric(
	ctx context.Context,
	rule *OpsAlertRule,
	systemMetrics *OpsSystemMetricsSnapshot,
	start time.Time,
	end time.Time,
	platform string,
	groupID *int64,
) (float64, bool) {
	if rule == nil {
		return 0, false
	}
	// Return unavailable when a required query or field cannot be read;
	// evaluateOnce then resets continuity instead of treating that absence
	// as a measured zero.
	switch metricType := strings.TrimSpace(rule.MetricType); metricType {
	case "cpu_usage_percent":
		if systemMetrics != nil && systemMetrics.CPUUsagePercent != nil {
			return *systemMetrics.CPUUsagePercent, true
		}
		return 0, false
	case "memory_usage_percent":
		if systemMetrics != nil && systemMetrics.MemoryUsagePercent != nil {
			return *systemMetrics.MemoryUsagePercent, true
		}
		return 0, false
	case "disk_usage_percent":
		if systemMetrics != nil && systemMetrics.DiskUsagePercent != nil {
			return *systemMetrics.DiskUsagePercent, true
		}
		return 0, false
	case "concurrency_queue_depth":
		if systemMetrics != nil && systemMetrics.ConcurrencyQueueDepth != nil {
			return float64(*systemMetrics.ConcurrencyQueueDepth), true
		}
		return 0, false
	case "group_available_providers", "group_available_ratio", "provider_rate_limited_count",
		"provider_error_count", "group_rate_limit_ratio", "provider_error_ratio", "overload_provider_count":
		switch metricType {
		case "group_available_providers", "group_available_ratio", "group_rate_limit_ratio":
			if groupID == nil || *groupID <= 0 {
				return 0, false
			}
		}
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetProviderAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		return computeAvailabilityMetric(metricType, availability)
	case "proxy_expired_count":
		if s == nil || s.proxyRepo == nil {
			return 0, false
		}
		n, err := s.proxyRepo.CountExpired(ctx)
		if err != nil {
			return 0, false
		}
		return float64(n), true
	case "proxy_expiring_soon_count":
		if s == nil || s.proxyRepo == nil {
			return 0, false
		}
		n, err := s.proxyRepo.CountExpiringSoon(ctx, time.Now())
		if err != nil {
			return 0, false
		}
		return float64(n), true
	}

	// Query the shared OpsService when bound; otherwise validate the raw window
	// and read traffic ratios directly from the narrowed evaluator repository
	// without invoking unrelated system-metric or heartbeat queries.
	filter := &OpsDashboardFilter{
		StartTime: start,
		EndTime:   end,
		Platform:  platform,
		GroupID:   groupID,
		QueryMode: OpsQueryModeRaw,
	}
	var (
		overview *OpsDashboardOverview
		err      error
	)
	if s.opsService != nil {
		overview, err = s.opsService.GetDashboardOverview(ctx, filter)
	} else {
		if s.opsRepo == nil || start.IsZero() || end.IsZero() || start.After(end) {
			return 0, false
		}
		filter.IgnoredStatusCodes = DefaultOpsIgnoredStatusCodes()
		overview, err = s.opsRepo.GetDashboardOverview(ctx, filter)
	}
	if err != nil {
		return 0, false
	}
	if overview == nil {
		return 0, false
	}

	switch strings.TrimSpace(rule.MetricType) {
	// Scale only dashboard fractions here for percentage thresholds; system
	// samples already carry percentages while queue, proxy, and provider
	// counts retain count-valued units.
	case "success_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.SLA * 100, true
	case "error_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.ErrorRate * 100, true
	case "upstream_error_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.UpstreamErrorRate * 100, true
	default:
		return 0, false
	}
}

// computeAvailabilityMetric calculates one indicator from a nonnil availability
// observation after computeRuleMetric validates the selected group and query.
// Missing groups yield usable zero group metrics, and nonpositive group totals
// yield zero ratios. Empty provider maps yield usable zero provider metrics;
// unsupported names return false. Callers keep the supplied observation stable
// while the calculation reads its values directly.
//
// Error counts exclude providers with any temporary-unschedulable deadline,
// including past deadlines. Error ratios divide by every provider map position,
// including nil entries skipped by the numerator. Group ratios use the supplied
// aggregate counts and can exceed one hundred percent.
func computeAvailabilityMetric(metricType string, availability *OpsProviderAvailability) (float64, bool) {
	switch metricType {
	case "group_available_providers":
		if availability.Group == nil {
			return 0, true
		}
		return float64(availability.Group.AvailableCount), true
	case "group_available_ratio":
		return computeGroupAvailableRatio(availability.Group), true
	case "provider_rate_limited_count":
		return float64(countProvidersByCondition(availability.Providers, func(acc *ProviderAvailability) bool {
			return acc.IsRateLimited
		})), true
	case "provider_error_count":
		return float64(countProvidersByCondition(availability.Providers, func(acc *ProviderAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})), true
	case "group_rate_limit_ratio":
		if availability.Group == nil || availability.Group.TotalProviders <= 0 {
			return 0, true
		}
		// Percentage output matches configured group rate-limit ratio thresholds.
		return (float64(availability.Group.RateLimitCount) / float64(availability.Group.TotalProviders)) * 100, true
	case "provider_error_ratio":
		total := int64(len(availability.Providers))
		if total <= 0 {
			return 0, true
		}
		errorCount := countProvidersByCondition(availability.Providers, func(acc *ProviderAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})
		// Percentage output matches configured provider-error ratio thresholds.
		return (float64(errorCount) / float64(total)) * 100, true
	case "overload_provider_count":
		return float64(countProvidersByCondition(availability.Providers, func(acc *ProviderAvailability) bool {
			return acc.IsOverloaded
		})), true
	default:
		return 0, false
	}
}

// compareMetric applies the trimmed comparison operator to the value and
// threshold using ordinary float64 comparisons. Unsupported operators return
// false; equality is exact and no tolerance is introduced for alert decisions.
func compareMetric(value float64, operator string, threshold float64) bool {
	switch strings.TrimSpace(operator) {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

// buildOpsAlertDimensions copies a trimmed nonblank platform and positive group
// identity into a newly allocated event map. With neither dimension it returns
// nil; region is supplied separately to scoped silence checks.
func buildOpsAlertDimensions(platform string, groupID *int64) map[string]any {
	dims := map[string]any{}
	if strings.TrimSpace(platform) != "" {
		dims["platform"] = strings.TrimSpace(platform)
	}
	if groupID != nil && *groupID > 0 {
		dims["group_id"] = *groupID
	}
	if len(dims) == 0 {
		return nil
	}
	return dims
}

// buildOpsAlertDescription formats the rule's metric, threshold, current value,
// and selected platform/group scope for an event. Nil rules return an empty
// string, nonpositive windows become one minute, and numeric values use two
// decimal places.
func buildOpsAlertDescription(rule *OpsAlertRule, value float64, windowMinutes int, platform string, groupID *int64) string {
	if rule == nil {
		return ""
	}
	scope := "overall"
	if strings.TrimSpace(platform) != "" {
		scope = fmt.Sprintf("platform=%s", strings.TrimSpace(platform))
	}
	if groupID != nil && *groupID > 0 {
		scope = fmt.Sprintf("%s group_id=%d", scope, *groupID)
	}
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	// Use the same decimal precision as alert template and plain-mail measurements.
	return fmt.Sprintf("%s %s %.2f (current %.2f) over last %dm (%s)",
		strings.TrimSpace(rule.MetricType),
		strings.TrimSpace(rule.Operator),
		rule.Threshold,
		value,
		windowMinutes,
		strings.TrimSpace(scope),
	)
}

// maybeSendAlertEmail returns whether at least one configured delivery callback
// returned nil for this event. Missing dependencies, already-marked events,
// disabled notifications, severity refusal, absent recipients, unavailable
// preferences, or runtime silence prevent attempts. Use constructor-initialized
// emailLimiter and a nonnil repository for accepted-mail marking.
//
// The process limiter reserves each nonblank recipient before template or plain
// sending, so failed delivery consumes capacity. Only callback-approved
// template errors try plain mail; other template errors and per-recipient send
// errors are suppressed. A nil template result can represent notification
// deduplication as well as delivery.
//
// Any accepted callback triggers a detached event EmailSent update whose error
// is ignored. The passed event object is not updated, and the boolean can be
// true despite marker failure. Event creation already happened, while later
// evaluateOnce cycles retain existing firing events and do not retry their
// email through this method.
func (s *OpsAlertEvaluatorService) maybeSendAlertEmail(ctx context.Context, runtimeCfg *OpsAlertRuntimeSettings, rule *OpsAlertRule, event *OpsAlertEvent) bool {
	if s == nil || s.emailService == nil || s.opsService == nil || event == nil || rule == nil {
		return false
	}
	if event.EmailSent {
		return false
	}
	if !rule.NotifyEmail {
		return false
	}

	// Unreadable mail preferences suppress this notification attempt while
	// the already-created event remains stored and later rule evaluation
	// can retain its firing status.
	emailCfg, err := s.opsService.GetEmailNotificationConfig(ctx)
	if err != nil || emailCfg == nil || !emailCfg.Alert.Enabled {
		return false
	}

	if len(emailCfg.Alert.Recipients) == 0 {
		return false
	}
	if !shouldSendOpsAlertEmailByMinSeverity(strings.TrimSpace(emailCfg.Alert.MinSeverity), strings.TrimSpace(rule.Severity)) {
		return false
	}

	if runtimeCfg != nil && runtimeCfg.Silencing.Enabled {
		if isOpsAlertSilenced(time.Now().UTC(), rule, event, runtimeCfg.Silencing) {
			return false
		}
	}

	// Apply the current preference before recipient admission, retaining
	// earlier limited attempts; this in-memory limiter is independent of
	// other evaluator instances.
	s.emailLimiter.SetLimit(emailCfg.Alert.RateLimitPerHour)

	subject := fmt.Sprintf("[Ops Alert][%s] %s", strings.TrimSpace(rule.Severity), strings.TrimSpace(rule.Name))
	body := buildOpsAlertEmailBody(rule, event)

	anySent := false
	for _, to := range emailCfg.Alert.Recipients {
		addr := strings.TrimSpace(to)
		if addr == "" {
			continue
		}
		// Reserve capacity before either mail path and keep it after
		// failure; this limit counts admitted recipient attempts rather
		// than confirmed deliveries.
		if !s.emailLimiter.Allow(time.Now().UTC()) {
			continue
		}
		// A nil template return accepts the attempt, including
		// notification deduplication; only callback-approved errors
		// proceed to plain mail, preventing delivery errors from being
		// blindly retried.
		if s.emailService.TemplatesEnabled {
			if err := s.emailService.SendTemplate(ctx, Notification{
				Event:          "ops.alert",
				RecipientEmail: addr,
				RecipientName:  s.emailService.RecipientName(addr),
				SourceType:     "ops_alert",
				SourceID:       fmt.Sprintf("%d", event.ID),
				Variables:      opsAlertEmailVariables(rule, event),
			}); err == nil {
				anySent = true
				continue
			} else if !s.emailService.ShouldFallback(err) {
				continue
			}
		}
		if err := s.emailService.SendEmail(ctx, addr, subject, body); err != nil {
			// Leave the admission reservation consumed after this
			// send fails and continue later recipients; the event
			// persists without a returned delivery error.
			continue
		}
		anySent = true
	}

	// Mark the event after any accepted callback using Background with no
	// method-specific timeout; ignored storage error leaves the true return
	// independent of durable marking.
	if anySent {
		_ = s.opsRepo.UpdateAlertEventEmailSent(context.Background(), event.ID, true)
	}
	return anySent
}

// opsAlertEmailVariables builds a new template map from the current rule and
// stored event. Missing fields initially use dashes and the current UTC
// instant; supplied blank rule strings replace their defaults as given after
// trimming.
//
// The event's metric, threshold, status, nonzero fire time, and nonblank
// description then override the applicable values. Rule name, severity, metric
// type, and operator remain current-rule values. Numeric values have two
// decimals and trigger time uses UTC RFC3339 seconds. Event description wins
// over rule description, while a missing event threshold leaves the rule
// threshold available.
func opsAlertEmailVariables(rule *OpsAlertRule, event *OpsAlertEvent) map[string]string {
	variables := map[string]string{
		"rule_name":         "-",
		"severity":          "-",
		"alert_status":      "-",
		"metric_type":       "-",
		"operator":          "-",
		"metric_value":      "-",
		"threshold_value":   "-",
		"triggered_at":      time.Now().UTC().Format(time.RFC3339),
		"alert_description": "-",
	}
	// Keep template measurement precision consistent with stored event descriptions
	// and plain-mail bodies, which also display two decimal places.
	if rule != nil {
		variables["rule_name"] = strings.TrimSpace(rule.Name)
		variables["severity"] = strings.TrimSpace(rule.Severity)
		variables["metric_type"] = strings.TrimSpace(rule.MetricType)
		variables["operator"] = strings.TrimSpace(rule.Operator)
		variables["threshold_value"] = fmt.Sprintf("%.2f", rule.Threshold)
		if strings.TrimSpace(rule.Description) != "" {
			variables["alert_description"] = strings.TrimSpace(rule.Description)
		}
	}
	// Apply persisted event measurements after current rule defaults so
	// saved threshold and nonblank event description take precedence during
	// template rendering.
	if event != nil {
		variables["alert_status"] = strings.TrimSpace(event.Status)
		if event.MetricValue != nil {
			variables["metric_value"] = fmt.Sprintf("%.2f", *event.MetricValue)
		}
		if event.ThresholdValue != nil {
			variables["threshold_value"] = fmt.Sprintf("%.2f", *event.ThresholdValue)
		}
		if !event.FiredAt.IsZero() {
			variables["triggered_at"] = event.FiredAt.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(event.Description) != "" {
			variables["alert_description"] = strings.TrimSpace(event.Description)
		}
	}
	return variables
}

// buildOpsAlertEmailBody renders escaped current rule labels with the stored
// event status and description. Nil rule or event returns an empty string;
// event threshold overrides the rule threshold, missing metric displays a dash,
// and FiredAt keeps its own RFC3339 zone.
func buildOpsAlertEmailBody(rule *OpsAlertRule, event *OpsAlertEvent) string {
	if rule == nil || event == nil {
		return ""
	}
	metric := strings.TrimSpace(rule.MetricType)
	value := "-"
	// Display measurements at the same decimal precision as the template variables.
	threshold := fmt.Sprintf("%.2f", rule.Threshold)
	if event.MetricValue != nil {
		value = fmt.Sprintf("%.2f", *event.MetricValue)
	}
	if event.ThresholdValue != nil {
		threshold = fmt.Sprintf("%.2f", *event.ThresholdValue)
	}
	// Escape interpolated labels and event text before embedding HTML; the
	// event fire time keeps its supplied zone, unlike UTC template
	// variables.
	return fmt.Sprintf(`
<h2>Ops Alert</h2>
<p><b>Rule</b>: %s</p>
<p><b>Severity</b>: %s</p>
<p><b>Status</b>: %s</p>
<p><b>Metric</b>: %s %s %s</p>
<p><b>Fired at</b>: %s</p>
<p><b>Description</b>: %s</p>
`,
		htmlEscape(rule.Name),
		htmlEscape(rule.Severity),
		htmlEscape(event.Status),
		htmlEscape(metric),
		htmlEscape(rule.Operator),
		htmlEscape(fmt.Sprintf("%s (threshold %s)", value, threshold)),
		event.FiredAt.Format(time.RFC3339),
		htmlEscape(event.Description),
	)
}

// shouldSendOpsAlertEmailByMinSeverity compares the evaluator's rule-priority
// mapping with a configured mail severity. Blank minimum permits sending; other
// minimums are trimmed and lowercased.
//
// P0 maps to critical, P1 to warning, and every other rule priority maps to
// info. The ordering is critical above warning above info. Unknown minimum
// labels receive rank zero and therefore permit every mapped priority. This
// decision reads the current rule severity rather than the event's saved
// severity.
func shouldSendOpsAlertEmailByMinSeverity(minSeverity string, ruleSeverity string) bool {
	minSeverity = strings.ToLower(strings.TrimSpace(minSeverity))
	if minSeverity == "" {
		return true
	}

	eventLevel := opsEmailSeverityForOps(ruleSeverity)
	minLevel := strings.ToLower(minSeverity)

	// Use the mail preference classes for ordering, with unknown minimum
	// labels ranked below every mapped rule class and therefore accepting
	// them.
	rank := func(level string) int {
		switch level {
		case "critical":
			return 3
		case "warning":
			return 2
		case "info":
			return 1
		default:
			return 0
		}
	}
	return rank(eventLevel) >= rank(minLevel)
}

// opsEmailSeverityForOps maps trimmed, case-insensitive P0 and P1 rule
// priorities to critical and warning mail classes. Every other value, including
// blank input, becomes info for the evaluator's minimum-severity comparison.
func opsEmailSeverityForOps(severity string) string {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "P0":
		return "critical"
	case "P1":
		return "warning"
	default:
		return "info"
	}
}

// isOpsAlertSilenced selects runtime mail suppression from a global deadline or
// the first applicable entry. Disabled silencing returns false and zero now
// uses the current UTC instant. Callers provide nonnil rule and event when
// entries contain severity filters, because those checks read their fields.
//
// Valid global silence applies strictly before its end. Valid entries remain
// active at equality and can restrict a supplied positive rule identity or
// match either current-rule or event severity after case folding. An absent or
// nonpositive rule bypasses the entry's rule-ID comparison. Malformed deadlines
// are ignored, and one rejected entry permits later entries to match.
func isOpsAlertSilenced(now time.Time, rule *OpsAlertRule, event *OpsAlertEvent, silencing OpsAlertSilencingSettings) bool {
	if !silencing.Enabled {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// Ignore malformed global deadline text and continue entry selection; a
	// valid global deadline excludes equality from its active interval.
	if strings.TrimSpace(silencing.GlobalUntilRFC3339) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(silencing.GlobalUntilRFC3339)); err == nil {
			if now.Before(t) {
				return true
			}
		}
	}

	for _, entry := range silencing.Entries {
		untilRaw := strings.TrimSpace(entry.UntilRFC3339)
		if untilRaw == "" {
			continue
		}
		// Skip an entry with malformed time while continuing later
		// entries; a valid entry at its exact deadline remains eligible
		// for matching.
		until, err := time.Parse(time.RFC3339, untilRaw)
		if err != nil {
			continue
		}
		if now.After(until) {
			continue
		}
		if entry.RuleID != nil && rule != nil && rule.ID > 0 && *entry.RuleID != rule.ID {
			continue
		}
		if len(entry.Severities) > 0 {
			match := false
			for _, s := range entry.Severities {
				if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(event.Severity)) || strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(rule.Severity)) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		return true
	}

	return false
}

// tryAcquireLeaderLock claims the configured evaluator lease and returns an
// owner-checked release callback. Use a nonnil service when locking is enabled.
// Disabled locking or missing Redis permits execution; configured Redis errors
// and contention refuse it. Blank key and nonpositive TTL use evaluator
// fallback constants. Claims are unrenewed, while release uses a detached
// five-second budget and ignores errors.
func (s *OpsAlertEvaluatorService) tryAcquireLeaderLock(ctx context.Context, lock OpsDistributedLockSettings) (func(), bool) {
	if !lock.Enabled {
		return nil, true
	}
	// Missing Redis permits local evaluation and selects a warning once;
	// this shares its diagnostic guard with configured-client claim
	// failures.
	if s.redisClient == nil {
		s.warnNoRedisOnce.Do(func() {
			reportOptions(s.cfg, "[OpsAlertEvaluator] redis not configured; running without distributed lock")
		})
		return nil, true
	}
	key := strings.TrimSpace(lock.Key)
	if key == "" {
		key = opsAlertEvaluatorLeaderLockKey
	}
	ttl := time.Duration(lock.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = opsAlertEvaluatorLeaderLockTTL
	}

	ok, err := s.redisClient.Claim(ctx, key, s.instanceID, ttl)
	if err != nil {
		// Reject the cycle when Redis cannot establish ownership; the
		// shared warning guard can already have been consumed by the
		// missing-client path.
		s.warnNoRedisOnce.Do(func() {
			reportOptions(s.cfg, "[OpsAlertEvaluator] leader lock SetNX failed; skipping this cycle: %v", err)
		})
		return nil, false
	}
	if !ok {
		s.maybeLogSkip(key)
		return nil, false
	}
	// Release through a detached five-second context so cycle cancellation
	// need not block owner cleanup; ignored release failure still leaves
	// expiration as the remaining lease exit.
	return func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		_ = s.redisClient.Release(releaseCtx, key, s.instanceID)
	}, true
}

// maybeLogSkip selects a contention diagnostic at most once per shared skip
// interval under skipLogMu. All supplied keys share skipLogAt, and timestamp
// selection advances even when reportOptions has no configured output.
func (s *OpsAlertEvaluatorService) maybeLogSkip(key string) {
	// Serialize timestamp selection with diagnostic emission; every key
	// uses one allowance and an absent reporting callback still consumes
	// the selected interval.
	s.skipLogMu.Lock()
	defer s.skipLogMu.Unlock()

	now := time.Now()
	if !s.skipLogAt.IsZero() && now.Sub(s.skipLogAt) < opsAlertEvaluatorSkipLogInterval {
		return
	}
	s.skipLogAt = now
	reportOptions(s.cfg, "[OpsAlertEvaluator] leader lock held by another instance; skipping (key=%q)", key)
}

// recordHeartbeatSuccess publishes a handled evaluation summary with a detached
// two-second budget. Missing storage skips persistence, blank result becomes
// ok, diagnostics are UTF-8 capped, duration becomes whole milliseconds, and
// storage failures are ignored.
func (s *OpsAlertEvaluatorService) recordHeartbeatSuccess(runAt time.Time, duration time.Duration, result string) {
	if s == nil || s.opsRepo == nil {
		return
	}
	now := time.Now().UTC()
	// Record elapsed time at whole-millisecond precision for job
	// diagnostics.
	durMs := duration.Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg := strings.TrimSpace(result)
	if msg == "" {
		msg = "ok"
	}
	// Cap the job diagnostic by UTF-8 byte length.
	msg = truncateString(msg, 2048)
	// Record status with a detached budget after cycle work, so
	// query-context expiry need not prevent diagnostics. Ignored write
	// failure cannot undo earlier event changes; a zero duration is omitted
	// by storage and can retain an older duration on conflict.
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAlertEvaluatorJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &now,
		LastDurationMs: &durMs,
		LastResult:     &msg,
	})
}

// recordHeartbeatError publishes a rule-list failure with a detached two-second
// budget and whole-millisecond duration. Missing storage or nil error skips
// persistence, the error text is UTF-8 capped, and write failure is ignored.
func (s *OpsAlertEvaluatorService) recordHeartbeatError(runAt time.Time, duration time.Duration, err error) {
	if s == nil || s.opsRepo == nil || err == nil {
		return
	}
	now := time.Now().UTC()
	// Record elapsed time at whole-millisecond precision for job
	// diagnostics.
	durMs := duration.Milliseconds()
	// Cap the job diagnostic by UTF-8 byte length.
	msg := truncateString(err.Error(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Use detached status persistence after a rule-snapshot failure;
	// ignored storage error leaves older job diagnostics available. A zero
	// duration is omitted by storage and can retain an earlier duration on
	// conflict.
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAlertEvaluatorJobName,
		LastRunAt:      &runAt,
		LastErrorAt:    &now,
		LastError:      &msg,
		LastDurationMs: &durMs,
	})
}

// htmlEscape escapes ampersands, angle brackets, and quote characters for
// interpolated HTML text. Supply unescaped input; existing entities have their
// ampersands escaped again, and callers retain responsibility for the
// surrounding HTML structure.
func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

// newSlidingWindowLimiter constructs an empty local admission history with the
// supplied limit and interval. A nonpositive window becomes one hour, and
// nonpositive limits allow admissions without recording timestamps until a
// positive limit is configured.
func newSlidingWindowLimiter(limit int, window time.Duration) *slidingWindowLimiter {
	if window <= 0 {
		window = time.Hour
	}
	return &slidingWindowLimiter{
		limit:  limit,
		window: window,
		sent:   []time.Time{},
	}
}

// SetLimit replaces the admission cap under mu while retaining recorded
// timestamps and the constructor's window. Nonpositive values disable recording
// and refusal; a later positive limit resumes checking the retained history
// through Allow.
func (l *slidingWindowLimiter) SetLimit(limit int) {
	// Serialize capacity replacement with Allow's history check and
	// reservation, so concurrent admissions see either complete old or
	// complete new limits.
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
}

// Allow reserves one admission under mu when the retained history is below a
// positive limit. Callers supply the instant; a nonpositive limit returns true
// before pruning or recording, so such admissions consume no stored capacity.
//
// Enabled checks reuse the history slice, retain timestamps strictly after now
// minus window, and include any future entries when the supplied clock moves
// backward. An entry at the cutoff expires. Pruning and reservation share the
// lock, preventing parallel callers from taking the same remaining slot.
// Subsequent delivery failure leaves this reservation recorded.
func (l *slidingWindowLimiter) Allow(now time.Time) bool {
	// Hold one lock across pruning, count comparison, and reservation;
	// failed downstream mail cannot release a slot through this local
	// limiter.
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.limit <= 0 {
		return true
	}
	cutoff := now.Add(-l.window)
	// Reuse owned slice storage while discarding expired admissions; strict
	// cutoff comparison retains future timestamps and removes admissions
	// exactly one window old.
	keep := l.sent[:0]
	for _, t := range l.sent {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.sent = keep
	if len(l.sent) >= l.limit {
		return false
	}
	l.sent = append(l.sent, now)
	return true
}

// computeGroupAvailableRatio returns AvailableCount as a percentage of
// TotalProviders for a supplied group. Nil groups and nonpositive totals return
// zero; positive totals use the counts as supplied and can produce values above
// one hundred.
//
// Eight available providers out of ten produce eighty percent. Metric selection
// treats an absent group as a usable zero once its availability query succeeds,
// so a zero threshold comparison differs from an unavailable query that resets
// local evaluation continuity.
func computeGroupAvailableRatio(group *GroupAvailability) float64 {
	if group == nil || group.TotalProviders <= 0 {
		return 0
	}
	// Percentage output matches this evaluator's configured group-ratio
	// thresholds.
	return (float64(group.AvailableCount) / float64(group.TotalProviders)) * 100
}

// countProvidersByCondition counts nonnil provider values accepted by the
// predicate while reading the supplied map. Empty maps and nil predicates
// return zero; keys do not affect selection, and callers keep the map stable
// during evaluation.
func countProvidersByCondition(providers map[int64]*ProviderAvailability, condition func(*ProviderAvailability) bool) int64 {
	if len(providers) == 0 || condition == nil {
		return 0
	}
	var count int64
	for _, provider := range providers {
		if provider != nil && condition(provider) {
			count++
		}
	}
	return count
}
