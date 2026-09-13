package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultDashboardAggregationTimeout         = 2 * time.Minute
	defaultDashboardAggregationBackfillTimeout = 30 * time.Minute
	dashboardAggregationRetentionInterval      = 6 * time.Hour
	dashboardAggregationSchedulerTick          = 30 * time.Second
	dashboardAggregationBackfillBudget         = 10 * time.Second
	dashboardAggregationBackfillMaxHours       = 24

	dashboardAggregationLeaderLockKey = "dashboard:aggregation:leader"
	// 锁 TTL 需长于单轮定时聚合的最坏运行时间，避免任务未结束时锁过期。
	dashboardAggregationLeaderLockTTL = 5 * time.Minute
)

var (
	// ErrDashboardBackfillDisabled 当配置禁用回填时返回。
	ErrDashboardBackfillDisabled = errors.New("仪表盘聚合回填已禁用")
	// ErrDashboardBackfillTooLarge 当回填跨度超过限制时返回。
	ErrDashboardBackfillTooLarge   = errors.New("回填时间跨度过大")
	errDashboardAggregationRunning = errors.New("聚合作业正在运行")
)

// DashboardAggregationRepository 定义仪表盘预聚合仓储接口。
type DashboardAggregationRepository interface {
	AggregateRange(ctx context.Context, start, end time.Time) error
	// RecomputeRange 重新计算指定时间范围内的聚合数据（包含活跃用户等派生表）。
	// 设计目的：当 usage_logs 被批量删除/回滚后，确保聚合表可恢复一致性。
	RecomputeRange(ctx context.Context, start, end time.Time) error
	GetAggregationWatermark(ctx context.Context) (time.Time, error)
	UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error
	CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error
	CleanupUsageLogs(ctx context.Context, cutoff time.Time) error
	CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error
	EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error
}

// DashboardAggregationService 负责定时聚合与回填。
type DashboardAggregationService struct {
	reporter             func(string, string, ...any)
	runtimeMu            sync.Mutex
	runtimeStarted       bool
	runtimeStopped       bool
	runtimeWG            sync.WaitGroup
	runCtx               context.Context
	runCancel            context.CancelFunc
	stopOnce             sync.Once
	stopDone             chan struct{}
	repo                 DashboardAggregationRepository
	analyticsRepo        UsageAnalyticsAggregationRepository
	timingWheel          TimingWheel
	cfg                  DashboardAggregationConfig
	settings             AggregationSettings
	running              int32
	lastScheduledAt      atomic.Int64
	lastBackfillAt       atomic.Int64
	lastRetentionCleanup atomic.Value // time.Time
	locker               SingletonLocker
	instanceID           string
	// backfillBudget 仅供测试缩短单轮预算；生产为零时使用固定的 10 秒预算。
	backfillBudget time.Duration
}

// SetPreAggregationSettings 注入统一运行时配置，并在开启或修改周期时立即唤醒任务。
func (s *DashboardAggregationService) SetPreAggregationSettings(settings AggregationSettings) {
	if s == nil {
		return
	}
	s.settings = settings
	if analyticsRepo, ok := s.repo.(UsageAnalyticsAggregationRepository); ok {
		s.analyticsRepo = analyticsRepo
	}
	if settings != nil {
		settings.RegisterListener(func(previous, next PreAggregationSettings) {
			if (!previous.Usage.Enabled && next.Usage.Enabled) || previous.Usage.IntervalSeconds != next.Usage.IntervalSeconds {
				s.lastScheduledAt.Store(0)
				s.runBackground(s.runScheduledAggregation)
			}
		})
	}
}

// NewDashboardAggregationService 创建聚合服务。
func NewDashboardAggregationService(repo DashboardAggregationRepository, timingWheel TimingWheel, cfg *Options) *DashboardAggregationService {
	var aggCfg DashboardAggregationConfig
	if cfg != nil {
		aggCfg = cfg.DashboardAgg
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	return &DashboardAggregationService{reporter: usageReporter(cfg), runCtx: runCtx, runCancel: runCancel, stopDone: make(chan struct{}),
		repo:        repo,
		timingWheel: timingWheel,
		cfg:         aggCfg,
		instanceID:  uuid.NewString(),
	}
}

// SetSingletonLocker 绑定原有锁策略，核心只负责使用与释放。
func (s *DashboardAggregationService) SetSingletonLocker(locker SingletonLocker) { s.locker = locker }

// Start 启动定时聚合作业；实际启停和周期由统一运行时配置决定。
func (s *DashboardAggregationService) Start() {
	s.runtimeMu.Lock()
	if s.runtimeStarted || s.runtimeStopped {
		s.runtimeMu.Unlock()
		return
	}
	s.runtimeStarted = true
	s.runtimeMu.Unlock()

	if s == nil || s.repo == nil || s.timingWheel == nil {
		return
	}
	if !s.cfg.Enabled {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业已禁用")
		return
	}

	if s.cfg.RecomputeDays > 0 && s.analyticsRepo == nil {
		s.runBackground(s.recomputeRecentDays)
	}

	s.timingWheel.ScheduleRecurring("dashboard:aggregation", dashboardAggregationSchedulerTick, func() {
		s.runScheduledAggregation()
	})
	s.runBackground(s.runScheduledAggregation)
	s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业启动 (scheduler_tick=%v, lookback=%ds)", dashboardAggregationSchedulerTick, s.cfg.LookbackSeconds)
	if !s.cfg.BackfillEnabled {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 回填已禁用，如需补齐保留窗口以外历史数据请手动回填")
	}
}

// TriggerBackfill 触发回填（异步）。
func (s *DashboardAggregationService) TriggerBackfill(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !s.cfg.BackfillEnabled {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 回填被拒绝: backfill_enabled=false")
		return ErrDashboardBackfillDisabled
	}
	if !end.After(start) {
		return errors.New("回填时间范围无效")
	}
	if s.cfg.BackfillMaxDays > 0 {
		maxRange := time.Duration(s.cfg.BackfillMaxDays) * 24 * time.Hour
		if end.Sub(start) > maxRange {
			return ErrDashboardBackfillTooLarge
		}
	}
	if s.analyticsRepo == nil {
		return errors.New("多维聚合服务未初始化")
	}

	ctx, cancel := context.WithTimeout(s.operationContext(), defaultDashboardAggregationTimeout)
	defer cancel()
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		return err
	}
	requestedStart := start.UTC().Truncate(time.Hour)
	requestedCursor := end.UTC().Truncate(time.Hour)
	state.ManualBackfillStart = timePointer(requestedStart)
	state.ManualBackfillCursor = timePointer(requestedCursor)
	state.Phase = "backfill"
	if err := s.updateAnalyticsState(ctx, state, AnalyticsManualRequest); err != nil {
		return err
	}
	// 手动操作复用定时任务的分布式锁和资源预算。
	s.lastBackfillAt.Store(0)
	s.TriggerNow()
	return nil
}

// TriggerRecomputeRange 触发指定范围的重新计算（异步）。
// 与 TriggerBackfill 不同：
// - 不依赖 backfill_enabled（这是内部一致性修复）
// - 不受运行时开关影响，避免关闭期间删除原始记录后留下陈旧聚合
// - 不更新 watermark（避免影响正常增量聚合游标）
func (s *DashboardAggregationService) TriggerRecomputeRange(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !end.After(start) {
		return errors.New("重新计算时间范围无效")
	}

	s.runBackground(func() {
		const maxRetries = 3
		for i := 0; i < maxRetries; i++ {
			ctx, cancel := context.WithTimeout(s.operationContext(), defaultDashboardAggregationBackfillTimeout)
			err := s.recomputeRange(ctx, start, end)
			cancel()
			if err == nil {
				return
			}
			if !errors.Is(err, errDashboardAggregationRunning) {
				s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 重新计算失败: %v", err)
				return
			}
			select {
			case <-s.operationContext().Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 重新计算放弃: 聚合作业持续占用")
	})
	return nil
}

func (s *DashboardAggregationService) recomputeRecentDays() {
	days := s.cfg.RecomputeDays
	if days <= 0 {
		return
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -days)

	ctx, cancel := context.WithTimeout(s.operationContext(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	if err := s.backfillRange(ctx, start, now); err != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 启动重算失败: %v", err)
		return
	}
}

func (s *DashboardAggregationService) recomputeRange(ctx context.Context, start, end time.Time) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return errDashboardAggregationRunning
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	if err := s.repo.RecomputeRange(ctx, start, end); err != nil {
		return err
	}
	if s.analyticsRepo != nil {
		if err := s.analyticsRepo.RecomputeUsageAnalyticsRange(ctx, start, end); err != nil {
			return err
		}
	}
	s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 重新计算完成 (start=%s end=%s duration=%s)",
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		time.Since(jobStart).String(),
	)
	return nil
}

func (s *DashboardAggregationService) runScheduledAggregation() {
	if !s.usageEnabled(s.operationContext()) {
		return
	}
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.running, 0)
	// 取得运行权后再消费调度信号，避免立即触发与在途任务冲突时丢失唤醒。
	if !s.scheduledRunDue(time.Now()) {
		return
	}

	jobStart := time.Now().UTC()
	ctx, cancel := context.WithTimeout(s.operationContext(), defaultDashboardAggregationTimeout)
	defer cancel()

	// 多实例保护：仅主实例执行本轮聚合，避免重复聚合查询与水位竞争。
	release, ok := s.acquireSingleton(ctx)
	if !ok {
		return
	}
	defer release()

	now := time.Now().UTC()
	s.markAnalyticsRunStarted(ctx, now)
	last, err := s.repo.GetAggregationWatermark(ctx)
	if err != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 读取水位失败: %v", err)
		last = time.Unix(0, 0).UTC()
	}

	lookback := time.Duration(s.cfg.LookbackSeconds) * time.Second
	epoch := time.Unix(0, 0).UTC()
	start := last.Add(-lookback)
	if !last.After(epoch) {
		// 新实例只实时聚合当前窗口，历史数据交给受预算约束的反向回填，避免启动时长时间扫描。
		start = now.Add(-lookback)
	} else if start.After(now) {
		start = now.Add(-lookback)
	}
	if err := s.aggregateLiveRange(ctx, start, now); err != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 聚合失败: %v", err)
		s.markAnalyticsRunFailed(ctx, now, jobStart, err)
		return
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, now)
	if updateErr != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	slog.Debug("[DashboardAggregation] 聚合完成",
		"start", start.Format(time.RFC3339),
		"end", now.Format(time.RFC3339),
		"duration", time.Since(jobStart).String(),
		"watermark_updated", updateErr == nil,
	)
	s.markAnalyticsLiveSuccess(ctx, now, start, jobStart)
	s.runHistoricalBackfill(ctx, now)

	s.maybeCleanupRetention(ctx, now)
}

func (s *DashboardAggregationService) backfillRange(ctx context.Context, start, end time.Time) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return errDashboardAggregationRunning
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	startUTC := start.UTC()
	endUTC := end.UTC()
	if !endUTC.After(startUTC) {
		return errors.New("回填时间范围无效")
	}

	cursor := truncateToDayUTC(startUTC)
	for cursor.Before(endUTC) {
		windowEnd := cursor.Add(24 * time.Hour)
		if windowEnd.After(endUTC) {
			windowEnd = endUTC
		}
		if err := s.aggregateRange(ctx, cursor, windowEnd); err != nil {
			return err
		}
		cursor = windowEnd
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, endUTC)
	if updateErr != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 回填聚合完成 (start=%s end=%s duration=%s watermark_updated=%t)",
		startUTC.Format(time.RFC3339),
		endUTC.Format(time.RFC3339),
		time.Since(jobStart).String(),
		updateErr == nil,
	)

	s.maybeCleanupRetention(ctx, endUTC)
	return nil
}

func (s *DashboardAggregationService) aggregateRange(ctx context.Context, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	if err := s.repo.EnsureUsageLogsPartitions(ctx, end); err != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 分区检查失败: %v", err)
	}
	if err := s.repo.AggregateRange(ctx, start, end); err != nil {
		return err
	}
	if s.analyticsRepo != nil {
		return s.analyticsRepo.AggregateUsageAnalyticsRange(ctx, start, end)
	}
	return nil
}

// aggregateHourlyRange 只写入实时或自动回填所需的小时聚合表。
func (s *DashboardAggregationService) aggregateHourlyRange(ctx context.Context, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	if err := s.repo.EnsureUsageLogsPartitions(ctx, end); err != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 分区检查失败: %v", err)
	}
	if err := s.repo.AggregateRange(ctx, start, end); err != nil {
		return err
	}
	if s.analyticsRepo != nil {
		return s.analyticsRepo.AggregateUsageAnalyticsHourlyRange(ctx, start, end)
	}
	return nil
}

// aggregateLiveRange 每轮刷新小时表，并同步重建本轮实际触及的已闭合 UTC 日期。
func (s *DashboardAggregationService) aggregateLiveRange(ctx context.Context, start, end time.Time) error {
	if err := s.aggregateHourlyRange(ctx, start, end); err != nil || s.analyticsRepo == nil {
		return err
	}
	// 回看窗口跨午夜时可能持续吸收前一日迟到记录，必须在窗口离开该日期前持续刷新日表。
	dayStart := truncateToDayUTC(start)
	closedDayEnd := truncateToDayUTC(end)
	if !closedDayEnd.After(dayStart) {
		return nil
	}
	return s.analyticsRepo.RebuildUsageAnalyticsDailyRange(ctx, dayStart, closedDayEnd)
}

// aggregateAutomaticBackfillHour 在日期完成后生成日桶，日桶成功前不会推进调用方游标。
func (s *DashboardAggregationService) aggregateAutomaticBackfillHour(ctx context.Context, start, end, oldestHour time.Time) error {
	if err := s.aggregateHourlyRange(ctx, start, end); err != nil || s.analyticsRepo == nil {
		return err
	}
	chunkStart := start.UTC().Truncate(time.Hour)
	if chunkStart.Hour() != 0 && !chunkStart.Equal(oldestHour.UTC().Truncate(time.Hour)) {
		return nil
	}
	dayStart := truncateToDayUTC(chunkStart)
	return s.analyticsRepo.RebuildUsageAnalyticsDailyRange(ctx, dayStart, dayStart.AddDate(0, 0, 1))
}

func (s *DashboardAggregationService) maybeCleanupRetention(ctx context.Context, now time.Time) {
	lastAny := s.lastRetentionCleanup.Load()
	if lastAny != nil {
		if last, ok := lastAny.(time.Time); ok && now.Sub(last) < dashboardAggregationRetentionInterval {
			return
		}
	}

	hourlyCutoff := now.AddDate(0, 0, -s.cfg.Retention.HourlyDays)
	dailyCutoff := now.AddDate(0, 0, -s.cfg.Retention.DailyDays)
	usageCutoff := now.AddDate(0, 0, -s.cfg.Retention.UsageLogsDays)
	dedupCutoff := now.AddDate(0, 0, -s.cfg.Retention.UsageBillingDedupDays)

	aggErr := s.repo.CleanupAggregates(ctx, hourlyCutoff, dailyCutoff)
	if aggErr != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 聚合保留清理失败: %v", aggErr)
	}
	if s.analyticsRepo != nil {
		if analyticsErr := s.analyticsRepo.CleanupUsageAnalytics(ctx, hourlyCutoff, dailyCutoff); analyticsErr != nil {
			s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] 多维聚合保留清理失败: %v", analyticsErr)
			aggErr = analyticsErr
		}
	}
	usageErr := s.repo.CleanupUsageLogs(ctx, usageCutoff)
	if usageErr != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] usage_logs 保留清理失败: %v", usageErr)
	}
	dedupErr := s.repo.CleanupUsageBillingDedup(ctx, dedupCutoff)
	if dedupErr != nil {
		s.logMessage("service.dashboard_aggregation", "[DashboardAggregation] usage_billing_dedup 保留清理失败: %v", dedupErr)
	}
	if aggErr == nil && usageErr == nil && dedupErr == nil {
		s.lastRetentionCleanup.Store(now)
	}
}

// TriggerNow 立即尝试执行一轮聚合，供运行时开关开启后使用。
func (s *DashboardAggregationService) TriggerNow() {
	if s == nil {
		return
	}
	s.lastScheduledAt.Store(0)
	s.runBackground(s.runScheduledAggregation)
}

// RuntimeStatus 返回设置页面所需的多维聚合状态。
func (s *DashboardAggregationService) RuntimeStatus(ctx context.Context) PreAggregationRuntimeStatus {
	status := PreAggregationRuntimeStatus{Phase: "unavailable"}
	if s == nil || s.analyticsRepo == nil {
		return status
	}
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		status.Phase = "error"
		status.LastError = err.Error()
		return status
	}
	status.Phase = state.Phase
	if !s.usageEnabled(ctx) {
		status.Phase = "disabled"
	}
	if !state.LiveWatermark.IsZero() && state.LiveWatermark.After(time.Unix(0, 0)) {
		watermark := state.LiveWatermark.UTC()
		status.LiveWatermark = &watermark
		status.LagSeconds = maxInt64(0, int64(time.Since(watermark).Seconds()))
	}
	status.CoverageStart = state.CoverageStart
	status.SourceOldestAt = state.SourceOldestAt
	status.LastRunAt = state.LastRunAt
	status.LastSuccessAt = state.LastSuccessAt
	status.LastErrorAt = state.LastErrorAt
	status.LastError = state.LastError
	status.LastDurationMS = state.LastDurationMS
	return status
}

func (s *DashboardAggregationService) usageEnabled(ctx context.Context) bool {
	if s == nil || !s.cfg.Enabled {
		return false
	}
	if s.settings == nil {
		return true
	}
	return s.settings.UsageEnabled(ctx)
}

func (s *DashboardAggregationService) scheduledRunDue(now time.Time) bool {
	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	if s.settings != nil {
		interval = time.Duration(s.settings.Resolve(s.operationContext()).Usage.IntervalSeconds) * time.Second
	}
	if interval <= 0 {
		interval = time.Minute
	}
	last := s.lastScheduledAt.Load()
	if last > 0 && now.Sub(time.Unix(0, last)) < interval {
		return false
	}
	return s.lastScheduledAt.CompareAndSwap(last, now.UnixNano())
}

func (s *DashboardAggregationService) markAnalyticsRunStarted(ctx context.Context, now time.Time) {
	if s.analyticsRepo == nil {
		return
	}
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		return
	}
	state.Phase = "live"
	state.LastRunAt = timePointer(now)
	_ = s.updateAnalyticsState(ctx, state, AnalyticsRunStarted)
}

func (s *DashboardAggregationService) markAnalyticsLiveSuccess(ctx context.Context, now, start, jobStart time.Time) {
	if s.analyticsRepo == nil {
		return
	}
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		return
	}
	state.LiveWatermark = now
	if state.CoverageStart == nil {
		coverage := start.UTC().Truncate(time.Hour)
		state.CoverageStart = &coverage
		state.BackfillCursor = &coverage
	}
	state.Phase = "idle"
	state.LastSuccessAt = timePointer(now)
	state.LastError = ""
	state.LastErrorAt = nil
	state.LastDurationMS = time.Since(jobStart).Milliseconds()
	_ = s.updateAnalyticsState(ctx, state, AnalyticsLiveSuccess)
}

func (s *DashboardAggregationService) markAnalyticsRunFailed(ctx context.Context, now, jobStart time.Time, runErr error) {
	if s.analyticsRepo == nil {
		return
	}
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		return
	}
	state.Phase = "error"
	state.LastError = runErr.Error()
	state.LastErrorAt = timePointer(now)
	state.LastDurationMS = time.Since(jobStart).Milliseconds()
	_ = s.updateAnalyticsState(ctx, state, AnalyticsRunFailed)
}

func (s *DashboardAggregationService) runHistoricalBackfill(ctx context.Context, now time.Time) {
	if s.analyticsRepo == nil {
		return
	}
	last := s.lastBackfillAt.Load()
	if last > 0 && now.Sub(time.Unix(0, last)) < time.Minute {
		return
	}
	if !s.lastBackfillAt.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	state, err := s.readAnalyticsState(ctx)
	if err != nil {
		return
	}
	if state.ManualBackfillStart != nil && state.ManualBackfillCursor != nil {
		s.runManualHistoricalBackfill(ctx, state)
		return
	}
	if state.SourceOldestAt == nil {
		oldest, oldestErr := s.analyticsRepo.GetOldestUsageLogTime(ctx)
		if oldestErr != nil {
			return
		}
		state.SourceOldestAt = oldest
	}
	if state.SourceOldestAt == nil {
		return
	}
	oldestHour := state.SourceOldestAt.UTC().Truncate(time.Hour)
	cursor := now.UTC().Truncate(time.Hour)
	if state.BackfillCursor != nil {
		cursor = state.BackfillCursor.UTC().Truncate(time.Hour)
	}
	if !cursor.After(oldestHour) {
		state.Phase = "idle"
		state.CoverageStart = timePointer(oldestHour)
		state.BackfillCursor = timePointer(oldestHour)
		_ = s.updateAnalyticsState(ctx, state, AnalyticsAutomaticProgress)
		return
	}

	started := time.Now()
	budget := s.resolveBackfillBudget()
	var previousIterationDuration time.Duration
	state.Phase = "backfill"
	_ = s.updateAnalyticsState(ctx, state, AnalyticsAutomaticProgress)
	for processed := 0; processed < dashboardAggregationBackfillMaxHours; processed++ {
		chunkStart := cursor.Add(-time.Hour)
		if chunkStart.Before(oldestHour) {
			chunkStart = oldestHour
		}
		remaining := budget - time.Since(started)
		if !shouldStartBackfillChunk(processed, remaining, previousIterationDuration) {
			s.pauseHistoricalBackfill(ctx, state, started)
			return
		}
		iterationStarted := time.Now()
		chunkCtx, cancel := context.WithTimeout(ctx, remaining)
		chunkErr := s.aggregateAutomaticBackfillHour(chunkCtx, chunkStart, cursor, oldestHour)
		chunkContextErr := chunkCtx.Err()
		cancel()
		if chunkErr != nil {
			if errors.Is(chunkContextErr, context.DeadlineExceeded) {
				if processed > 0 {
					s.pauseHistoricalBackfill(ctx, state, started)
					return
				}
				timeoutErr := fmt.Errorf("单个小时回填超过 %s 工作预算", budget)
				s.markAnalyticsRunFailed(ctx, time.Now().UTC(), started, timeoutErr)
				return
			}
			s.markAnalyticsRunFailed(ctx, time.Now().UTC(), started, chunkErr)
			return
		}
		cursor = chunkStart
		state.CoverageStart = timePointer(cursor)
		state.BackfillCursor = timePointer(cursor)
		state.LastSuccessAt = timePointer(time.Now().UTC())
		state.LastError = ""
		state.LastErrorAt = nil
		state.LastDurationMS = time.Since(started).Milliseconds()
		if !cursor.After(oldestHour) {
			state.Phase = "idle"
		} else {
			state.Phase = "backfill"
		}
		if err := s.updateAnalyticsState(ctx, state, AnalyticsAutomaticProgress); err != nil {
			return
		}
		previousIterationDuration = time.Since(iterationStarted)
		if !cursor.After(oldestHour) {
			return
		}
	}
}

// runManualHistoricalBackfill 仅重算管理员指定的最近时间范围，并保存独立游标以支持重启续跑。
func (s *DashboardAggregationService) runManualHistoricalBackfill(ctx context.Context, state *UsageAnalyticsAggregationState) {
	if state == nil || state.ManualBackfillStart == nil || state.ManualBackfillCursor == nil {
		return
	}
	target := state.ManualBackfillStart.UTC().Truncate(time.Hour)
	cursor := state.ManualBackfillCursor.UTC().Truncate(time.Hour)
	if !cursor.After(target) {
		state.ManualBackfillStart = nil
		state.ManualBackfillCursor = nil
		state.Phase = "idle"
		_ = s.updateAnalyticsState(ctx, state, AnalyticsManualProgress)
		return
	}

	started := time.Now()
	budget := s.resolveBackfillBudget()
	var previousIterationDuration time.Duration
	state.Phase = "backfill"
	_ = s.updateAnalyticsState(ctx, state, AnalyticsManualProgress)
	for processed := 0; processed < dashboardAggregationBackfillMaxHours; processed++ {
		chunkEnd := cursor
		chunkStart := cursor.Add(-time.Hour)
		if chunkStart.Before(target) {
			chunkStart = target
		}
		remaining := budget - time.Since(started)
		if !shouldStartBackfillChunk(processed, remaining, previousIterationDuration) {
			s.pauseHistoricalBackfill(ctx, state, started)
			return
		}
		iterationStarted := time.Now()
		chunkCtx, cancel := context.WithTimeout(ctx, remaining)
		chunkErr := s.aggregateRange(chunkCtx, chunkStart, chunkEnd)
		chunkContextErr := chunkCtx.Err()
		cancel()
		if chunkErr != nil {
			if errors.Is(chunkContextErr, context.DeadlineExceeded) {
				if processed > 0 {
					s.pauseHistoricalBackfill(ctx, state, started)
					return
				}
				timeoutErr := fmt.Errorf("单个小时回填超过 %s 工作预算", budget)
				s.markAnalyticsRunFailed(ctx, time.Now().UTC(), started, timeoutErr)
				return
			}
			s.markAnalyticsRunFailed(ctx, time.Now().UTC(), started, chunkErr)
			return
		}

		cursor = chunkStart
		extendUsageAnalyticsCoverage(state, chunkStart, chunkEnd)
		state.ManualBackfillCursor = timePointer(cursor)
		state.LastSuccessAt = timePointer(time.Now().UTC())
		state.LastError = ""
		state.LastErrorAt = nil
		state.LastDurationMS = time.Since(started).Milliseconds()
		if !cursor.After(target) {
			state.ManualBackfillStart = nil
			state.ManualBackfillCursor = nil
			state.Phase = "idle"
		} else {
			state.Phase = "backfill"
		}
		if err := s.updateAnalyticsState(ctx, state, AnalyticsManualProgress); err != nil {
			return
		}
		previousIterationDuration = time.Since(iterationStarted)
		if !cursor.After(target) {
			return
		}
	}
}

// resolveBackfillBudget 返回单轮数据库工作预算，测试可注入更短时长。
func (s *DashboardAggregationService) resolveBackfillBudget() time.Duration {
	if s != nil && s.backfillBudget > 0 {
		return s.backfillBudget
	}
	return dashboardAggregationBackfillBudget
}

// shouldStartBackfillChunk 用上一轮完整耗时判断剩余预算是否足以启动下一小时。
func shouldStartBackfillChunk(processed int, remaining, previousIterationDuration time.Duration) bool {
	if remaining <= 0 {
		return false
	}
	if processed == 0 || previousIterationDuration <= 0 {
		return true
	}
	return remaining > previousIterationDuration
}

// pauseHistoricalBackfill 保存已完成游标，预算正常耗尽不记录为任务错误。
func (s *DashboardAggregationService) pauseHistoricalBackfill(ctx context.Context, state *UsageAnalyticsAggregationState, started time.Time) {
	if s == nil || s.analyticsRepo == nil || state == nil {
		return
	}
	state.Phase = "backfill"
	state.LastError = ""
	state.LastErrorAt = nil
	state.LastDurationMS = time.Since(started).Milliseconds()
	_ = s.updateAnalyticsState(ctx, state, progressKind(state))
}

// extendUsageAnalyticsCoverage 只在手动块与现有连续覆盖相接时推进公共覆盖游标。
func extendUsageAnalyticsCoverage(state *UsageAnalyticsAggregationState, chunkStart, chunkEnd time.Time) {
	if state == nil || state.CoverageStart == nil {
		return
	}
	coverage := state.CoverageStart.UTC().Truncate(time.Hour)
	if chunkEnd.Before(coverage) || chunkStart.After(coverage) {
		return
	}
	state.CoverageStart = timePointer(chunkStart)
	if state.BackfillCursor == nil || state.BackfillCursor.After(chunkStart) {
		state.BackfillCursor = timePointer(chunkStart)
	}
}

func timePointer(value time.Time) *time.Time {
	result := value.UTC()
	return &result
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func truncateToDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// runBackground 将启动和设置更新触发的工作纳入同一停止屏障。
func (s *DashboardAggregationService) runBackground(fn func()) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if !s.runtimeStarted || s.runtimeStopped {
		return
	}
	s.runtimeWG.Add(1)
	go func() { defer s.runtimeWG.Done(); fn() }()
}

// Stop 先停止产生新聚合工作，再等待时间轮回调与已唤醒的任务。
func (s *DashboardAggregationService) Stop() { _ = s.StopContext(context.Background()) }

// StopContext 停止新工作并取消在途聚合；超时不代表资源已排空。
func (s *DashboardAggregationService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		s.runtimeMu.Lock()
		s.runtimeStopped = true
		if s.runCancel != nil {
			s.runCancel()
		}
		if s.stopDone == nil {
			s.stopDone = make(chan struct{})
		}
		s.runtimeMu.Unlock()
		go func() {
			if s.timingWheel != nil {
				s.timingWheel.CancelAndWait("dashboard:aggregation")
			}
			s.runtimeWG.Wait()
			close(s.stopDone)
		}()
	})
	select {
	case <-s.stopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// operationContext 兼容未启动的同步验证；生产构造始终持有运行 context。
func (s *DashboardAggregationService) operationContext() context.Context {
	if s != nil && s.runCtx != nil {
		return s.runCtx
	}
	return context.Background()
}

func (s *DashboardAggregationService) acquireSingleton(ctx context.Context) (func(), bool) {
	if s.locker == nil {
		return func() {}, true
	}
	return s.locker(ctx, dashboardAggregationLeaderLockKey, s.instanceID, dashboardAggregationLeaderLockTTL)
}

func progressKind(state *UsageAnalyticsAggregationState) AnalyticsChangeKind {
	if state.observedManualStart != nil {
		return AnalyticsManualProgress
	}
	return AnalyticsAutomaticProgress
}

func (s *DashboardAggregationService) logMessage(component, format string, args ...any) {
	if s != nil && s.reporter != nil {
		s.reporter(component, format, args...)
		return
	}
	logUsage(component, format, args...)
}
