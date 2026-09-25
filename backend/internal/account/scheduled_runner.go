// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ScheduledTestExecutor 保留后台结果语义；实际测试事件入口在后续批次接入同一实现。
type ScheduledTestExecutor interface {
	RunTestBackground(context.Context, int64, string) (*ScheduledTestResult, error)
}
type SuccessfulTestRecovery struct {
	ClearedError     bool
	ClearedRateLimit bool
}
type ScheduledTestSchedule interface {
	Start(context.Context, func()) error
	Stop(context.Context) error
}
type ScheduledRunnerOptions struct {
	Schedule ScheduledTestSchedule
	Now      func() time.Time
	NextRun  func(string, time.Time) (time.Time, error)
	Offset   time.Duration
	Observe  func(string, ...any)
	Recover  func(context.Context, int64) (*SuccessfulTestRecovery, error)
}

// ScheduledTestRunnerService 跟踪每轮执行，停止同时约束偏移等待、槽位等待和在途测试。
type ScheduledTestRunnerService struct {
	planRepo       ScheduledTestPlanRepository
	scheduledSvc   *ScheduledTestService
	accountTestSvc ScheduledTestExecutor
	options        ScheduledRunnerOptions
	startOnce      sync.Once
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	stopped        bool
	startErr       error
	active         int
	idle           chan struct{}
	stopDone       chan struct{}
	stopErr        error
}

func NewScheduledTestRunnerService(plans ScheduledTestPlanRepository, service *ScheduledTestService, tests ScheduledTestExecutor, options ScheduledRunnerOptions) *ScheduledTestRunnerService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: service, accountTestSvc: tests, options: options, ctx: ctx, cancel: cancel}
}
func (s *ScheduledTestRunnerService) observe(format string, args ...any) {
	if s.options.Observe != nil {
		s.options.Observe(format, args...)
	}
}
func (s *ScheduledTestRunnerService) Start() { _ = s.StartContext(context.Background()) }
func (s *ScheduledTestRunnerService) StartContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.startOnce.Do(func() {
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		var err error
		if s.options.Schedule == nil {
			err = errors.New("scheduled test schedule is not configured")
		} else {
			startCtx, cancel := context.WithCancel(s.ctx)
			defer cancel()
			after := context.AfterFunc(ctx, cancel)
			defer after()
			err = s.options.Schedule.Start(startCtx, s.runScheduled)
		}
		s.mu.Lock()
		s.startErr = err
		stopped = s.stopped
		s.mu.Unlock()
		if err != nil {
			s.observe("[ScheduledTestRunner] not started (invalid schedule): %v", err)
		} else if !stopped {
			s.observe("[ScheduledTestRunner] started (tick=every minute)")
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startErr
}
func (s *ScheduledTestRunnerService) Stop() { _ = s.StopContext(context.Background()) }

// StopContext 共享首次结果；超时后仍保留未完成状态，不能谎报任务已经结束。
func (s *ScheduledTestRunnerService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.stopped {
		done := s.stopDone
		s.mu.Unlock()
		select {
		case <-done:
			return s.stopErr
		default:
		}
		select {
		case <-done:
			return s.stopErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.stopped = true
	s.stopDone = make(chan struct{})
	done := s.stopDone
	idle := s.idle
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	var stopErr error
	if s.options.Schedule != nil {
		scheduleDone := make(chan error, 1)
		go func() { scheduleDone <- s.options.Schedule.Stop(ctx) }()
		select {
		case stopErr = <-scheduleDone:
		case <-ctx.Done():
			stopErr = fmt.Errorf("scheduled cron stop remains unfinished: %w", ctx.Err())
		}
	}
	if idle != nil {
		select {
		case <-idle:
		default:
			select {
			case <-idle:
			case <-ctx.Done():
				stopErr = errors.Join(stopErr, fmt.Errorf("scheduled tests remain unfinished: %w", ctx.Err()))
			}
		}
	}
	s.mu.Lock()
	s.stopErr = stopErr
	close(done)
	s.mu.Unlock()
	return stopErr
}
func (s *ScheduledTestRunnerService) runScheduled() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.active++
	if s.active == 1 {
		s.idle = make(chan struct{})
	}
	parent := s.ctx
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		if s.active == 0 {
			close(s.idle)
		}
		s.mu.Unlock()
	}()
	// 仍落在分钟约 :10；停止时不再等待不可取消的十秒 Sleep。
	delay := time.NewTimer(s.options.Offset)
	defer delay.Stop()
	select {
	case <-parent.Done():
		return
	case <-delay.C:
	}
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	plans, err := s.planRepo.ListDue(ctx, s.options.Now())
	if err != nil {
		s.observe("[ScheduledTestRunner] ListDue error: %v", err)
		return
	}
	if len(plans) == 0 {
		return
	}
	s.observe("[ScheduledTestRunner] found %d due plans", len(plans))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for _, plan := range plans {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func(p *ScheduledTestPlan) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() == nil {
				s.runOnePlan(ctx, p)
			}
		}(plan)
	}
	wg.Wait()
}

func (s *ScheduledTestRunnerService) runOnePlan(ctx context.Context, plan *ScheduledTestPlan) {
	result, err := s.accountTestSvc.RunTestBackground(ctx, plan.AccountID, plan.ModelID)
	if err != nil {
		s.observe("[ScheduledTestRunner] plan=%d RunTestBackground error: %v", plan.ID, err)
		return
	}

	if err := s.scheduledSvc.SaveResult(ctx, plan.ID, plan.MaxResults, result); err != nil {
		s.observe("[ScheduledTestRunner] plan=%d SaveResult error: %v", plan.ID, err)
	}

	// 仅测试成功且计划启用自动恢复时执行原恢复入口。
	if result.Status == "success" && plan.AutoRecover {
		s.tryRecoverAccount(ctx, plan.AccountID, plan.ID)
	}

	nextRun, err := s.options.NextRun(plan.CronExpression, s.options.Now())
	if err != nil {
		s.observe("[ScheduledTestRunner] plan=%d computeNextRun error: %v", plan.ID, err)
		return
	}

	if err := s.planRepo.UpdateAfterRun(ctx, plan.ID, s.options.Now(), nextRun); err != nil {
		s.observe("[ScheduledTestRunner] plan=%d UpdateAfterRun error: %v", plan.ID, err)
	}
}

// tryRecoverAccount 保留原可恢复状态的判断结果与日志。
func (s *ScheduledTestRunnerService) tryRecoverAccount(ctx context.Context, accountID int64, planID int64) {
	if s.options.Recover == nil {
		return
	}

	recovery, err := s.options.Recover(ctx, accountID)
	if err != nil {
		s.observe("[ScheduledTestRunner] plan=%d auto-recover failed: %v", planID, err)
		return
	}
	if recovery == nil {
		return
	}

	if recovery.ClearedError {
		s.observe("[ScheduledTestRunner] plan=%d auto-recover: account=%d recovered from error status", planID, accountID)
	}
	if recovery.ClearedRateLimit {
		s.observe("[ScheduledTestRunner] plan=%d auto-recover: account=%d cleared rate-limit/runtime state", planID, accountID)
	}
}
