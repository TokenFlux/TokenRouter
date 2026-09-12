package apikey

import (
	"context"
	"errors"
	"sync"
)

// ErrAuthenticationStopped 只用于关闭期间拒绝新的认证工作，不与无效 Key 混淆。
var ErrAuthenticationStopped = errors.New("API Key authentication is stopping")

// authOperationGate 把停止认领与在途计数放在同一个临界区，禁止 Wait 与零计数 Add 竞争。
type authOperationGate struct {
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
}

func (g *authOperationGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return false
	}
	g.active.Add(1)
	return true
}
func (g *authOperationGate) leave()           { g.active.Done() }
func (g *authOperationGate) stop()            { g.mu.Lock(); g.stopping = true; g.mu.Unlock() }
func (g *authOperationGate) isStopping() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.stopping }

// Start 只在 app 完成绑定后初始化缓存与订阅；停止过的实例不能重新认领工作。
func (s *APIKeyService) Start() {
	if s == nil {
		return
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.operations.isStopping() {
		return
	}
	s.runtimeStart.Do(func() { s.initAuthCaches(); s.StartAuthCacheInvalidationSubscriber(context.Background()) })
}

// StopContext 拒绝新工作并等待在途完成，再取消订阅和关闭 L1；超时不假装资源已经释放。
func (s *APIKeyService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.runtimeMu.Lock()
	s.runtimeStop.Do(func() {
		s.operations.stop()
		s.stopped = make(chan struct{})
		go func() {
			s.operations.active.Wait()
			s.StopAuthCacheInvalidationSubscriber()
			if cache := s.authCacheL1.Load(); cache != nil {
				cache.Close()
			}
			if cache := s.authNegativeCacheL1.Load(); cache != nil {
				cache.Close()
			}
			close(s.stopped)
		}()
	})
	done := s.stopped
	s.runtimeMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop 保留旧同步等待入口，生产生命周期使用带预算的 StopContext。
func (s *APIKeyService) Stop() { _ = s.StopContext(context.Background()) }
