//go:build unit

package creative_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

// TestCreativeWorkerRuntimeWorkerCount 验证 worker 数量默认值与正数热更新边界。
func TestCreativeWorkerRuntimeWorkerCount(t *testing.T) {
	runtime := newCreativeRuntimeFixture(nil, nil, &config.Config{})
	require.Equal(t, creative.DefaultCreativeWorkerCount, runtime.WorkerCount())

	runtime.SetWorkerCount(3)
	require.Equal(t, 3, runtime.WorkerCount())
	runtime.SetWorkerCount(0)
	require.Equal(t, 3, runtime.WorkerCount())
}

// TestCreativeWorkerRuntimeStartStopAndScale 验证任务 worker 池可启动、缩容并完整停止。
func TestCreativeWorkerRuntimeStartStopAndScale(t *testing.T) {
	fixture := newCreativeWorkerFixture()
	runtime := newCreativeRuntimeFixture(fixture.worker, fixture.service, &config.Config{
		Creative: config.CreativeConfig{QueueEnabled: true},
	})
	runtime.SetWorkerCount(2)
	runtime.Start()
	require.Eventually(t, runtime.Running, time.Second, 10*time.Millisecond)
	require.Equal(t, 2, runtime.WorkerCount())

	runtime.SetWorkerCount(1)
	require.Equal(t, 1, runtime.WorkerCount())
	runtime.Stop()
	require.False(t, runtime.Running())
}

// TestCreativeWorkerRuntimeStatus 验证状态快照如实反映运行状态、池规模与忙碌 worker 数量。
func TestCreativeWorkerRuntimeStatus(t *testing.T) {
	stopped := newCreativeRuntimeFixture(nil, nil, &config.Config{
		Creative: config.CreativeConfig{QueueEnabled: true},
	})
	require.Equal(t, creative.CreativeWorkerStatus{}, stopped.Status())

	fixture := newCreativeWorkerFixture()
	seedCreativeRun(fixture, "crun_status_1", true)
	seedCreativeRun(fixture, "crun_status_2", true)
	testassert.MustType[*creativeFakeUserRepo](testassert.MustType[creativeUserReader](fixture.service.UserRepo).source).user.Concurrency = 2

	repo := &parallelCreativeRunRepo{creativeFakeRunRepo: fixture.repo}
	transient := &parallelCreativeTransient{creativeFakeTransient: fixture.store}
	billing := &parallelCreativeBilling{creativeFakeBillingRepo: fixture.billing}
	bindCreativeRepoFixture(fixture.service, repo)
	bindCreativeTransientFixture(fixture.service, transient)
	fixture.service.Results.Funding = creativeFundingProjection(billing)
	queue := &parallelCreativeQueue{ready: make(chan string, 2)}
	executor := &overlappingCreativeExecutor{
		overlapped: make(chan struct{}),
		release:    make(chan struct{}),
	}
	worker := newCreativeWorkerFixtureForSources(queue, repo, transient, executor, fixture.service, fixture.worker.Options(), scheduler.NewConcurrencyService(&parallelCreativeUserCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event},
	))
	runtime := newCreativeRuntimeFixture(worker, fixture.service, &config.Config{
		Creative: config.CreativeConfig{QueueEnabled: true},
	})
	runtime.SetWorkerCount(2)
	runtime.Start()
	t.Cleanup(func() {
		executor.allow()
		runtime.Stop()
	})

	queue.ready <- "crun_status_1"
	queue.ready <- "crun_status_2"
	select {
	case <-executor.overlapped:
	case <-time.After(2 * time.Second):
		t.Fatal("两个创作台任务未同时进入 provider")
	}
	require.Eventually(t, func() bool {
		status := runtime.Status()
		return status.Running && status.WorkerCount == 2 && status.BusyWorkers == 2
	}, 2*time.Second, 10*time.Millisecond)

	executor.allow()
	runtime.Stop()
	require.Equal(t, creative.CreativeWorkerStatus{}, runtime.Status())
}

// parallelCreativeQueue 是只用于并行验收的内存队列，保留真实 worker 的 Reserve/锁/确认路径。
type parallelCreativeQueue struct {
	ready chan string
}

func (q *parallelCreativeQueue) Enqueue(_ context.Context, runID string) error {
	q.ready <- runID
	return nil
}

func (q *parallelCreativeQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (creative.ReservedCreativeRun, error) {
	timer := time.NewTimer(blockTimeout)
	defer timer.Stop()
	select {
	case runID := <-q.ready:
		return creative.ReservedCreativeRun{RunID: runID, LeaseToken: "test-lease"}, nil
	case <-ctx.Done():
		return creative.ReservedCreativeRun{}, ctx.Err()
	case <-timer.C:
		return creative.ReservedCreativeRun{}, creative.ErrCreativeQueueEmpty
	}
}

func (q *parallelCreativeQueue) RequeueAfter(context.Context, string, string, time.Duration) error {
	return nil
}
func (q *parallelCreativeQueue) Ack(context.Context, string, string) error { return nil }
func (q *parallelCreativeQueue) Heartbeat(context.Context, string, string) (bool, error) {
	return true, nil
}
func (q *parallelCreativeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}
func (q *parallelCreativeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}
func (q *parallelCreativeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (creative.CreativeRunJobLock, bool, error) {
	return &creativeFakeJobLock{}, true, nil
}

// parallelCreativeRunRepo 为并行测试保护 fake 仓储的 map 访问。
type parallelCreativeRunRepo struct {
	*creativeFakeRunRepo
	mu sync.Mutex
}

func (r *parallelCreativeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, err := r.creativeFakeRunRepo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return cloneCreativeParallelValue(v), nil
}

func (r *parallelCreativeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, accountID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunRunning(ctx, runID, accountID, now)
}

func (r *parallelCreativeRunRepo) SetCreativeRunAccountID(ctx context.Context, runID string, accountID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunAccountID(ctx, runID, accountID, now)
}

func (r *parallelCreativeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunSucceeded(ctx, runID, actualCost, now)
}

func (r *parallelCreativeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.UpdateCreativeRunOutput(ctx, runID, outputIndex, status, mimeType, byteSize, transientExpiresAt, errorCode, errorMessage)
}

func (r *parallelCreativeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, err := r.creativeFakeRunRepo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return nil, err
	}
	return cloneCreativeParallelValue(v), nil
}

// parallelCreativeTransient 保护并行结算写入的输出 map。
type parallelCreativeTransient struct {
	*creativeFakeTransient
	mu sync.Mutex
}

func (s *parallelCreativeTransient) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creativeFakeTransient.SaveOutput(ctx, runID, index, data, ttl)
}

// parallelCreativeBilling 保护并行结算更新的 fake 计数器。
type parallelCreativeBilling struct {
	*creativeFakeBillingRepo
	mu sync.Mutex
}

func (r *parallelCreativeBilling) Capture(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeBillingRepo.Capture(ctx, cmd)
}

// parallelCreativeUserCache 以原子计数模拟用户并发槽位。
type parallelCreativeUserCache struct {
	creativeUserCacheFixture
	active atomic.Int64
}

func (c *parallelCreativeUserCache) AcquireUserSlot(_ context.Context, _ int64, maxConcurrency int, _ string) (bool, error) {
	for {
		current := c.active.Load()
		if maxConcurrency > 0 && current >= int64(maxConcurrency) {
			return false, nil
		}
		if c.active.CompareAndSwap(current, current+1) {
			return true, nil
		}
	}
}

func (c *parallelCreativeUserCache) ReleaseUserSlot(context.Context, int64, string) error {
	c.active.Add(-1)
	return nil
}

// overlappingCreativeExecutor 在两个 provider 调用同时进入时通知测试，然后等待统一放行。
type overlappingCreativeExecutor struct {
	active      atomic.Int64
	overlapped  chan struct{}
	overlapOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
}

func (e *overlappingCreativeExecutor) Prepare(_ context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	return &creative.CreativeExecution{
		AccountID: 55,
		Target: creativeFixtureTarget(func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
			return e.Execute(ctx, run, payload, nil)
		}),
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

func (e *overlappingCreativeExecutor) Execute(ctx context.Context, _ creative.CreativeRun, _ creative.CreativeRunPayload, _ *creative.CreativeExecution) (*creative.CreativeExecuteResult, error) {
	if e.active.Add(1) >= 2 {
		e.overlapOnce.Do(func() { close(e.overlapped) })
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		e.active.Add(-1)
		return nil, ctx.Err()
	}
	e.active.Add(-1)
	return &creative.CreativeExecuteResult{
		Outputs:   []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}},
		AccountID: 55,
	}, nil
}

func (e *overlappingCreativeExecutor) IsRetryable(err error) bool {
	return creative.IsRetryableCreativeError(err)
}

func (e *overlappingCreativeExecutor) allow() {
	e.releaseOnce.Do(func() { close(e.release) })
}

// TestCreativeWorkerRuntimeParallelProviderExecution 验证两个 worker 可让同一用户的两个任务并行进入 provider。
func TestCreativeWorkerRuntimeParallelProviderExecution(t *testing.T) {
	fixture := newCreativeWorkerFixture()
	seedCreativeRun(fixture, "crun_parallel_1", true)
	seedCreativeRun(fixture, "crun_parallel_2", true)
	testassert.MustType[*creativeFakeUserRepo](testassert.MustType[creativeUserReader](fixture.service.UserRepo).source).user.Concurrency = 2

	repo := &parallelCreativeRunRepo{creativeFakeRunRepo: fixture.repo}
	transient := &parallelCreativeTransient{creativeFakeTransient: fixture.store}
	billing := &parallelCreativeBilling{creativeFakeBillingRepo: fixture.billing}
	bindCreativeRepoFixture(fixture.service, repo)
	bindCreativeTransientFixture(fixture.service, transient)
	fixture.service.Results.Funding = creativeFundingProjection(billing)
	queue := &parallelCreativeQueue{ready: make(chan string, 2)}
	executor := &overlappingCreativeExecutor{
		overlapped: make(chan struct{}),
		release:    make(chan struct{}),
	}
	concurrency := scheduler.NewConcurrencyService(&parallelCreativeUserCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event},
	)
	worker := newCreativeWorkerFixtureForSources(queue, repo, transient, executor, fixture.service, fixture.worker.Options(), concurrency)
	runtime := newCreativeRuntimeFixture(worker, fixture.service, &config.Config{Creative: config.CreativeConfig{QueueEnabled: true}})
	runtime.SetWorkerCount(2)
	runtime.Start()
	t.Cleanup(func() {
		executor.allow()
		runtime.Stop()
	})

	queue.ready <- "crun_parallel_1"
	queue.ready <- "crun_parallel_2"
	select {
	case <-executor.overlapped:
	case <-time.After(2 * time.Second):
		t.Fatal("两个创作台任务未同时进入 provider")
	}
	executor.allow()
	// 并行成功契约等待两条链完成，再单独验证停止；停止取消由专门回归覆盖。
	require.Eventually(t, func() bool {
		first, e1 := repo.GetCreativeRunByRunID(context.Background(), "crun_parallel_1")
		second, e2 := repo.GetCreativeRunByRunID(context.Background(), "crun_parallel_2")
		return e1 == nil && e2 == nil && first.Status == creative.CreativeRunStatusSucceeded && second.Status == creative.CreativeRunStatusSucceeded
	}, 2*time.Second, time.Millisecond)
	runtime.Stop()

	first, err := repo.GetCreativeRunByRunID(context.Background(), "crun_parallel_1")
	require.NoError(t, err)
	second, err := repo.GetCreativeRunByRunID(context.Background(), "crun_parallel_2")
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusSucceeded, first.Status)
	require.Equal(t, creative.CreativeRunStatusSucceeded, second.Status)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.CreateCreativeRun(ctx, params)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunByIdempotencyKey(ctx, scope, key)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.ListCreativeRunsForOwner(ctx, scope, filter)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.TransitionCreativeRunStatus(ctx, runID, toStatus, opts)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunOutput(ctx, runID, outputIndex)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunOutputAcked(ctx, runID, outputIndex, now)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.ListCreativeRunsDueForTransientCleanup(ctx, cutoff, limit)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunAttempt(ctx, runID)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunSettlementAttempt(ctx, runID)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunReleaseAttempt(ctx, runID)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunProvisioningPhase(ctx, runID, phase)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, accountID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunProviderSucceeded(ctx, runID, accountID, now)
}

// 并行替身的所有仓储入口共用同一把锁。
func (r *parallelCreativeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunReconcileError(ctx, runID, message, next)
}

// 测试读取模拟真实仓储的独立查询结果，避免锁外读写同一可变实体。
func cloneCreativeParallelValue[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	return result
}

// 新增闭合操作也必须参与并行替身的同一同步边界。
func (r *parallelCreativeRunRepo) RecordProviderOutcome(ctx context.Context, id string, accountID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.RecordProviderOutcome(ctx, id, accountID, outputs, now)
}
func (r *parallelCreativeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.CompleteProviderOutcome(ctx, id, cost, lost, now)
}

// B05 的交付确认增加输出读取，读取必须与并行替身的保存共用屏障。
func (s *parallelCreativeTransient) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.creativeFakeTransient.LoadOutput(ctx, runID, index)
	return append([]byte(nil), data...), err
}
