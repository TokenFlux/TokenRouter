package account

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scheduledScheduleStub struct {
	starts atomic.Int32
	stops  atomic.Int32
	err    error
	start  func(func())
}

func (s *scheduledScheduleStub) Start(_ context.Context, run func()) error {
	s.starts.Add(1)
	if s.start != nil {
		s.start(run)
	}
	return s.err
}
func (s *scheduledScheduleStub) Stop(context.Context) error { s.stops.Add(1); return nil }

type scheduledPlansStub struct {
	ScheduledTestPlanRepository
	reads atomic.Int32
	plans []*ScheduledTestPlan
}

func (s *scheduledPlansStub) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	s.reads.Add(1)
	return s.plans, nil
}
func (*scheduledPlansStub) UpdateAfterRun(ctx context.Context, _ int64, _, _ time.Time) error {
	return ctx.Err()
}

type scheduledExecutorStub struct {
	entered chan struct{}
	release chan struct{}
}

func (s scheduledExecutorStub) RunTestBackground(context.Context, int64, string) (*ScheduledTestResult, error) {
	close(s.entered)
	<-s.release
	return &ScheduledTestResult{Status: "error"}, nil
}

type scheduledResultsStub struct{ ScheduledTestResultRepository }

func (scheduledResultsStub) Create(ctx context.Context, _ *ScheduledTestResult) (*ScheduledTestResult, error) {
	return nil, ctx.Err()
}

func scheduledRunnerOptions(schedule ScheduledTestSchedule, offset time.Duration) ScheduledRunnerOptions {
	return ScheduledRunnerOptions{Schedule: schedule, Offset: offset, Now: time.Now,
		NextRun: func(_ string, from time.Time) (time.Time, error) { return from.Add(time.Minute), nil }}
}

func waitScheduledRound(t *testing.T, runner *ScheduledTestRunnerService) {
	t.Helper()
	require.Eventually(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.active > 0
	}, time.Second, time.Millisecond)
}

// 构造无启动，重复 Start 不注册新任务，Stop 之后也不能重新开启 cron。
func TestScheduledRunnerStopsPermanently(t *testing.T) {
	for _, beforeStart := range []bool{false, true} {
		schedule := &scheduledScheduleStub{}
		runner := NewScheduledTestRunnerService(&scheduledPlansStub{}, nil, nil, scheduledRunnerOptions(schedule, 10*time.Second))
		require.Zero(t, schedule.starts.Load())
		if beforeStart {
			require.NoError(t, runner.StopContext(context.Background()))
		}
		require.NoError(t, runner.StartContext(context.Background()))
		require.NoError(t, runner.StartContext(context.Background()))
		require.NoError(t, runner.StopContext(context.Background()))
		require.NoError(t, runner.StartContext(context.Background()))
		if beforeStart {
			require.Zero(t, schedule.starts.Load())
		} else {
			require.Equal(t, int32(1), schedule.starts.Load())
		}
		require.Equal(t, int32(1), schedule.stops.Load())
	}
}

// 十秒偏移必须被停止立即取消，不能等偏移结束后再读取到期计划。
func TestScheduledRunnerCancelsOffsetBeforeClaim(t *testing.T) {
	plans := &scheduledPlansStub{}
	schedule := &scheduledScheduleStub{}
	runner := NewScheduledTestRunnerService(plans, nil, nil, scheduledRunnerOptions(schedule, 10*time.Second))
	done := make(chan struct{})
	go func() { runner.runScheduled(); close(done) }()
	waitScheduledRound(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	require.NoError(t, runner.StopContext(ctx))
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("停止未结束偏移等待")
	}
	require.Zero(t, plans.reads.Load())
	runner.runScheduled()
	require.Zero(t, plans.reads.Load())
}

// 忽略取消的在途执行必须报告未完成，迟到结束也不能抹掉首次超时结果。
func TestScheduledRunnerReportsUnfinishedExecution(t *testing.T) {
	plans := &scheduledPlansStub{plans: []*ScheduledTestPlan{{ID: 1, AccountID: 2}}}
	executor := scheduledExecutorStub{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(executor.release) })
	defer release()
	svc := NewScheduledTestService(plans, scheduledResultsStub{}, ScheduledTestOptions{})
	runner := NewScheduledTestRunnerService(plans, svc, executor, scheduledRunnerOptions(&scheduledScheduleStub{}, 0))
	done := make(chan struct{})
	go func() { runner.runScheduled(); close(done) }()
	select {
	case <-executor.entered:
	case <-time.After(time.Second):
		t.Fatal("执行器没有取得计划")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := runner.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "scheduled tests remain unfinished")
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("已释放执行器仍未退出")
	}
	require.Equal(t, err, runner.StopContext(context.Background()))
}

type scheduledBlockedStop struct {
	scheduledScheduleStub
	release chan struct{}
	done    chan struct{}
}

func (s *scheduledBlockedStop) Stop(context.Context) error {
	s.stops.Add(1)
	<-s.release
	close(s.done)
	return nil
}

// 调度器端口忽略取消时，执行器仍必须有界返回并报告具体未完成阶段。
func TestScheduledRunnerBoundsSchedulerStop(t *testing.T) {
	schedule := &scheduledBlockedStop{release: make(chan struct{}), done: make(chan struct{})}
	release := sync.OnceFunc(func() { close(schedule.release) })
	defer release()
	runner := NewScheduledTestRunnerService(&scheduledPlansStub{}, nil, nil, scheduledRunnerOptions(schedule, time.Second))
	require.NoError(t, runner.StartContext(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := runner.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "scheduled cron stop remains unfinished")
	release()
	select {
	case <-schedule.done:
	case <-time.After(time.Second):
		t.Fatal("释放后的日历停止调用仍未返回")
	}
	require.Equal(t, err, runner.StopContext(context.Background()))
	require.Equal(t, int32(1), schedule.stops.Load())
}

// 部分启动失败后仍能取消已进入的回调并释放日历调度资源。
func TestScheduledRunnerCleansPartialStart(t *testing.T) {
	plans := &scheduledPlansStub{}
	failed := errors.New("forced schedule start failure")
	done := make(chan struct{})
	schedule := &scheduledScheduleStub{err: failed, start: func(run func()) { go func() { run(); close(done) }() }}
	runner := NewScheduledTestRunnerService(plans, nil, nil, scheduledRunnerOptions(schedule, 10*time.Second))
	require.ErrorIs(t, runner.StartContext(context.Background()), failed)
	require.NoError(t, runner.StopContext(context.Background()))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("部分启动的回调没有退出")
	}
	require.Zero(t, plans.reads.Load())
	require.Equal(t, int32(1), schedule.stops.Load())
}
