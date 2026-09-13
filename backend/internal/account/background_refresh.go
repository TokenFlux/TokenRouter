package account

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// BackgroundTokenRefresher 提供已选平台的资格和交换能力，核心不识别具体 SDK。
type BackgroundTokenRefresher interface {
	RefreshTokenOperation
	CanRefresh(*Record) bool
	NeedsRefresh(*Record, time.Duration) bool
}
type RefreshRegistration struct {
	Platform  string
	Refresher BackgroundTokenRefresher
	Executor  OAuthRefreshExecutor
}

// BackgroundRefreshOptions 由 app 投影静态配置与平台端口；所有共享运行状态均属于本实例。
type BackgroundRefreshOptions struct {
	Tuning                   *RefreshTuning
	Pager                    OAuthRefreshCandidatePager
	Registrations            []RefreshRegistration
	Attempts                 RefreshAttempts
	Reconciliation           GrokReconciliationOptions
	Debug, Info, Warn, Error func(string, ...any)
}

// @project-doc docs/operations/account_maintenance.md#account_credential_refresh
// BackgroundRefreshService 统一持有周期、游标、平台准入及按需管理对账的生命周期。
type BackgroundRefreshService struct {
	options        BackgroundRefreshOptions
	loop           *RefreshLoop
	scan           RefreshCandidateScan
	gates          RefreshProviderGates
	reconciliation *GrokReconciliationService
	activity       operationActivity
	stopMu         sync.Mutex
	stopDone       chan struct{}
	stopErr        error
}

func NewBackgroundRefreshService(options BackgroundRefreshOptions) *BackgroundRefreshService {
	noop := func(string, ...any) {}
	if options.Info == nil {
		options.Info = noop
	}
	if options.Warn == nil {
		options.Warn = noop
	}
	if options.Error == nil {
		options.Error = noop
	}
	if options.Debug == nil {
		options.Debug = noop
	}
	if options.Tuning != nil {
		copy := *options.Tuning
		options.Tuning = &copy
	}
	options.Registrations = slices.Clone(options.Registrations)
	options.Attempts.Tuning = options.Tuning
	s := &BackgroundRefreshService{options: options}
	s.loop = NewRefreshLoop(s.ScanCycle)
	options.Reconciliation.Pager = options.Pager
	options.Reconciliation.Execution = func() *RefreshProviderExecution {
		for _, registration := range options.Registrations {
			if registration.Platform == PlatformGrok && registration.Refresher != nil {
				return s.execution(registration)
			}
		}
		return nil
	}
	s.reconciliation = NewGrokReconciliationService(options.Reconciliation)
	return s
}
func (s *BackgroundRefreshService) StartContext(ctx context.Context) error {
	if s.isStopping() {
		return nil
	}
	tuning := s.options.Tuning
	if tuning == nil || !tuning.Enabled {
		s.options.Info("token_refresh.service_disabled")
		return nil
	}
	interval := time.Duration(tuning.CheckIntervalMinutes) * time.Minute
	if interval < time.Minute {
		interval = 5 * time.Minute
	}
	started, err := s.loop.StartContext(ctx, interval)
	if started {
		s.options.Info("token_refresh.service_started", "check_interval_minutes", tuning.CheckIntervalMinutes, "refresh_before_expiry_hours", tuning.RefreshBeforeExpiryHours)
	}
	return err
}

// StopContext 同时取消独立生产入口，并固定首次停止结果；依赖释放仍由 app 在其后执行。
func (s *BackgroundRefreshService) StopContext(ctx context.Context) error {
	s.stopMu.Lock()
	if s.stopDone != nil {
		done := s.stopDone
		s.stopMu.Unlock()
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
	s.stopDone = make(chan struct{})
	done := s.stopDone
	s.stopMu.Unlock()
	results := make([]error, 3)
	var workers sync.WaitGroup
	stops := []func(context.Context) error{s.loop.StopContext, func(ctx context.Context) error { return s.activity.stop(ctx, "account refresh scans") }, s.reconciliation.StopContext}
	for i, stop := range stops {
		workers.Add(1)
		go func() { defer workers.Done(); results[i] = stop(ctx) }()
	}
	workers.Wait()
	err := errors.Join(results...)
	s.stopMu.Lock()
	s.stopErr = err
	close(done)
	s.stopMu.Unlock()
	if err != nil {
		s.options.Warn("token_refresh.service_stop_incomplete", "error", err)
	} else {
		s.options.Info("token_refresh.service_stopped")
	}
	return err
}
func (s *BackgroundRefreshService) isStopping() bool {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	return s.stopDone != nil
}

func (s *BackgroundRefreshService) ReconcileGrokOAuth(ctx context.Context, input GrokOAuthReconcileInput) (*GrokOAuthReconcileResult, error) {
	if s.isStopping() {
		return nil, ErrRefreshStopped
	}
	return s.reconciliation.ReconcileGrokOAuth(ctx, input)
}
func (s *BackgroundRefreshService) CandidatePosition() int64 { return s.scan.Position() }
func (s *BackgroundRefreshService) ProviderRateGate(platform string) *RefreshRateGate {
	return s.gates.Rate(platform, s.options.Tuning.QPS())
}
func (s *BackgroundRefreshService) ProviderConcurrencyGate(platform string) *RefreshConcurrencyGate {
	return s.gates.Pool(platform, s.options.Tuning.Concurrency())
}

func (s *BackgroundRefreshService) execution(registration RefreshRegistration) *RefreshProviderExecution {
	state := NewRefreshProviderState(s.ProviderRateGate(registration.Platform), s.ProviderConcurrencyGate(registration.Platform), s.options.Tuning.FailureThreshold(), s.options.Attempts.NonRetryable)
	return &RefreshProviderExecution{Platform: registration.Platform, State: state, CanRefresh: registration.Refresher.CanRefresh, NeedsRefresh: registration.Refresher.NeedsRefresh,
		Execute: func(ctx context.Context, v *Record, window time.Duration, state *RefreshProviderState) error {
			return s.options.Attempts.Run(ctx, v, registration.Refresher, registration.Executor, window, state)
		},
	}
}

// ScanCycle 保留每轮独立平台失败计数、共享准入及未完成页不推进游标。
func (s *BackgroundRefreshService) ScanCycle(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	if s.isStopping() {
		return
	}
	parent, finish, err := s.activity.begin(parent, ErrRefreshStopped)
	if err != nil {
		return
	}
	defer finish()
	platforms := make([]string, 0, len(s.options.Registrations))
	states := make(map[string]*RefreshProviderExecution, len(s.options.Registrations))
	for _, registration := range s.options.Registrations {
		if registration.Platform != "" && registration.Refresher != nil {
			platforms = append(platforms, registration.Platform)
			states[registration.Platform] = s.execution(registration)
		}
	}
	window := time.Duration(0)
	if s.options.Tuning != nil {
		window = time.Duration(s.options.Tuning.RefreshBeforeExpiryHours * float64(time.Hour))
	}
	processor := RefreshPageProcessor{Concurrency: s.options.Tuning.Concurrency(), Info: s.options.Info, Warn: s.options.Warn}
	s.scan.Run(parent, s.options.Pager, RefreshScanOptions{Timeout: s.options.Tuning.CycleTimeout(), PageSize: s.options.Tuning.PageSize(), Platforms: platforms, Debug: s.options.Debug, Info: s.options.Info, Warn: s.options.Warn, Error: s.options.Error}, func(ctx context.Context, values []Record) RefreshPageStats {
		return processor.ProcessPage(ctx, values, states, window)
	})
}
