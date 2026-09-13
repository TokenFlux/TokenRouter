package scheduler

import (
	"context"
	"sync"
	"time"
)

// ImageConcurrencyLimiter 保留独立本地容量，不与账号 Redis 池合并。
type ImageConcurrencyLimiter struct {
	mu      sync.Mutex
	notify  chan struct{}
	limit   int
	active  int
	waiting int
	enabled bool
}

func (l *ImageConcurrencyLimiter) TryAcquire(enabled bool, limit int) (func(), bool) {
	return l.acquire(context.Background(), enabled, limit, false, 0, 0)
}

func (l *ImageConcurrencyLimiter) Acquire(ctx context.Context, enabled bool, limit int, wait bool, timeout time.Duration, maxWaiting int) (func(), bool) {
	return l.acquire(ctx, enabled, limit, wait, timeout, maxWaiting)
}

func (l *ImageConcurrencyLimiter) acquire(ctx context.Context, enabled bool, limit int, wait bool, timeout time.Duration, maxWaiting int) (func(), bool) {
	if !enabled || limit <= 0 {
		return nil, true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if wait {
		if timeout <= 0 {
			return nil, false
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ctx = waitCtx
	}
	if maxWaiting < 0 {
		maxWaiting = 0
	}
	for {
		release, acquired, waitRelease, notify := l.tryAcquireLocked(enabled, limit, wait, maxWaiting)
		if acquired {
			return release, acquired
		}
		if !wait || notify == nil {
			return nil, false
		}
		if !l.waitForSlot(ctx, notify) {
			if waitRelease != nil {
				waitRelease()
			}
			return nil, false
		}
		if waitRelease != nil {
			waitRelease()
		}
	}
}

func (l *ImageConcurrencyLimiter) tryAcquireLocked(enabled bool, limit int, wait bool, maxWaiting int) (func(), bool, func(), <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.notify == nil {
		l.notify = make(chan struct{})
	}
	if l.enabled != enabled || l.limit != limit {
		l.enabled = enabled
		l.limit = limit
	}
	if l.active < l.limit {
		l.active++
		return l.releaseFunc(), true, nil, nil
	}
	if !wait {
		return nil, false, nil, nil
	}
	if maxWaiting > 0 && l.waiting >= maxWaiting {
		return nil, false, nil, nil
	}
	l.waiting++
	return nil, false, l.waiterReleaseFunc(), l.notify
}

func (l *ImageConcurrencyLimiter) waitForSlot(ctx context.Context, notify <-chan struct{}) bool {
	select {
	case <-notify:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *ImageConcurrencyLimiter) releaseFunc() func() {
	return NewLease(context.Background(), ReleaseOnCompletion, func() {
		l.mu.Lock()
		if l.active > 0 {
			l.active--
		}
		if l.notify != nil {
			close(l.notify)
			l.notify = make(chan struct{})
		}
		l.mu.Unlock()
	}).Release
}

func (l *ImageConcurrencyLimiter) waiterReleaseFunc() func() {
	return NewLease(context.Background(), ReleaseOnCompletion, func() {
		l.mu.Lock()
		if l.waiting > 0 {
			l.waiting--
		}
		l.mu.Unlock()
	}).Release
}
