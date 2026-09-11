// Package timingwheel 提供可以停止并等待在途回调的时间轮。
package timingwheel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/zeromicro/go-zero/core/collection"
)

var newTimingWheel = collection.NewTimingWheel

type task struct {
	name      string
	delay     time.Duration
	recurring bool
	fn        func()
	wg        sync.WaitGroup
}

// Wheel 的构造不启动 goroutine。任务身份防止取消后的回调重新覆盖同名新任务。
type Wheel struct {
	mu       sync.Mutex
	tw       *collection.TimingWheel
	tasks    map[string]*task
	stopped  bool
	wg       sync.WaitGroup
	stopDone chan struct{}
}

func New() *Wheel { return &Wheel{tasks: make(map[string]*task)} }

// Start 保持一秒 tick 与 3600 槽，只启动一次；初始化错误交由 app 回收。
func (w *Wheel) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return fmt.Errorf("timing wheel already stopped")
	}
	if w.tw != nil {
		return nil
	}
	tw, err := newTimingWheel(time.Second, 3600, func(_ any, value any) {
		if fn, ok := value.(func()); ok {
			fn()
		}
	})
	if err != nil {
		return fmt.Errorf("创建 timing wheel 失败: %w", err)
	}
	w.tw = tw
	for _, t := range w.tasks {
		w.arm(t)
	}
	logging.LegacyPrintf("service.timing_wheel", "%s", "[TimingWheel] Started")
	return nil
}

func (w *Wheel) Schedule(name string, delay time.Duration, fn func()) {
	w.schedule(name, delay, fn, false)
}

func (w *Wheel) ScheduleRecurring(name string, delay time.Duration, fn func()) {
	w.schedule(name, delay, fn, true)
}

func (w *Wheel) schedule(name string, delay time.Duration, fn func(), recurring bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		logging.LegacyPrintf("service.timing_wheel", "[TimingWheel] SetTimer failed for %q: stopped", name)
		return
	}
	t := &task{name: name, delay: delay, fn: fn, recurring: recurring}
	w.tasks[name] = t
	if w.tw != nil {
		w.arm(t)
	}
}

// arm 在持锁状态投递；底层回调异步执行，不在投递线程调用业务函数。
func (w *Wheel) arm(t *task) {
	if err := w.tw.SetTimer(t.name, func() { w.execute(t) }, t.delay); err != nil {
		logging.LegacyPrintf("service.timing_wheel", "[TimingWheel] SetTimer failed for %q: %v", t.name, err)
	}
}

func (w *Wheel) execute(t *task) {
	w.mu.Lock()
	if w.stopped || w.tasks[t.name] != t {
		w.mu.Unlock()
		return
	}
	w.wg.Add(1)
	t.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()
	defer t.wg.Done()
	if t.fn != nil {
		t.fn()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.tasks[t.name] != t {
		return
	}
	if t.recurring {
		w.arm(t)
	} else {
		delete(w.tasks, t.name)
	}
}

func (w *Wheel) cancel(name string) *task {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.tasks[name]
	delete(w.tasks, name)
	if w.tw != nil && !w.stopped {
		_ = w.tw.RemoveTimer(name)
	}
	return t
}

func (w *Wheel) Cancel(name string) { w.cancel(name) }

// CancelAndWait 用于拥有者停止及最后一次 flush；不能在该任务自己的回调中调用。
func (w *Wheel) CancelAndWait(name string) {
	if t := w.cancel(name); t != nil {
		t.wg.Wait()
	}
}

func (w *Wheel) Stop() { _ = w.Shutdown(context.Background()) }

// Shutdown 先封闭调度，再等待已进入执行的回调；超时不会谎报回调已经结束。
func (w *Wheel) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	if w.stopDone == nil {
		w.stopped = true
		w.stopDone = make(chan struct{})
		if w.tw != nil {
			w.tw.Stop()
		}
		clear(w.tasks)
		go func() { w.wg.Wait(); close(w.stopDone) }()
	}
	done := w.stopDone
	w.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
