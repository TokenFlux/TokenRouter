package idempotency

import (
	"context"
	"sync"
	"time"

	"fmt"
)

// IdempotencyCleanupService 定期清理已过期的幂等记录，避免表无限增长。
type IdempotencyCleanupService struct {
	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	repo        IdempotencyRepository
	interval    time.Duration
	batch       int

	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
}

// CleanupOptions 由 app 从启动配置投影。
type CleanupOptions struct {
	Interval time.Duration
	Batch    int
}

func NewIdempotencyCleanupService(repo IdempotencyRepository, opts CleanupOptions) *IdempotencyCleanupService {
	interval := opts.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	batch := opts.Batch
	if batch <= 0 {
		batch = 500
	}
	return &IdempotencyCleanupService{repo: repo, interval: interval, batch: batch, stopCh: make(chan struct{})}
}

func (s *IdempotencyCleanupService) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true

	s.startOnce.Do(func() {
		observe("service.idempotency_cleanup", fmt.Sprintf("[IdempotencyCleanup] started interval=%s batch=%d", s.interval, s.batch))
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.runLoop() }()
	})
}

func (s *IdempotencyCleanupService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.stopped = true

	s.stopOnce.Do(func() {
		close(s.stopCh)
		observe("service.idempotency_cleanup", "[IdempotencyCleanup] stopped")
	})
	s.lifecycleMu.Unlock()
	s.wg.Wait()
}

func (s *IdempotencyCleanupService) runLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// 启动后先清理一轮，防止重启后积压。
	s.cleanupOnce()

	for {
		select {
		case <-ticker.C:
			s.cleanupOnce()
		case <-s.stopCh:
			return
		}
	}
}

func (s *IdempotencyCleanupService) cleanupOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deleted, err := s.repo.DeleteExpired(ctx, time.Now(), s.batch)
	if err != nil {
		observe("service.idempotency_cleanup", fmt.Sprintf("[IdempotencyCleanup] cleanup failed err=%v", err))
		return
	}
	if deleted > 0 {
		observe("service.idempotency_cleanup", fmt.Sprintf("[IdempotencyCleanup] cleaned expired records count=%d", deleted))
	}
}
