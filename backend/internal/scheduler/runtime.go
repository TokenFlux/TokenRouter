package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// RuntimeTask 声明一个初始任务或周期任务，构造不执行 Run。
type RuntimeTask struct {
	Name      string
	Interval  time.Duration
	Immediate bool
	Run       func(context.Context)
}

// WorkerRuntime 拥有调度后台任务的启动屏障、取消和等待；零值可用。
type WorkerRuntime struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	stopped  bool
	active   map[string]int
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopErr  error
}

func (r *WorkerRuntime) initializeLocked() {
	if r.ctx == nil {
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.active = make(map[string]int)
	}
}

// Context 在首次运行前也可供同步维护使用，停止后始终返回已取消的同一个上下文。
func (r *WorkerRuntime) Context() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initializeLocked()
	return r.ctx
}

// Start 只启动一次；所有 WaitGroup 登记在停止屏障之前完成。
func (r *WorkerRuntime) Start(tasks ...RuntimeTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initializeLocked()
	if r.started || r.stopped {
		return
	}
	r.started = true
	for _, task := range tasks {
		if task.Run == nil {
			continue
		}
		r.wg.Add(1)
		r.active[task.Name]++
		go r.run(task)
	}
}

func (r *WorkerRuntime) run(task RuntimeTask) {
	defer func() {
		r.mu.Lock()
		r.active[task.Name]--
		r.mu.Unlock()
		r.wg.Done()
	}()
	ctx := r.Context()
	if ctx.Err() != nil {
		return
	}
	// 周期计时先于立即首轮建立，保持旧 outbox 长首轮后的 tick 时序。
	var ticks <-chan time.Time
	if task.Interval > 0 {
		ticker := time.NewTicker(task.Interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	if task.Immediate || task.Interval <= 0 {
		task.Run(ctx)
	}
	if task.Interval <= 0 {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if ctx.Err() != nil {
				return
			}
			task.Run(ctx)
		}
	}
}

// StopContext 固定首次停止结果；超时不把尚未完成的任务报告为已经清理。
func (r *WorkerRuntime) StopContext(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.initializeLocked()
		r.stopped = true
		r.cancel()
		r.mu.Unlock()
		done := make(chan struct{})
		go func() { r.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			r.mu.Lock()
			var pending []string
			for name, count := range r.active {
				if count > 0 {
					pending = append(pending, name)
				}
			}
			r.mu.Unlock()
			if len(pending) > 0 {
				sort.Strings(pending)
				r.stopErr = fmt.Errorf("scheduler stop unfinished [%s]: %w", strings.Join(pending, ", "), ctx.Err())
			}
		}
	})
	return r.stopErr
}

// Enter 将按需操作登记到同一停止屏障。取消只终止等待/I/O，已取得资源仍由租约完成释放。
func (r *WorkerRuntime) Enter(parent context.Context, name string) (context.Context, func(), error) {
	r.mu.Lock()
	r.initializeLocked()
	if r.stopped {
		r.mu.Unlock()
		return nil, nil, ErrRuntimeStopped
	}
	r.wg.Add(1)
	r.active[name]++
	runtimeContext := r.ctx
	r.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(runtimeContext, cancel)
	var once sync.Once
	done := func() {
		once.Do(func() {
			stop()
			cancel()
			r.mu.Lock()
			r.active[name]--
			r.mu.Unlock()
			r.wg.Done()
		})
	}
	return ctx, done, nil
}

// ErrRuntimeStopped 表示不能在已停止的实例中重新认领资源。
var ErrRuntimeStopped = errors.New("scheduler runtime stopped")
