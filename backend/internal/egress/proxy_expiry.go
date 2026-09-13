// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	context "context"
	fmt "fmt"
	log "log"
	sync "sync"
	time "time"
)

// ProxyExpiryService 拥有代理到期扫描的启动与停止屏障。
// 构造不启动；单次扫描保留十秒预算，停止时取消并等待当前扫描。
type ProxyExpiryService struct {
	proxyRepo ProxyRepository
	interval  time.Duration
	now       func() time.Time
	mu        sync.Mutex
	started   bool
	stopped   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewProxyExpiryService(repo ProxyRepository, interval time.Duration, clocks ...func() time.Time) *ProxyExpiryService {
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &ProxyExpiryService{proxyRepo: repo, interval: interval, now: now}
}

// Start 保留立即首轮，只允许同一实例启动一次。
// @project-doc docs/operations/upstream_transport_security.md#upstream_proxy_lifecycle
func (s *ProxyExpiryService) Start() {
	if s == nil || s.proxyRepo == nil || s.interval <= 0 {
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
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}
func (s *ProxyExpiryService) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			s.sweep(ctx)
		}
	}
}
func (s *ProxyExpiryService) sweep(parent context.Context) {
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	changed, err := s.proxyRepo.SweepExpiredProxies(ctx, s.now())
	if err != nil {
		log.Printf("[ProxyExpiry] sweep expired proxies failed: %v", err)
		return
	}
	if changed > 0 {
		log.Printf("[ProxyExpiry] re-routed %d accounts off expired proxies", changed)
	}
}

// StopContext 不把超时返回当作扫描完成，生命周期继续保护其共享依赖。
func (s *ProxyExpiryService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("ProxyExpiryService stop: %w", ctx.Err())
	}
}
func (s *ProxyExpiryService) Stop() { _ = s.StopContext(context.Background()) }
