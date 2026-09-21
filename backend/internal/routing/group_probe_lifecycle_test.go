package routing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 可控调度器避免测试依赖真实分钟边界，只记录实际启停及回调绑定。
type probeScheduleStub struct {
	starts int
	stops  int
	run    func()
	err    error
}

func (s *probeScheduleStub) Start(run func()) error { s.starts++; s.run = run; return s.err }
func (s *probeScheduleStub) Stop() context.Context {
	s.stops++
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

type probeExecutorStub struct{}

func (probeExecutorStub) Select(context.Context, GroupAvailabilityProbeDueGroup, string) (int64, error) {
	return 1, nil
}
func (probeExecutorStub) Test(context.Context, int64, string, string, string) (*ProbeExecutionResult, error) {
	return &ProbeExecutionResult{Status: GroupAvailabilityProbeStatusSuccess}, nil
}

func TestS06ProbeStopPreventsLaterStart(t *testing.T) {
	schedule := &probeScheduleStub{}
	runner := NewGroupAvailabilityProbeRunnerService(&groupAvailabilityProbeRunnerRepoStub{}, probeExecutorStub{}, GroupProbeOptions{Schedule: schedule})
	require.Zero(t, schedule.starts)
	runner.Stop()
	runner.Start()
	require.Zero(t, schedule.starts)
	require.NoError(t, runner.StopContext(context.Background()))
}
func TestProbeRepeatedStartAndStop(t *testing.T) {
	schedule := &probeScheduleStub{}
	repo := &groupAvailabilityProbeRunnerRepoStub{}
	runner := NewGroupAvailabilityProbeRunnerService(repo, probeExecutorStub{}, GroupProbeOptions{Schedule: schedule})
	runner.Start()
	runner.Start()
	require.Equal(t, 1, schedule.starts)
	runner.Stop()
	schedule.run()
	calls, _ := repo.claimSnapshot()
	require.Zero(t, calls)
	runner.Stop()
	require.Equal(t, 1, schedule.stops)
}
func TestProbeStopCancelsClaimAndWaitsForRun(t *testing.T) {
	started := make(chan struct{})
	repo := &groupAvailabilityProbeRunnerRepoStub{claimStarted: started, releaseClaim: make(chan struct{})}
	runner := NewGroupAvailabilityProbeRunnerService(repo, probeExecutorStub{}, GroupProbeOptions{})
	done := make(chan struct{})
	go func() { defer close(done); runner.runDue() }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runner.StopContext(ctx))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("停止后领取仍未退出")
	}
	runner.runDue()
	calls, _ := repo.claimSnapshot()
	require.Equal(t, 1, calls)
}

// 即便底层暂不响应取消，关闭调用也受预算约束，只有其真正结束后才报告完成。
type uncancellableProbeRepo struct {
	groupAvailabilityProbeRunnerRepoStub
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *uncancellableProbeRepo) ClaimDue(context.Context, time.Time, time.Time, string, int) ([]GroupAvailabilityProbeDueGroup, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return nil, nil
}
func TestProbeStopBudgetReportsUnfinishedClaim(t *testing.T) {
	repo := &uncancellableProbeRepo{started: make(chan struct{}), release: make(chan struct{})}
	runner := NewGroupAvailabilityProbeRunnerService(repo, probeExecutorStub{}, GroupProbeOptions{})
	done := make(chan struct{})
	go func() { defer close(done); runner.runDue() }()
	<-repo.started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := runner.StopContext(ctx)
	close(repo.release)
	<-done
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, runner.StopContext(context.Background()))
}
func TestProbeInvalidScheduleDoesNotRetryStart(t *testing.T) {
	schedule := &probeScheduleStub{err: errors.New("invalid schedule")}
	runner := NewGroupAvailabilityProbeRunnerService(&groupAvailabilityProbeRunnerRepoStub{}, probeExecutorStub{}, GroupProbeOptions{Schedule: schedule})
	runner.Start()
	runner.Start()
	runner.Stop()
	require.Equal(t, 1, schedule.starts)
}
