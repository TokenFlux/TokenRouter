package account

import (
	"context"
	"sync"
	"time"
)

// ExpiryRepository 只维护启用自动暂停的过期账号，不读取凭据或更改其他停调状态。
type ExpiryRepository interface {
	AutoPauseExpiredAccounts(context.Context, time.Time) (int64, error)
}
type ExpiryOptions struct {
	Interval time.Duration
	Now      func() time.Time
	Observe  func(string, ...any)
}

// ExpiryService 保留启动首轮和周期，构造不扫描，停止后不能重开。
type ExpiryService struct {
	repo             ExpiryRepository
	options          ExpiryOptions
	mu               sync.Mutex
	started, stopped bool
	cancel           context.CancelFunc
	runDone          chan struct{}
	stopDone         chan struct{}
	stopErr          error
}

func NewExpiryService(repo ExpiryRepository, options ExpiryOptions) *ExpiryService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Observe == nil {
		options.Observe = func(string, ...any) {}
	}
	return &ExpiryService{repo: repo, options: options}
}
func (s *ExpiryService) Start() {
	if s == nil || s.repo == nil || s.options.Interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.runDone = make(chan struct{})
	go s.run(ctx)
}
func (s *ExpiryService) run(ctx context.Context) {
	defer close(s.runDone)
	ticker := time.NewTicker(s.options.Interval)
	defer ticker.Stop()
	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}
func (s *ExpiryService) scan(parent context.Context) {
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	updated, err := s.repo.AutoPauseExpiredAccounts(ctx, s.options.Now())
	if err != nil {
		s.options.Observe("[AccountExpiry] Auto pause expired accounts failed: %v", err)
		return
	}
	if updated > 0 {
		s.options.Observe("[AccountExpiry] Auto paused %d expired accounts", updated)
	}
}
func (s *ExpiryService) Stop() { _ = s.StopContext(context.Background()) }

// StopContext 取消周期与扫描并等待在途；第一次停止的超时结果不被后续调用改写。
func (s *ExpiryService) StopContext(ctx context.Context) error {
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
	runDone := s.runDone
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	var err error
	if runDone != nil {
		select {
		case <-runDone:
		default:
			select {
			case <-runDone:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
	}
	s.mu.Lock()
	s.stopErr = err
	close(done)
	s.mu.Unlock()
	return err
}
