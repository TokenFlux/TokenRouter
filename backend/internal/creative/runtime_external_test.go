package creative_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// Parallel runtime fixtures coordinate task arrival and provider execution
// through channels and atomic counts. Two provider calls publish overlap once,
// then wait for the test release signal or context cancellation. Cleanup
// releases that signal before stopping the runtime, so a test assertion
// failure does not leave its simulated executions waiting indefinitely.
//
// Repository wrappers serialize their overridden fake methods under a mutex, but
// their returned ownership differs: internal GetRun and ListOutputs JSON-copy
// their results, while other lookup wrappers return borrowed fake pointers
// after unlocking. Transient output reads copy bytes and share the
// output-write mutex. The user-slot cache counts a global fixture total,
// sufficient for the same-user two-task cases; it models neither Redis
// ownership tokens nor per-user buckets.

// parallelCreativeQueue delivers fixture task identifiers to concurrent queue
// loops through one channel.
type parallelCreativeQueue struct {
	// ready is supplied by the scenario and stays open while workers run.
	// Enqueue can block on it, so the test provides buffer space or active readers.
	ready chan string // shared task arrival channel
}

// parallelCreativeRunRepo serializes overridden fake metadata methods used by
// parallel runtime scenarios.
type parallelCreativeRunRepo struct {
	// creativeFakeRunRepo retains map state while mu serializes only these
	// overrides. GetCreativeRunByRunID and ListCreativeRunOutputs copy results;
	// other read methods can expose retained pointers after the mutex is
	// released.
	*creativeFakeRunRepo            // retained in-memory task fake
	mu                   sync.Mutex // overridden fake operation mutex
}

// parallelCreativeTransient synchronizes generated output access for
// concurrent fixture settlement and reads.
type parallelCreativeTransient struct {
	// creativeFakeTransient retains byte maps while mu protects SaveOutput and
	// LoadOutput. The latter copies returned bytes before unlocking; other
	// promoted methods retain the base fake behavior and synchronization scope.
	*creativeFakeTransient            // retained temporary byte fake
	mu                     sync.Mutex // overridden fake operation mutex
}

// parallelCreativeBilling serializes fake capture observations while
// concurrent tasks complete their funds calls.
type parallelCreativeBilling struct {
	// creativeFakeBillingRepo retains counts and identifiers while mu serializes
	// the Capture override. Other promoted funds methods keep the base fake
	// synchronization scope and need their own coordination during parallel use.
	*creativeFakeBillingRepo            // retained funds observation fake
	mu                       sync.Mutex // overridden fake operation mutex
}

// parallelCreativeUserCache models one global fixture user-slot count through
// atomic admission updates.
type parallelCreativeUserCache struct {
	// creativeUserCacheFixture supplies promoted methods while active counts
	// global admissions here. The overridden slot methods ignore per-user/token
	// identity; other promoted operations retain their base fixture behavior.
	creativeUserCacheFixture              // embedded unused slot fixture behavior
	active                   atomic.Int64 // global acquired fixture slot count
}

// overlappingCreativeExecutor observes concurrent entry and waits for a shared
// test-controlled execution release.
type overlappingCreativeExecutor struct {
	// active counts calls that have entered Execute. overlapped closes once
	// through overlapOnce after concurrent entry, while release closes once
	// through releaseOnce so normal completion and test cleanup can both unblock
	// waiting executions.
	active      atomic.Int64  // concurrent simulated execution count
	overlapped  chan struct{} // two-execution overlap observed signal
	overlapOnce sync.Once     // overlap signal close guard
	release     chan struct{} // simulated provider completion gate
	releaseOnce sync.Once     // execution release close guard
}

// TestCreativeWorkerRuntimeWorkerCount inspects the constructor default and
// positive count updates before startup. Setting zero leaves the previously
// selected count of three unchanged in this fixture.
func TestCreativeWorkerRuntimeWorkerCount(t *testing.T) {
	runtime := newCreativeRuntimeFixture(nil, nil, &config.Config{})
	require.Equal(t, creative.DefaultCreativeWorkerCount, runtime.WorkerCount())

	runtime.SetWorkerCount(3)
	require.Equal(t, 3, runtime.WorkerCount())
	runtime.SetWorkerCount(0)
	require.Equal(t, 3, runtime.WorkerCount())
}

// TestCreativeWorkerRuntimeStartStopAndScale starts a two-worker fixture,
// requests a one-worker target, and stops it. Assertions check Running and
// desired counts rather than a provider task-drain interleaving.
func TestCreativeWorkerRuntimeStartStopAndScale(t *testing.T) {
	fixture := newCreativeWorkerFixture()
	runtime := newCreativeRuntimeFixture(fixture.worker, fixture.service, &config.Config{Creative: config.CreativeConfig{QueueWorkerEnabled: true}})
	runtime.SetWorkerCount(2)
	runtime.Start()
	require.Eventually(t, runtime.Running, time.Second, 10*time.Millisecond)
	require.Equal(t, 2, runtime.WorkerCount())

	runtime.SetWorkerCount(1)
	require.Equal(t, 1, runtime.WorkerCount())
	runtime.Stop()
	require.False(t, runtime.Running())
}

// TestCreativeWorkerRuntimeStatus holds two fake executions at their shared
// gate and reads runtime status. It checks running, two active handles, two
// busy calls, and zero status after release and stop.
func TestCreativeWorkerRuntimeStatus(t *testing.T) {
	stopped := newCreativeRuntimeFixture(nil, nil, &config.Config{Creative: config.CreativeConfig{QueueWorkerEnabled: true}})
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
	worker := newCreativeWorkerFixtureForSources(queue, repo, transient, executor, fixture.service, fixture.worker.Options(), scheduler.NewConcurrencyService(&parallelCreativeUserCache{}, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	))
	runtime := newCreativeRuntimeFixture(worker, fixture.service, &config.Config{Creative: config.CreativeConfig{QueueWorkerEnabled: true}})
	runtime.SetWorkerCount(2)
	runtime.Start()
	// Release the simulated provider gate before runtime shutdown on every test
	// exit, so an assertion failure cannot leave executions blocked while Stop
	// waits for the shared worker goroutines.
	t.Cleanup(func() {
		executor.allow()
		runtime.Stop()
	})

	// Submit both IDs after starting workers so their calls can reach the shared
	// provider gate while this test waits for the overlap observation.
	queue.ready <- "crun_status_1"
	queue.ready <- "crun_status_2"
	select {
	case <-executor.overlapped:
	case <-time.After(2 * time.Second):
		t.Fatal("两个创作台任务未同时进入 platform")
	}
	require.Eventually(t, func() bool {
		status := runtime.Status()
		return status.Running && status.WorkerCount == 2 && status.BusyWorkers == 2
	}, 2*time.Second, 10*time.Millisecond)

	executor.allow()
	runtime.Stop()
	require.Equal(t, creative.CreativeWorkerStatus{}, runtime.Status())
}

// TestCreativeWorkerRuntimeParallelProviderExecution starts two same-user
// tasks with two fixture slots. It observes overlapped execution, releases
// both calls, and waits for both succeeded rows before stopping the runtime.
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
	concurrency := scheduler.NewConcurrencyService(&parallelCreativeUserCache{}, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	)
	worker := newCreativeWorkerFixtureForSources(queue, repo, transient, executor, fixture.service, fixture.worker.Options(), concurrency)
	runtime := newCreativeRuntimeFixture(worker, fixture.service, &config.Config{Creative: config.CreativeConfig{QueueWorkerEnabled: true}})
	runtime.SetWorkerCount(2)
	runtime.Start()
	// Release the simulated provider gate before runtime shutdown on every test
	// exit, so an assertion failure cannot leave executions blocked while Stop
	// waits for the shared worker goroutines.
	t.Cleanup(func() {
		executor.allow()
		runtime.Stop()
	})

	// Submit both IDs after starting workers so their calls can reach the shared
	// provider gate while this test waits for the overlap observation.
	queue.ready <- "crun_parallel_1"
	queue.ready <- "crun_parallel_2"
	select {
	case <-executor.overlapped:
	case <-time.After(2 * time.Second):
		t.Fatal("两个创作台任务未同时进入 platform")
	}
	executor.allow()
	// Wait for both succeeded rows before shutdown, so runtime cancellation
	// cannot replace either simulated completion while the test checks its
	// parallel-execution outcome.
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

// TestStoppedRuntimesRejectStart stops the creative runtime before calling
// Start. The assertion requires Running to remain false, while the deferred
// second stop follows the same completed lifecycle.
func TestStoppedRuntimesRejectStart(t *testing.T) {
	t.Run("creative", func(t *testing.T) {
		q := &parallelCreativeQueue{ready: make(chan string)}
		w := newCreativeWorkerFixtureForSources(q, nil, nil, nil, nil, creative.CreativeWorkerOptions{})
		r := newCreativeRuntimeFixture(w, nil, &config.Config{Creative: config.CreativeConfig{QueueWorkerEnabled: true}})
		r.SetWorkerCount(1)
		r.Stop()
		r.Start()
		defer r.Stop()
		require.False(t, r.Running(), "停止后的创作台 runtime 不应重开")
	})
}

// newCreativeRuntimeFixture binds queue enablement from optional config and
// recovery callbacks from supplied Public.Results. It retains the worker and
// starts no goroutines until the returned runtime Start is called.
func newCreativeRuntimeFixture(worker *creative.CreativeRunWorker, public *creative.Public, cfg *config.Config) *creative.CreativeWorkerRuntime {
	options := creative.RuntimeOptions{Enabled: cfg != nil && cfg.Creative.QueueWorkerEnabled}
	if public != nil && public.Results != nil {
		options.Outbox = public.Results.RunCreativeOutboxReconciler
		options.Transient = public.Results.RunCreativeTransientReconciler
	}
	return creative.NewCreativeWorkerRuntime(worker, options)
}

// bindCreativeRepoFixture installs the same repository in Public and Results
// before runtime use. Callers configure this pair before starting parallel
// workers so both sides read the same fixture metadata.
func bindCreativeRepoFixture(public *creative.Public, repo creative.CreativeRunRepository) {
	public.Repo = repo
	public.Results.Repo = repo
}

// Enqueue sends the run identifier to the shared ready channel and returns
// success. This fixture send ignores cancellation and can block until buffer
// space or a receiving worker becomes available.
func (q *parallelCreativeQueue) Enqueue(_ context.Context, runID string) error {
	q.ready <- runID
	return nil
}

// Reserve selects one ready arrival, cancellation or timeout and returns the
// fixed test lease. Keep ready open during worker use; a closed channel can
// supply an empty run, and timeout reports ErrCreativeQueueEmpty. The timer is
// stopped when the call returns.
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

// RequeueAfter accepts every retry request and succeeds while leaving the
// channel unchanged. The supplied delay, task and token are ignored, so this
// parallel fixture exercises successful processing rather than delayed
// scheduling.
func (q *parallelCreativeQueue) RequeueAfter(context.Context, string, string, time.Duration) error {
	return nil
}

// Ack succeeds for every completion request and changes no channel bookkeeping.
// Task identity and lease token are ignored, so these success fixtures do not
// establish production queue-fencing behavior.
func (q *parallelCreativeQueue) Ack(context.Context, string, string) error { return nil }

// Heartbeat reports owned success for every task and token under any supplied
// context. It checks no active membership and updates no timestamp, keeping
// this fixture focused on parallel provider calls.
func (q *parallelCreativeQueue) Heartbeat(context.Context, string, string) (bool, error) {
	return true, nil
}

// MoveDueDelayedToReady returns zero transfers for every supplied batch limit.
// This fixture keeps no delayed entries, so the runtime mover receives empty
// batches while ready arrivals come from the channel.
func (q *parallelCreativeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

// RecoverStaleActive returns zero recoveries for every supplied age and limit.
// This fixture keeps no active timestamps, and its parallel success tests
// introduce work through direct ready-channel arrivals.
func (q *parallelCreativeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

// TryAcquireJobLock returns a fresh observation lock with acquired true for
// each call. Its independent fake handle records cleanup, and this fixture
// models no distributed lock contention or token ownership.
func (q *parallelCreativeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (creative.CreativeRunJobLock, bool, error) {
	return &creativeFakeJobLock{}, true, nil
}

// GetCreativeRunByRunID reads the base fake under mu and JSON-copies its
// returned run before unlock. Caller reads therefore use separate serializable
// metadata while the underlying task can keep advancing.
func (r *parallelCreativeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, err := r.creativeFakeRunRepo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return cloneCreativeParallelValue(v), nil
}

// MarkCreativeRunRunning serializes the base fake initial running transition
// and start/provider assignment. Already-running rows succeed unchanged; the
// mutex covers this call while later borrowed-pointer access still needs caller
// coordination.
func (r *parallelCreativeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, providerID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunRunning(ctx, runID, providerID, now)
}

// SetCreativeRunExecution serializes base fake provider/platform assignment and
// its observation count. Only positive provider values update an existing task,
// and this wrapper retains the base missing-task error result.
func (r *parallelCreativeRunRepo) SetCreativeRunExecution(ctx context.Context, runID string, providerID int64, platform string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunExecution(ctx, runID, providerID, platform, now)
}

// MarkCreativeRunSucceeded serializes fake cost and completion updates for
// supported prior states. Base fake status checks still reject cancelled or
// other unsupported rows, and the mutex protects the delegated mutation only.
func (r *parallelCreativeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunSucceeded(ctx, runID, actualCost, now)
}

// UpdateCreativeRunOutput serializes the base fake indexed delivery update.
// Acknowledged rows stay intact, empty codes retain previous text and messages
// are ignored; the supplied expiry pointer remains shared after unlock.
func (r *parallelCreativeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.UpdateCreativeRunOutput(ctx, runID, outputIndex, status, mimeType, byteSize, transientExpiresAt, errorCode, errorMessage)
}

// ListCreativeRunOutputs reads the base fake output slice under mu and
// JSON-copies it before unlock. Callers inspect separate serializable output
// values while subsequent fixture updates mutate retained metadata.
func (r *parallelCreativeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, err := r.creativeFakeRunRepo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return nil, err
	}
	return cloneCreativeParallelValue(v), nil
}

// SaveOutput serializes the base fake output-map write and retains its
// supplied byte slice. Callers keep those bytes stable; LoadOutput uses the
// same mutex and returns its own copy.
func (s *parallelCreativeTransient) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creativeFakeTransient.SaveOutput(ctx, runID, index, data, ttl)
}

// Capture serializes updates to fake capture counts and request-ID
// observations. It delegates the base fixture amount result and provides no
// database transaction or external funds deduplication itself.
func (r *parallelCreativeBilling) Capture(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeBillingRepo.Capture(ctx, cmd)
}

// AcquireUserSlot admits one global fixture slot when below a positive
// maximum, with nonpositive maxima unlimited. User and token inputs are
// ignored.
//
// A compare-and-swap loop retries after another call changes the count, so
// concurrent admission cannot both increment from the same observed value. The
// check and increment use the same observed count, while release subtracts
// independently. This is the fixture global counter used for same-user runtime
// scenarios, rather than a Redis token-owned slot implementation.
func (c *parallelCreativeUserCache) AcquireUserSlot(_ context.Context, _ int64, maxConcurrency int, _ string) (bool, error) {
	for {
		// Compare and increment from the same observation. A losing concurrent
		// admission retries the new count before checking the shared
		// fixture limit,
		// so it cannot publish a stale below-limit decision.
		current := c.active.Load()
		if maxConcurrency > 0 && current >= int64(maxConcurrency) {
			return false, nil
		}
		if c.active.CompareAndSwap(current, current+1) {
			return true, nil
		}
	}
}

// ReleaseUserSlot subtracts one global fixture slot regardless of user or
// token. Callers release acquired slots once through scheduler lease cleanup;
// this counter operation itself has no duplicate-release or zero-floor guard.
func (c *parallelCreativeUserCache) ReleaseUserSlot(context.Context, int64, string) error {
	c.active.Add(-1)
	return nil
}

// Prepare returns a provider-fifty-five target that delegates to this
// overlapping executor. Platform stays unset and the upstream model comes from
// the supplied run; its release callback performs no provider-capacity cleanup.
func (e *overlappingCreativeExecutor) Prepare(_ context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	return &creative.CreativeExecution{
		ProviderID: 55,
		Target: creativeFixtureTarget(func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
			return e.Execute(ctx, run, payload, nil)
		}),
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

// Execute increments the active fixture count and publishes overlap when at
// least two calls have entered. It returns image bytes after release or the
// context error after cancellation.
//
// The overlap signal is closed through sync.Once so simultaneous
// second-and-later entrants cannot close it twice. Every return path subtracts
// the active count. Callers initialize both channels before execution. The
// test owns release and its channels, while allow uses a
// separate once guard so assertion cleanup and normal release can both unblock
// any waiting executions.
func (e *overlappingCreativeExecutor) Execute(ctx context.Context, _ creative.CreativeRun, _ creative.CreativeRunPayload, _ *creative.CreativeExecution) (*creative.CreativeExecuteResult, error) {
	// Publish overlap after active admission, then hold both calls at the same
	// release gate. The once guard prevents another simultaneous entrant from
	// closing the signal that the test is already observing.
	if e.active.Add(1) >= 2 {
		e.overlapOnce.Do(func() { close(e.overlapped) })
	}
	// Context cancellation and the test release signal compete for this fixture
	// return. Both paths decrement active before returning, letting runtime status
	// and test cleanup observe completed simulated work.
	select {
	case <-e.release:
	case <-ctx.Done():
		e.active.Add(-1)
		return nil, ctx.Err()
	}
	e.active.Add(-1)
	return &creative.CreativeExecuteResult{
		Outputs:    []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}},
		ProviderID: 55,
	}, nil
}

// IsRetryable delegates creative error classification for failures returned by
// the overlap fixture.
func (e *overlappingCreativeExecutor) IsRetryable(err error) bool {
	return creative.IsRetryableCreativeError(err)
}

// allow closes the shared execution-release channel once for normal test
// progress or cleanup. Its once guard permits repeated calls after the waiting
// executions have already been allowed to finish.
func (e *overlappingCreativeExecutor) allow() {
	e.releaseOnce.Do(func() { close(e.release) })
}

// CreateCreativeRun serializes base fake task/output creation and returns its
// retained run pointer. The lock protects that operation only; callers keep
// the borrowed pointer immutable after return or coordinate later reads.
func (r *parallelCreativeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.CreateCreativeRun(ctx, params)
}

// GetCreativeRunByRunIDForOwner serializes the base scope check and returns its
// borrowed run pointer. After unlock, callers coordinate access with task
// writers or use the JSON-copied internal lookup for separate metadata.
func (r *parallelCreativeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
}

// GetCreativeRunByIdempotencyKey serializes the base workspace/key lookup and
// returns its borrowed run pointer. Callers reading that retained row after
// unlock coordinate with writers or use the copied internal lookup.
func (r *parallelCreativeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunByIdempotencyKey(ctx, scope, key)
}

// ListCreativeRunsForOwner serializes map iteration and returns a new slice of
// retained task pointers. The base fake ignores status and pagination filters;
// after unlock, callers coordinate reads of those shared rows.
func (r *parallelCreativeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.ListCreativeRunsForOwner(ctx, scope, filter)
}

// TransitionCreativeRunStatus serializes the base fake state check and row
// mutation. Invalid transitions remain errors, and supplied error/time pointers
// retained on the row stay shared with the caller after unlock.
func (r *parallelCreativeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.TransitionCreativeRunStatus(ctx, runID, toStatus, opts)
}

// GetCreativeRunOutput serializes the base indexed lookup and returns its
// borrowed output pointer. Callers coordinate later access with writers or use
// ListCreativeRunOutputs for JSON-copied fixture metadata.
func (r *parallelCreativeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.GetCreativeRunOutput(ctx, runID, outputIndex)
}

// MarkCreativeRunOutputAcked serializes base fake receipt status and timestamp
// assignment. Only succeeded outputs pass that fake check; public
// acknowledgment handles a repeat before this method is invoked again.
func (r *parallelCreativeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunOutputAcked(ctx, runID, outputIndex, now)
}

// ListCreativeRunsDueForTransientCleanup serializes the base fake empty cleanup
// result. Its cutoff and limit select no rows, so this fixture supplies no
// transient cleanup tasks during runtime success tests.
func (r *parallelCreativeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.ListCreativeRunsDueForTransientCleanup(ctx, cutoff, limit)
}

// IncrementCreativeRunAttempt serializes one base fake execution-counter
// increment and its returned value. The count is observed under the same
// wrapper mutex; missing rows retain the fake task-not-found error.
func (r *parallelCreativeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunAttempt(ctx, runID)
}

// IncrementCreativeRunSettlementAttempt serializes one base fake
// settlement-counter increment and its returned value. The count is observed
// under the same wrapper mutex; missing rows retain the fake task-not-found
// error.
func (r *parallelCreativeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunSettlementAttempt(ctx, runID)
}

// IncrementCreativeRunReleaseAttempt serializes one base fake release-counter
// increment and its returned value. The count is observed under the same
// wrapper mutex; missing rows retain the fake task-not-found error.
func (r *parallelCreativeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.IncrementCreativeRunReleaseAttempt(ctx, runID)
}

// SetCreativeRunProvisioningPhase serializes phase assignment on the retained
// fake task. Supplied spelling is stored directly, including empty values, and
// a missing row returns the base task-not-found error.
func (r *parallelCreativeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunProvisioningPhase(ctx, runID, phase)
}

// MarkCreativeRunProviderSucceeded serializes base fake work timing and
// optional provider identity assignment. Running rows advance to
// provider-succeeded, while other existing states retain status with the
// recorded fact.
func (r *parallelCreativeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, providerID int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.MarkCreativeRunProviderSucceeded(ctx, runID, providerID, now)
}

// SetCreativeRunReconcileError serializes task recovery diagnostics and
// next-time updates. Empty messages clear the error and zero times clear the
// hint, while other values remain on the retained row.
func (r *parallelCreativeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.SetCreativeRunReconcileError(ctx, runID, message, next)
}

// cloneCreativeParallelValue JSON-round-trips fixture data into T and panics
// on either codec failure. Use it for the serializable run/output values
// exercised here; JSON-compatible copies can omit unexported data or alter
// interface value representations.
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

// RecordProviderOutcome serializes the base fake ordered output and
// provider-fact mutations. An error can leave earlier fake mutations present;
// this mutex wrapper supplies no rollback equivalent to the SQL outcome
// transaction.
func (r *parallelCreativeRunRepo) RecordProviderOutcome(ctx context.Context, id string, providerID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.RecordProviderOutcome(ctx, id, providerID, outputs, now)
}

// CompleteProviderOutcome serializes the base fake success check, completion
// metadata and lost-result choice. Cancelled rows fail the delegated initial
// check, so this wrapper retains the invalid-transition result handled by
// Results.
func (r *parallelCreativeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creativeFakeRunRepo.CompleteProviderOutcome(ctx, id, cost, lost, now)
}

// LoadOutput reads the base fake output under the SaveOutput mutex and copies
// returned bytes before unlock. Callers own that copied slice, while the
// returned error remains the base fake lookup result.
func (s *parallelCreativeTransient) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.creativeFakeTransient.LoadOutput(ctx, runID, index)
	return append([]byte(nil), data...), err
}
