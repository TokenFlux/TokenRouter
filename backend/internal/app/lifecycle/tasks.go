package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Tasks 只跟踪已有异步工作的完成，不改变其 context、并发数或重试策略。
// 等待期间允许在途任务继续派生子任务；全部完成后才封闭最终停止入口。
type Tasks struct {
	mu       sync.Mutex
	active   map[string]int
	count    int
	idle     chan struct{}
	stopping bool
	closed   bool
}

func NewTasks() *Tasks {
	idle := make(chan struct{})
	close(idle)
	return &Tasks{active: make(map[string]int), idle: idle}
}

// Go 的任务参数应在调用方提前求值，与原 go 语句保持一致。
func (t *Tasks) Go(name string, fn func()) bool {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return false
	}
	if t.count == 0 {
		t.idle = make(chan struct{})
	}
	t.count++
	t.active[name]++
	t.mu.Unlock()
	go func() {
		defer t.complete(name)
		fn()
	}()
	return true
}

func (t *Tasks) complete(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active[name]--
	if t.active[name] == 0 {
		delete(t.active, name)
	}
	t.count--
	if t.count == 0 {
		if t.stopping {
			t.closed = true
		}
		close(t.idle)
	}
}

// Wait 是消费层之间的完成屏障；后续消费层仍可提交原有异步副作用。
func (t *Tasks) Wait(ctx context.Context) error { return t.wait(ctx, false) }

// Stop 在最后一个生产者完成后调用，最终封闭任务入口。
func (t *Tasks) Stop(ctx context.Context) error { return t.wait(ctx, true) }

func (t *Tasks) wait(ctx context.Context, stop bool) error {
	t.mu.Lock()
	if stop {
		t.stopping = true
		if t.count == 0 {
			t.closed = true
		}
	}
	idle := t.idle
	t.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		t.mu.Lock()
		names := make([]string, 0, len(t.active))
		for name, n := range t.active {
			names = append(names, fmt.Sprintf("%s=%d", name, n))
		}
		t.mu.Unlock()
		sort.Strings(names)
		return fmt.Errorf("background work incomplete [%s]: %w", strings.Join(names, ", "), ctx.Err())
	}
}
