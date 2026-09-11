package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
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
	worker         *CreativeRunWorker
	cfg            *config.Config
	settingService *SettingService

	mu                 sync.Mutex
	cancel             context.CancelFunc
	ctx                context.Context
	done               chan struct{}
	wg                 *sync.WaitGroup
	workers            map[uint64]*creativeWorkerHandle
	nextWorkerID       uint64
	desiredWorkerCount int
}

// NewCreativeWorkerRuntime 创建创作台 worker runtime（不自动启动）。
func NewCreativeWorkerRuntime(worker *CreativeRunWorker, cfg *config.Config, settingServices ...*SettingService) *CreativeWorkerRuntime {
	var settingService *SettingService
	if len(settingServices) > 0 {
		settingService = settingServices[0]
	}
	return &CreativeWorkerRuntime{
		worker:             worker,
		cfg:                cfg,
		settingService:     settingService,
		desiredWorkerCount: DefaultCreativeWorkerCount,
	}
}

// ProvideCreativeWorkerRuntime 组装创作台 worker 并启动 runtime。
func ProvideCreativeWorkerRuntime(
	repo CreativeRunRepository,
	store CreativeTransientStore,
	queue CreativeRunQueue,
	executor CreativeRunExecutor,
	creativeService *CreativePublicService,
	concurrencyService *ConcurrencyService,
	settingService *SettingService,
	cfg *config.Config,
) *CreativeWorkerRuntime {
	worker := NewCreativeRunWorker(queue, repo, store, executor, creativeService, NewCreativeWorkerOptionsFromConfig(cfg), concurrencyService)
	runtime := NewCreativeWorkerRuntime(worker, cfg, settingService)
	return runtime
}

// Start 启动任务 worker 池、delayed mover 与 stale recovery；重复调用幂等。
func (r *CreativeWorkerRuntime) Start() {
	if r == nil || r.worker == nil || r.cfg == nil || !r.cfg.Creative.QueueEnabled {
		return
	}

	desired := r.desiredWorkerCountValue()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
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
		if r.worker.service != nil {
			r.worker.service.RunCreativeOutboxReconciler(ctx)
		}
	}()
	go func() {
		defer r.wg.Done()
		if r.worker.service != nil {
			r.worker.service.RunCreativeTransientReconciler(ctx)
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
	if r != nil && r.settingService != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		settings, err := r.settingService.GetAllSettings(ctx)
		cancel()
		if err == nil && settings != nil && settings.CreativeWorkerCount > 0 {
			return settings.CreativeWorkerCount
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
	running := r.cancel != nil
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

// Stop 停止 runtime；重复调用幂等。
func (r *CreativeWorkerRuntime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	r.mu.Lock()
	if r.done == done {
		r.cancel = nil
		r.ctx = nil
		r.done = nil
		r.wg = nil
		r.workers = nil
	}
	r.mu.Unlock()
}

// Running 报告 runtime 是否正在运行。
func (r *CreativeWorkerRuntime) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil
}
