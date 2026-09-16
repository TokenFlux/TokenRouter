package live

import (
	"context"
	"sync"
)

// ObserverState 是已有进程观察者登记的明确技术投影。
// 四个字段必须来自同一拥有者；过渡适配只传引用，不创建第二份取消表或等待计数。
type ObserverState struct {
	Mutex   *sync.Mutex
	Stopped *bool
	Cancels *map[string]context.CancelFunc
	Wait    *sync.WaitGroup
}

// Begin 在同一屏障内登记观察者，停止后不再接收任务。
func (s ObserverState) Begin(owner string) (context.Context, func(), bool) {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	if *s.Stopped {
		return nil, nil, false
	}
	if *s.Cancels == nil {
		*s.Cancels = make(map[string]context.CancelFunc)
	}
	ctx, cancel := context.WithCancel(context.Background())
	(*s.Cancels)[owner] = cancel
	s.Wait.Add(1)
	return ctx, func() {
		cancel()
		s.Mutex.Lock()
		delete(*s.Cancels, owner)
		s.Mutex.Unlock()
		s.Wait.Done()
	}, true
}

// Stop 取消本地观察循环，不删除远端会话；由应用剩余预算约束等待。
func (s ObserverState) Stop(ctx context.Context) error {
	s.Mutex.Lock()
	*s.Stopped = true
	for _, cancel := range *s.Cancels {
		cancel()
	}
	s.Mutex.Unlock()
	done := make(chan struct{})
	go func() { s.Wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
