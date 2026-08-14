package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type dashboardAggregationRepoTestStub struct {
	aggregateCalls       int
	aggregateRanges      []aggregationRangeCall
	recomputeCalls       int
	cleanupUsageCalls    int
	cleanupDedupCalls    int
	ensurePartitionCalls int
	lastStart            time.Time
	lastEnd              time.Time
	watermark            time.Time
	aggregateErr         error
	cleanupAggregatesErr error
	cleanupUsageErr      error
	cleanupDedupErr      error
	ensurePartitionErr   error
	aggregateStarted     chan struct{}
}

func (s *dashboardAggregationRepoTestStub) AggregateRange(ctx context.Context, start, end time.Time) error {
	s.aggregateCalls++
	s.aggregateRanges = append(s.aggregateRanges, aggregationRangeCall{start: start, end: end})
	s.lastStart = start
	s.lastEnd = end
	if s.aggregateStarted != nil {
		select {
		case s.aggregateStarted <- struct{}{}:
		default:
		}
	}
	return s.aggregateErr
}

type aggregationRangeCall struct {
	start time.Time
	end   time.Time
}

type usageAnalyticsAggregationRepoTestStub struct {
	state          UsageAnalyticsAggregationState
	oldest         *time.Time
	aggregateCalls []aggregationRangeCall
	hourlyCalls    []aggregationRangeCall
	dailyCalls     []aggregationRangeCall
	recomputeCalls []aggregationRangeCall
	hourlyCallback func(context.Context, time.Time, time.Time) error
	dailyCallback  func(context.Context, time.Time, time.Time) error
}

func (s *usageAnalyticsAggregationRepoTestStub) AggregateUsageAnalyticsRange(_ context.Context, start, end time.Time) error {
	s.aggregateCalls = append(s.aggregateCalls, aggregationRangeCall{start: start, end: end})
	return nil
}

func (s *usageAnalyticsAggregationRepoTestStub) AggregateUsageAnalyticsHourlyRange(ctx context.Context, start, end time.Time) error {
	s.hourlyCalls = append(s.hourlyCalls, aggregationRangeCall{start: start, end: end})
	if s.hourlyCallback != nil {
		return s.hourlyCallback(ctx, start, end)
	}
	return nil
}

func (s *usageAnalyticsAggregationRepoTestStub) RebuildUsageAnalyticsDailyRange(ctx context.Context, start, end time.Time) error {
	s.dailyCalls = append(s.dailyCalls, aggregationRangeCall{start: start, end: end})
	if s.dailyCallback != nil {
		return s.dailyCallback(ctx, start, end)
	}
	return nil
}

func (s *usageAnalyticsAggregationRepoTestStub) RecomputeUsageAnalyticsRange(_ context.Context, start, end time.Time) error {
	s.recomputeCalls = append(s.recomputeCalls, aggregationRangeCall{start: start, end: end})
	return nil
}

func (s *usageAnalyticsAggregationRepoTestStub) GetUsageAnalyticsAggregationState(context.Context) (*UsageAnalyticsAggregationState, error) {
	state := s.state
	return &state, nil
}

func (s *usageAnalyticsAggregationRepoTestStub) SaveUsageAnalyticsAggregationState(_ context.Context, state *UsageAnalyticsAggregationState) error {
	if state != nil {
		s.state = *state
	}
	return nil
}

func (s *usageAnalyticsAggregationRepoTestStub) GetOldestUsageLogTime(context.Context) (*time.Time, error) {
	return s.oldest, nil
}

func (s *usageAnalyticsAggregationRepoTestStub) CleanupUsageAnalytics(context.Context, time.Time, time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoTestStub) RecomputeRange(ctx context.Context, start, end time.Time) error {
	s.recomputeCalls++
	return s.AggregateRange(ctx, start, end)
}

func (s *dashboardAggregationRepoTestStub) GetAggregationWatermark(ctx context.Context) (time.Time, error) {
	return s.watermark, nil
}

func (s *dashboardAggregationRepoTestStub) UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoTestStub) CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error {
	return s.cleanupAggregatesErr
}

func (s *dashboardAggregationRepoTestStub) CleanupUsageLogs(ctx context.Context, cutoff time.Time) error {
	s.cleanupUsageCalls++
	return s.cleanupUsageErr
}

func (s *dashboardAggregationRepoTestStub) CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error {
	s.cleanupDedupCalls++
	return s.cleanupDedupErr
}

func (s *dashboardAggregationRepoTestStub) EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error {
	s.ensurePartitionCalls++
	return s.ensurePartitionErr
}

func TestDashboardAggregationService_RunScheduledAggregation_EpochUsesLiveLookback(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{watermark: time.Unix(0, 0).UTC()}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Enabled:         true,
			IntervalSeconds: 60,
			LookbackSeconds: 120,
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays: 1,
				HourlyDays:    1,
				DailyDays:     1,
			},
		},
	}

	svc.runScheduledAggregation()

	require.Equal(t, 1, repo.aggregateCalls)
	require.False(t, repo.lastEnd.IsZero())
	require.WithinDuration(t, repo.lastEnd.Add(-120*time.Second), repo.lastStart, time.Second)
}

// TestDashboardAggregationServiceRuntimeDisabledStopsWrites 验证运行时关闭后定时轮次不会写聚合表。
func TestDashboardAggregationServiceRuntimeDisabledStopsWrites(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{watermark: time.Unix(0, 0).UTC()}
	settingRepo := newRuntimeSettingRepoStub()
	settingRepo.values[SettingKeyPreAggregationSettings] = `{"usage":{"enabled":false,"interval_seconds":60},"ops":{"enabled":true}}`
	cfg := &config.Config{DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60}}
	settings := NewPreAggregationSettingsService(settingRepo, cfg)
	svc := NewDashboardAggregationService(repo, nil, cfg)
	svc.SetPreAggregationSettings(settings)

	svc.runScheduledAggregation()

	require.Zero(t, repo.aggregateCalls)
}

// TestDashboardAggregationServiceRuntimeEnableTriggersImmediately 验证开启运行时开关会立即唤醒任务。
func TestDashboardAggregationServiceRuntimeEnableTriggersImmediately(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{
		watermark:        time.Unix(0, 0).UTC(),
		aggregateStarted: make(chan struct{}, 1),
	}
	settingRepo := newRuntimeSettingRepoStub()
	settingRepo.values[SettingKeyPreAggregationSettings] = `{"usage":{"enabled":false,"interval_seconds":60},"ops":{"enabled":true}}`
	cfg := &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true, IntervalSeconds: 60, LookbackSeconds: 120,
			Retention: config.DashboardAggregationRetentionConfig{HourlyDays: 1, DailyDays: 1},
		},
	}
	settings := NewPreAggregationSettingsService(settingRepo, cfg)
	svc := NewDashboardAggregationService(repo, nil, cfg)
	svc.SetPreAggregationSettings(settings)

	_, err := settings.Update(context.Background(), PreAggregationSettings{
		Usage: PreAggregationUsageSettings{Enabled: true, IntervalSeconds: 60},
		Ops:   PreAggregationOpsSettings{Enabled: true},
	})
	require.NoError(t, err)
	select {
	case <-repo.aggregateStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("开启预聚合后未立即触发任务")
	}
}

// TestDashboardAggregationServiceRunningJobPreservesWakeup 验证在途任务不会消费新的立即运行信号。
func TestDashboardAggregationServiceRunningJobPreservesWakeup(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Enabled:         true,
			IntervalSeconds: 3600,
		},
	}
	atomic.StoreInt32(&svc.running, 1)
	svc.lastScheduledAt.Store(0)

	svc.runScheduledAggregation()

	require.Zero(t, svc.lastScheduledAt.Load())
	require.Zero(t, repo.aggregateCalls)
}

// TestDashboardAggregationServiceRecomputeRunsWhileRuntimeDisabled 验证删除后的内部重算不会留下陈旧聚合。
func TestDashboardAggregationServiceRecomputeRunsWhileRuntimeDisabled(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{aggregateStarted: make(chan struct{}, 1)}
	settingRepo := newRuntimeSettingRepoStub()
	settingRepo.values[SettingKeyPreAggregationSettings] = `{"usage":{"enabled":false,"interval_seconds":60},"ops":{"enabled":true}}`
	cfg := &config.Config{DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60}}
	settings := NewPreAggregationSettingsService(settingRepo, cfg)
	svc := NewDashboardAggregationService(repo, nil, cfg)
	svc.SetPreAggregationSettings(settings)

	end := time.Now().UTC()
	require.NoError(t, svc.TriggerRecomputeRange(end.Add(-time.Hour), end))
	select {
	case <-repo.aggregateStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("运行时关闭后内部一致性重算未执行")
	}
}

func TestDashboardAggregationService_CleanupRetentionFailure_DoesNotRecord(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{cleanupAggregatesErr: errors.New("清理失败")}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays: 1,
				HourlyDays:    1,
				DailyDays:     1,
			},
		},
	}

	svc.maybeCleanupRetention(context.Background(), time.Now().UTC())

	require.Nil(t, svc.lastRetentionCleanup.Load())
	require.Equal(t, 1, repo.cleanupUsageCalls)
	require.Equal(t, 1, repo.cleanupDedupCalls)
}

func TestDashboardAggregationService_CleanupDedupFailure_DoesNotRecord(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{cleanupDedupErr: errors.New("dedup cleanup failed")}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays: 1,
				HourlyDays:    1,
				DailyDays:     1,
			},
		},
	}

	svc.maybeCleanupRetention(context.Background(), time.Now().UTC())

	require.Nil(t, svc.lastRetentionCleanup.Load())
	require.Equal(t, 1, repo.cleanupDedupCalls)
}

func TestDashboardAggregationService_PartitionFailure_DoesNotAggregate(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{ensurePartitionErr: errors.New("partition failed")}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Enabled:         true,
			IntervalSeconds: 60,
			LookbackSeconds: 120,
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays:         1,
				UsageBillingDedupDays: 2,
				HourlyDays:            1,
				DailyDays:             1,
			},
		},
	}

	svc.runScheduledAggregation()

	require.Equal(t, 1, repo.ensurePartitionCalls)
	require.Equal(t, 1, repo.aggregateCalls)
}

func TestDashboardAggregationService_TriggerBackfill_TooLarge(t *testing.T) {
	repo := &dashboardAggregationRepoTestStub{}
	svc := &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			BackfillEnabled: true,
			BackfillMaxDays: 1,
		},
	}

	start := time.Now().AddDate(0, 0, -3)
	end := time.Now()
	err := svc.TriggerBackfill(start, end)
	require.ErrorIs(t, err, ErrDashboardBackfillTooLarge)
	require.Equal(t, 0, repo.aggregateCalls)
}

// TestDashboardAggregationServiceTriggerBackfillPersistsManualRange 验证天数请求不会覆盖自动历史游标。
func TestDashboardAggregationServiceTriggerBackfillPersistsManualRange(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 35, 0, 0, time.UTC)
	automaticCursor := now.AddDate(0, 0, -10).Truncate(time.Hour)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{BackfillCursor: timePointer(automaticCursor)},
	}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
		cfg: config.DashboardAggregationConfig{
			BackfillEnabled: true,
			BackfillMaxDays: 31,
		},
	}
	svc.lastBackfillAt.Store(now.Add(-30 * time.Second).UnixNano())

	require.NoError(t, svc.TriggerBackfill(now.AddDate(0, 0, -7), now))

	require.Equal(t, now.AddDate(0, 0, -7).Truncate(time.Hour), *analyticsRepo.state.ManualBackfillStart)
	require.Equal(t, now.Truncate(time.Hour), *analyticsRepo.state.ManualBackfillCursor)
	require.Equal(t, automaticCursor, *analyticsRepo.state.BackfillCursor)
	require.Equal(t, "backfill", analyticsRepo.state.Phase)
	require.Zero(t, svc.lastBackfillAt.Load())
}

// TestDashboardAggregationServiceManualBackfillStopsAtRequestedStart 验证手动任务到达目标后不会继续处理更早历史。
func TestDashboardAggregationServiceManualBackfillStopsAtRequestedStart(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	target := now.Add(-2 * time.Hour)
	oldest := now.Add(-10 * time.Hour)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:        timePointer(now),
			BackfillCursor:       timePointer(now),
			SourceOldestAt:       timePointer(oldest),
			ManualBackfillStart:  timePointer(target),
			ManualBackfillCursor: timePointer(now),
		},
		oldest: timePointer(oldest),
	}
	dashboardRepo := &dashboardAggregationRepoTestStub{}
	svc := &DashboardAggregationService{repo: dashboardRepo, analyticsRepo: analyticsRepo}

	svc.runHistoricalBackfill(context.Background(), now)

	require.Len(t, analyticsRepo.aggregateCalls, 2)
	require.Equal(t, target, analyticsRepo.aggregateCalls[1].start)
	require.Equal(t, target.Add(time.Hour), analyticsRepo.aggregateCalls[1].end)
	require.Nil(t, analyticsRepo.state.ManualBackfillStart)
	require.Nil(t, analyticsRepo.state.ManualBackfillCursor)
	require.Equal(t, target, *analyticsRepo.state.CoverageStart)
	require.Equal(t, target, *analyticsRepo.state.BackfillCursor)
	require.Equal(t, "idle", analyticsRepo.state.Phase)
	require.Equal(t, 2, dashboardRepo.aggregateCalls)
}

// TestDashboardAggregationServiceManualBackfillResumesFromSavedCursor 验证超过单轮上限的范围会续跑且不越过目标。
func TestDashboardAggregationServiceManualBackfillResumesFromSavedCursor(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	target := now.Add(-26 * time.Hour)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:        timePointer(now),
			BackfillCursor:       timePointer(now),
			ManualBackfillStart:  timePointer(target),
			ManualBackfillCursor: timePointer(now),
		},
	}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}

	svc.runHistoricalBackfill(context.Background(), now)

	require.Len(t, analyticsRepo.aggregateCalls, dashboardAggregationBackfillMaxHours)
	require.Equal(t, now.Add(-24*time.Hour), *analyticsRepo.state.ManualBackfillCursor)
	require.Equal(t, target, *analyticsRepo.state.ManualBackfillStart)
	require.Equal(t, "backfill", analyticsRepo.state.Phase)

	svc.lastBackfillAt.Store(0)
	svc.runHistoricalBackfill(context.Background(), now.Add(time.Minute))

	require.Len(t, analyticsRepo.aggregateCalls, 26)
	require.Equal(t, target, analyticsRepo.aggregateCalls[25].start)
	require.Equal(t, target.Add(time.Hour), analyticsRepo.aggregateCalls[25].end)
	require.Nil(t, analyticsRepo.state.ManualBackfillStart)
	require.Nil(t, analyticsRepo.state.ManualBackfillCursor)
}

// TestDashboardAggregationServiceLiveRebuildsTouchedClosedUTCDays 验证迟到记录的回看范围仍会刷新已闭合日表。
func TestDashboardAggregationServiceLiveRebuildsTouchedClosedUTCDays(t *testing.T) {
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}
	start := time.Date(2026, 8, 3, 23, 58, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 0, 2, 0, 0, time.UTC)

	require.NoError(t, svc.aggregateLiveRange(context.Background(), start, end))
	require.Equal(t, []aggregationRangeCall{{start: start, end: end}}, analyticsRepo.hourlyCalls)
	require.Equal(t, []aggregationRangeCall{{
		start: truncateToDayUTC(start),
		end:   truncateToDayUTC(end),
	}}, analyticsRepo.dailyCalls)

	analyticsRepo.dailyCalls = nil
	// 首轮完成后到达的前一日迟到记录，仍需通过跨日回看同步到日表。
	require.NoError(t, svc.aggregateLiveRange(context.Background(), start.Add(time.Minute), end.Add(time.Minute)))
	require.Equal(t, []aggregationRangeCall{{
		start: truncateToDayUTC(start),
		end:   truncateToDayUTC(end),
	}}, analyticsRepo.dailyCalls)

	analyticsRepo.dailyCalls = nil
	// 回看范围完全进入当前日期后，不再改写前一日日表。
	require.NoError(t, svc.aggregateLiveRange(context.Background(), start.Add(2*time.Minute), end.Add(2*time.Minute)))
	require.Empty(t, analyticsRepo.dailyCalls)
}

// TestDashboardAggregationServiceAutomaticBackfillRebuildsCompletedDay 验证自动回填完成一个 UTC 日后只重建一次日表。
func TestDashboardAggregationServiceAutomaticBackfillRebuildsCompletedDay(t *testing.T) {
	cursor := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	oldest := cursor.Add(-24 * time.Hour)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:  timePointer(cursor),
			BackfillCursor: timePointer(cursor),
			SourceOldestAt: timePointer(oldest),
		},
	}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}

	svc.runHistoricalBackfill(context.Background(), cursor)

	require.Len(t, analyticsRepo.hourlyCalls, 24)
	require.Equal(t, []aggregationRangeCall{{start: oldest, end: cursor}}, analyticsRepo.dailyCalls)
	require.Equal(t, oldest, *analyticsRepo.state.BackfillCursor)
	require.Equal(t, "idle", analyticsRepo.state.Phase)
}

// TestDashboardAggregationServiceAutomaticBackfillRebuildsOldestPartialDay 验证到达最早的部分日期时也生成对应日表。
func TestDashboardAggregationServiceAutomaticBackfillRebuildsOldestPartialDay(t *testing.T) {
	cursor := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:  timePointer(cursor),
			BackfillCursor: timePointer(cursor),
			SourceOldestAt: timePointer(oldest),
		},
	}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}

	svc.runHistoricalBackfill(context.Background(), cursor)

	require.Len(t, analyticsRepo.hourlyCalls, 3)
	dayStart := truncateToDayUTC(oldest)
	require.Equal(t, []aggregationRangeCall{{start: dayStart, end: dayStart.AddDate(0, 0, 1)}}, analyticsRepo.dailyCalls)
	require.Equal(t, oldest, *analyticsRepo.state.BackfillCursor)
}

// TestDashboardAggregationServiceDailyFailureDoesNotAdvanceCursor 验证日表失败后保留旧游标并在下轮幂等重试。
func TestDashboardAggregationServiceDailyFailureDoesNotAdvanceCursor(t *testing.T) {
	oldest := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	cursor := oldest.Add(time.Hour)
	dailyErr := errors.New("日表写入失败")
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:  timePointer(cursor),
			BackfillCursor: timePointer(cursor),
			SourceOldestAt: timePointer(oldest),
		},
		dailyCallback: func(context.Context, time.Time, time.Time) error {
			return dailyErr
		},
	}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}

	svc.runHistoricalBackfill(context.Background(), cursor)
	require.Equal(t, cursor, *analyticsRepo.state.BackfillCursor)
	require.Equal(t, "error", analyticsRepo.state.Phase)
	require.Equal(t, dailyErr.Error(), analyticsRepo.state.LastError)

	analyticsRepo.dailyCallback = nil
	svc.lastBackfillAt.Store(0)
	svc.runHistoricalBackfill(context.Background(), cursor.Add(time.Minute))
	require.Len(t, analyticsRepo.hourlyCalls, 2)
	require.Len(t, analyticsRepo.dailyCalls, 2)
	require.Equal(t, oldest, *analyticsRepo.state.BackfillCursor)
	require.Equal(t, "idle", analyticsRepo.state.Phase)
}

// TestDashboardAggregationServiceBudgetExhaustionKeepsBackfill 验证完成块后的数据库主动取消不会误报异常。
func TestDashboardAggregationServiceBudgetExhaustionKeepsBackfill(t *testing.T) {
	cursor := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	oldest := cursor.Add(-4 * time.Hour)
	call := 0
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:  timePointer(cursor),
			BackfillCursor: timePointer(cursor),
			SourceOldestAt: timePointer(oldest),
			LastError:      "旧错误",
			LastErrorAt:    timePointer(cursor.Add(-time.Minute)),
		},
		hourlyCallback: func(ctx context.Context, _ time.Time, _ time.Time) error {
			call++
			if call == 1 {
				time.Sleep(5 * time.Millisecond)
				return nil
			}
			<-ctx.Done()
			return errors.New("pq: canceling statement due to user request")
		},
	}
	svc := &DashboardAggregationService{
		repo:           &dashboardAggregationRepoTestStub{},
		analyticsRepo:  analyticsRepo,
		backfillBudget: 30 * time.Millisecond,
	}

	svc.runHistoricalBackfill(context.Background(), cursor)

	require.Equal(t, "backfill", analyticsRepo.state.Phase)
	require.Empty(t, analyticsRepo.state.LastError)
	require.Nil(t, analyticsRepo.state.LastErrorAt)
	require.Equal(t, cursor.Add(-time.Hour), *analyticsRepo.state.BackfillCursor)
}

// TestDashboardAggregationServiceFirstChunkTimeoutIsError 验证首个小时耗尽完整预算时才标记真实异常。
func TestDashboardAggregationServiceFirstChunkTimeoutIsError(t *testing.T) {
	cursor := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	oldest := cursor.Add(-2 * time.Hour)
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{
		state: UsageAnalyticsAggregationState{
			CoverageStart:  timePointer(cursor),
			BackfillCursor: timePointer(cursor),
			SourceOldestAt: timePointer(oldest),
		},
		hourlyCallback: func(ctx context.Context, _ time.Time, _ time.Time) error {
			<-ctx.Done()
			return errors.New("pq: canceling statement due to user request")
		},
	}
	svc := &DashboardAggregationService{
		repo:           &dashboardAggregationRepoTestStub{},
		analyticsRepo:  analyticsRepo,
		backfillBudget: 20 * time.Millisecond,
	}

	svc.runHistoricalBackfill(context.Background(), cursor)

	require.Equal(t, "error", analyticsRepo.state.Phase)
	require.Contains(t, analyticsRepo.state.LastError, "单个小时回填超过 20ms 工作预算")
	require.NotNil(t, analyticsRepo.state.LastErrorAt)
	require.Equal(t, cursor, *analyticsRepo.state.BackfillCursor)
}

// TestShouldStartBackfillChunkUsesPreviousDuration 验证尾部预算不足时不会启动大概率超时的新块。
func TestShouldStartBackfillChunkUsesPreviousDuration(t *testing.T) {
	require.True(t, shouldStartBackfillChunk(0, time.Millisecond, time.Second))
	require.True(t, shouldStartBackfillChunk(1, 11*time.Millisecond, 10*time.Millisecond))
	require.False(t, shouldStartBackfillChunk(1, 10*time.Millisecond, 10*time.Millisecond))
	require.False(t, shouldStartBackfillChunk(1, 0, time.Millisecond))
}

// TestDashboardAggregationServiceRecomputeUsesCompleteAnalyticsBuckets 验证删除修复不会复用实时部分桶接口。
func TestDashboardAggregationServiceRecomputeUsesCompleteAnalyticsBuckets(t *testing.T) {
	analyticsRepo := &usageAnalyticsAggregationRepoTestStub{}
	svc := &DashboardAggregationService{
		repo:          &dashboardAggregationRepoTestStub{},
		analyticsRepo: analyticsRepo,
	}
	start := time.Date(2026, 8, 4, 9, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)

	require.NoError(t, svc.recomputeRange(context.Background(), start, end))

	require.Empty(t, analyticsRepo.aggregateCalls)
	require.Equal(t, []aggregationRangeCall{{start: start, end: end}}, analyticsRepo.recomputeCalls)
}
