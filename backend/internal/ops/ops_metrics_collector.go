// 采样编排保持原调用顺序，SQL 与主机读取分别由 Adapter 执行。
package ops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	opsMetricsCollectorJobName     = "ops_metrics_collector"
	opsMetricsCollectorMinInterval = 60 * time.Second
	opsMetricsCollectorMaxInterval = 1 * time.Hour

	opsMetricsCollectorTimeout = 10 * time.Second

	opsMetricsCollectorLeaderLockKey = "ops:metrics:collector:leader"
	opsMetricsCollectorLeaderLockTTL = 90 * time.Second

	opsMetricsCollectorHeartbeatTimeout = 2 * time.Second
)

// opsSchedulableAccountLoadRepository 是 Ops 采样可选使用的轻量账号投影能力。
type opsSchedulableAccountLoadRepository interface {
	ListSchedulableAccountLoads(ctx context.Context) ([]AccountWithConcurrency, error)
}

type OpsMetricsCollector struct {
	lifecycleMu      sync.Mutex
	lifecycleStopped bool
	loopWG           sync.WaitGroup
	opsRepo          OpsRepository
	settingRepo      Settings
	cfg              *Options

	accountRepo        AccountLoadSource
	concurrencyService ConcurrencyReader

	db          MetricsSource
	redisClient RuntimeCache
	instanceID  string

	observer HostSampler

	stopCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once

	skipLogMu sync.Mutex
	skipLogAt time.Time
}

func NewOpsMetricsCollector(
	opsRepo OpsRepository,
	settingRepo Settings,
	accountRepo AccountLoadSource,
	concurrencyService ConcurrencyReader,
	db MetricsSource,
	redisClient RuntimeCache,
	cfg *Options,
	observer HostSampler,
) *OpsMetricsCollector {
	return &OpsMetricsCollector{
		opsRepo:            opsRepo,
		settingRepo:        settingRepo,
		cfg:                cfg,
		accountRepo:        accountRepo,
		concurrencyService: concurrencyService,
		db:                 db,
		redisClient:        redisClient,
		instanceID:         uuid.NewString(),
		observer:           observer,
	}
}

// @project-doc docs/operations/ops_monitoring_and_alerting.md#ops_signal_pipeline
func (c *OpsMetricsCollector) Start() {
	if c == nil {
		return
	}
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.lifecycleStopped {
		return
	}

	c.startOnce.Do(func() {
		if c.stopCh == nil {
			c.stopCh = make(chan struct{})
		}
		c.loopWG.Add(1)
		go func() { defer c.loopWG.Done(); c.run() }()
	})
}

func (c *OpsMetricsCollector) Stop() {
	if c == nil {
		return
	}
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

func (c *OpsMetricsCollector) run() {
	// First run immediately so the dashboard has data soon after startup.
	c.collectOnce()

	for {
		interval := c.getInterval()
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

func (c *OpsMetricsCollector) getInterval() time.Duration {
	interval := opsMetricsCollectorMinInterval

	if c.settingRepo == nil {
		return interval
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, err := c.settingRepo.GetValue(ctx, SettingKeyOpsMetricsIntervalSeconds)
	if err != nil {
		return interval
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return interval
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return interval
	}
	if seconds < int(opsMetricsCollectorMinInterval.Seconds()) {
		seconds = int(opsMetricsCollectorMinInterval.Seconds())
	}
	if seconds > int(opsMetricsCollectorMaxInterval.Seconds()) {
		seconds = int(opsMetricsCollectorMaxInterval.Seconds())
	}
	return time.Duration(seconds) * time.Second
}

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

	ctx, cancel := context.WithTimeout(context.Background(), opsMetricsCollectorTimeout)
	defer cancel()

	if !c.isMonitoringEnabled(ctx) {
		return
	}

	release, ok := c.tryAcquireLeaderLock(ctx)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}

	startedAt := time.Now().UTC()
	err := c.collectAndPersist(ctx)
	finishedAt := time.Now().UTC()

	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	dur := durationMs
	runAt := startedAt

	if err != nil {
		msg := truncateString(err.Error(), 2048)
		errAt := finishedAt
		hbCtx, hbCancel := context.WithTimeout(context.Background(), opsMetricsCollectorHeartbeatTimeout)
		defer hbCancel()
		_ = c.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
			JobName:        opsMetricsCollectorJobName,
			LastRunAt:      &runAt,
			LastErrorAt:    &errAt,
			LastError:      &msg,
			LastDurationMs: &dur,
		})
		log.Printf("[OpsMetricsCollector] collect failed: %v", err)
		return
	}

	successAt := finishedAt
	hbCtx, hbCancel := context.WithTimeout(context.Background(), opsMetricsCollectorHeartbeatTimeout)
	defer hbCancel()
	_ = c.opsRepo.UpsertJobHeartbeat(hbCtx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsMetricsCollectorJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &successAt,
		LastDurationMs: &dur,
	})
}

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
		if errors.Is(err, ErrSettingNotFound) {
			return true
		}
		// Fail-open: collector should not become a hard dependency.
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

func (c *OpsMetricsCollector) collectAndPersist(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Align to stable minute boundaries to avoid partial buckets and to maximize cache hits.
	now := time.Now().UTC()
	windowEnd := now.Truncate(time.Minute)
	windowStart := windowEnd.Add(-1 * time.Minute)

	sys, err := c.observer.CollectSystemStats(ctx)
	if err != nil {
		// Continue; system stats are best-effort.
		log.Printf("[OpsMetricsCollector] system stats error: %v", err)
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

	accountSwitchCount, err := c.db.QueryAccountSwitchCount(ctx, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("query account switch counts: %w", err)
	}

	windowSeconds := windowEnd.Sub(windowStart).Seconds()
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	requestTotal := successCount + errorTotal
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

		TokenConsumed:      tokenConsumed,
		AccountSwitchCount: accountSwitchCount,
		QPS:                float64Ptr(roundTo1DP(qps)),
		TPS:                float64Ptr(roundTo1DP(tps)),

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

	return c.opsRepo.InsertSystemMetrics(ctx, input)
}

func (c *OpsMetricsCollector) collectConcurrencyQueueDepth(parentCtx context.Context) *int {
	if c == nil || c.accountRepo == nil || c.concurrencyService == nil {
		return nil
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	// Best-effort: never let concurrency sampling break the metrics collector.
	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
	defer cancel()

	accountLoads, err := c.listSchedulableAccountLoads(ctx)
	if err != nil {
		return nil
	}
	if len(accountLoads) == 0 {
		zero := 0
		return &zero
	}

	loadMap, err := c.concurrencyService.GetAccountsLoadBatch(ctx, accountLoads)
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
	if total < 0 {
		total = 0
	}

	maxInt := int64(^uint(0) >> 1)
	if total > maxInt {
		total = maxInt
	}
	v := int(total)
	return &v
}

// listSchedulableAccountLoads 优先使用轻量投影，不支持时保持原仓储回退语义。
func (c *OpsMetricsCollector) listSchedulableAccountLoads(ctx context.Context) ([]AccountWithConcurrency, error) {
	if repo, ok := c.accountRepo.(opsSchedulableAccountLoadRepository); ok {
		return repo.ListSchedulableAccountLoads(ctx)
	}

	accounts, err := c.accountRepo.ListSchedulable(ctx)
	if err != nil {
		return nil, err
	}
	loads := make([]AccountWithConcurrency, 0, len(accounts))
	for _, account := range accounts {
		if account.ID <= 0 {
			continue
		}
		loads = append(loads, AccountWithConcurrency{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		})
	}
	return loads, nil
}

func (c *OpsMetricsCollector) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if c == nil || c.redisClient == nil {
		return nil, true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ok, err := c.redisClient.Claim(ctx, opsMetricsCollectorLeaderLockKey, c.instanceID, opsMetricsCollectorLeaderLockTTL)
	if err != nil {
		// Prefer fail-closed to avoid stampeding the database when Redis is flaky.
		// Fallback to a DB advisory lock when Redis is present but unavailable.
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

	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.redisClient.Release(ctx, opsMetricsCollectorLeaderLockKey, c.instanceID)
	}
	return release, true
}

func (c *OpsMetricsCollector) maybeLogSkip() {
	c.skipLogMu.Lock()
	defer c.skipLogMu.Unlock()

	now := time.Now()
	if !c.skipLogAt.IsZero() && now.Sub(c.skipLogAt) < time.Minute {
		return
	}
	c.skipLogAt = now
	log.Printf("[OpsMetricsCollector] leader lock held by another instance; skipping")
}
