package creative

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"fmt"
)

// DefaultCreativeWorkerCount 是创作台任务 worker 的默认并发数。
const DefaultCreativeWorkerCount = 128

// CreativeWorkerStatus 是创作台 worker 池状态快照，供管理端展示当前使用情况。
type CreativeWorkerStatus struct {
	Running     bool `json:"running"`
	WorkerCount int  `json:"worker_count"`
	BusyWorkers int  `json:"busy_workers"`
}

type creativeWorkerHandle struct {
	id       uint64
	stop     chan struct{}
	stopping atomic.Bool
}

// CreativeWorkerRuntime 管理创作台 worker 池、delayed mover 与 stale recovery 生命周期。
type CreativeWorkerRuntime struct {
	worker  RuntimeWorker
	options RuntimeOptions

	mu                 sync.Mutex
	stopped            bool
	stopDone           chan struct{}
	stopErr            error
	cancel             context.CancelFunc
	ctx                context.Context
	done               chan struct{}
	wg                 *sync.WaitGroup
	workers            map[uint64]*creativeWorkerHandle
	nextWorkerID       uint64
	desiredWorkerCount int
}

// Start 启动任务 worker 池、delayed mover 与 stale recovery；重复调用幂等。
func (r *CreativeWorkerRuntime) Start() {
	if r == nil || r.worker == nil || !r.options.Enabled {
		return
	}

	desired := r.desiredWorkerCountValue()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil || r.stopped {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.ctx = ctx
	r.done = make(chan struct{})
	r.wg = &sync.WaitGroup{}
	r.workers = make(map[uint64]*creativeWorkerHandle)
	r.desiredWorkerCount = desired
	r.wg.Add(4)
	go func() {
		defer r.wg.Done()
		r.worker.RunDelayedMover(ctx)
	}()
	go func() {
		defer r.wg.Done()
		r.worker.RunStaleActiveRecovery(ctx)
	}()
	go func() {
		defer r.wg.Done()
		if r.options.Outbox != nil {
			r.options.Outbox(ctx)
		}
	}()
	go func() {
		defer r.wg.Done()
		if r.options.Transient != nil {
			r.options.Transient(ctx)
		}
	}()
	r.reconcileWorkersLocked()
	wg := r.wg
	done := r.done
	go func() {
		wg.Wait()
		close(done)
	}()
}

// desiredWorkerCountValue 从数据库运行时设置读取初始 worker 数量，异常时回退默认值。
func (r *CreativeWorkerRuntime) desiredWorkerCountValue() int {
	if r != nil && r.options.WorkerCount != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		count := r.options.WorkerCount(ctx)
		cancel()
		if count > 0 {
			return count
		}
	}

	if r != nil {
		r.mu.Lock()
		desired := r.desiredWorkerCount
		r.mu.Unlock()
		if desired > 0 {
			return desired
		}
	}
	return DefaultCreativeWorkerCount
}

// SetWorkerCount 热更新任务 worker 数量；缩容采用优雅排空，不取消执行中的任务。
func (r *CreativeWorkerRuntime) SetWorkerCount(count int) {
	if r == nil || count <= 0 {
		return
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.desiredWorkerCount = count
	if r.cancel != nil && r.ctx != nil && r.ctx.Err() == nil {
		r.reconcileWorkersLocked()
	}
	r.mu.Unlock()
}

// WorkerCount 返回当前期望的 worker 数量。
func (r *CreativeWorkerRuntime) WorkerCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.desiredWorkerCount <= 0 {
		return DefaultCreativeWorkerCount
	}
	return r.desiredWorkerCount
}

// Status 返回 worker 池状态快照；未运行时返回 running=false 的零值快照。
func (r *CreativeWorkerRuntime) Status() CreativeWorkerStatus {
	if r == nil || r.worker == nil {
		return CreativeWorkerStatus{}
	}
	r.mu.Lock()
	running := r.cancel != nil && !r.stopped
	workerCount := 0
	if running {
		workerCount = r.activeWorkerCountLocked()
	}
	r.mu.Unlock()
	if !running {
		return CreativeWorkerStatus{Running: false}
	}
	return CreativeWorkerStatus{
		Running:     true,
		WorkerCount: workerCount,
		BusyWorkers: r.worker.BusyCount(),
	}
}

// reconcileWorkersLocked 使 worker 数量向 desiredWorkerCount 收敛；调用方必须持有 mu。
func (r *CreativeWorkerRuntime) reconcileWorkersLocked() {
	if r == nil || r.cancel == nil || r.ctx == nil || r.ctx.Err() != nil || r.wg == nil {
		return
	}
	desired := r.desiredWorkerCount
	if desired <= 0 {
		desired = DefaultCreativeWorkerCount
	}
	activeCount := r.activeWorkerCountLocked()
	for activeCount < desired {
		r.nextWorkerID++
		handle := &creativeWorkerHandle{id: r.nextWorkerID, stop: make(chan struct{})}
		r.workers[handle.id] = handle
		r.wg.Add(1)
		go r.runWorkerHandle(r.ctx, handle)
		activeCount++
	}
	if activeCount <= desired {
		return
	}
	// 选择 ID 最大的 worker 缩容，保留较早启动的 worker 以减少调度抖动。
	toStop := activeCount - desired
	for id := r.nextWorkerID; toStop > 0 && id > 0; id-- {
		handle, ok := r.workers[id]
		if !ok || handle.stopping.Load() {
			continue
		}
		handle.stopping.Store(true)
		close(handle.stop)
		toStop--
	}
}

// activeWorkerCountLocked 返回尚未收到排空信号的 worker 数量；调用方必须持有 mu。
func (r *CreativeWorkerRuntime) activeWorkerCountLocked() int {
	active := 0
	for _, handle := range r.workers {
		if handle != nil && !handle.stopping.Load() {
			active++
		}
	}
	return active
}

func (r *CreativeWorkerRuntime) runWorkerHandle(ctx context.Context, handle *creativeWorkerHandle) {
	defer r.wg.Done()
	r.worker.RunUntilStopped(ctx, handle.stop)
	r.mu.Lock()
	delete(r.workers, handle.id)
	if r.cancel != nil && r.ctx != nil && r.ctx.Err() == nil {
		r.reconcileWorkersLocked()
	}
	r.mu.Unlock()
}

// Stop 保留兼容入口，应用退出使用带预算的 StopContext。
func (r *CreativeWorkerRuntime) Stop() { _ = r.StopContext(context.Background()) }

// StopContext 固定首次结果；超时不意味着在途工作已排空。
func (r *CreativeWorkerRuntime) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.stopped {
		done := r.stopDone
		r.mu.Unlock()
		select {
		case <-done:
			return r.stopErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.stopped = true
	r.stopDone = make(chan struct{})
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var err error
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			err = fmt.Errorf("creative workers remain unfinished: %w", ctx.Err())
		}
	}
	r.mu.Lock()
	r.stopErr = err
	close(r.stopDone)
	r.mu.Unlock()
	return err
}

// Running 报告 runtime 是否正在运行。
func (r *CreativeWorkerRuntime) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil && !r.stopped
}

// RuntimeWorker 是运行拥有者需要的最小任务执行接口。
type RuntimeWorker interface {
	RunUntilStopped(context.Context, <-chan struct{})
	RunDelayedMover(context.Context)
	RunStaleActiveRecovery(context.Context)
	BusyCount() int
}

// RuntimeOptions 由 app 投影静态开关、动态数量和两个恢复循环。
type RuntimeOptions struct {
	Enabled     bool
	WorkerCount func(context.Context) int
	Outbox      func(context.Context)
	Transient   func(context.Context)
}

func NewCreativeWorkerRuntime(worker RuntimeWorker, options RuntimeOptions) *CreativeWorkerRuntime {
	return &CreativeWorkerRuntime{worker: worker, options: options, desiredWorkerCount: DefaultCreativeWorkerCount}
}
