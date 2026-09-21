//go:build unit

package creative_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

// creativeFakeExecutor 是 CreativeRunExecutor 的测试替身。
type creativeFakeExecutor struct {
	result         *creative.CreativeExecuteResult
	err            error
	execPrepareErr error
	// onExecute 可在执行期间修改仓储状态（模拟状态竞争）。
	onExecute func(runID string)
	calls     int
}

func (e *creativeFakeExecutor) Prepare(ctx context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	if e.execPrepareErr != nil {
		return nil, e.execPrepareErr
	}
	return &creative.CreativeExecution{
		AccountID: 55,
		Target: creativeFixtureTarget(func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
			return e.Execute(ctx, run, payload, nil)
		}),
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

func (e *creativeFakeExecutor) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, execution *creative.CreativeExecution) (*creative.CreativeExecuteResult, error) {
	e.calls++
	if e.onExecute != nil {
		e.onExecute(run.RunID)
	}
	return e.result, e.err
}

func (e *creativeFakeExecutor) IsRetryable(err error) bool {
	return creative.IsRetryableCreativeError(err)
}

// creativeWorkerFixture 组装 worker 测试夹具。
type creativeWorkerFixture struct {
	worker  *creative.CreativeRunWorker
	service *creative.Public
	repo    *creativeFakeRunRepo
	store   *creativeFakeTransient
	billing *creativeFakeBillingRepo
	queue   *creativeFakeQueue
	exec    *creativeFakeExecutor
}

func newCreativeWorkerFixture() *creativeWorkerFixture {
	svc := newCreativeTestService()
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)
	billing := testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc))
	queue := &creativeFakeQueue{}
	exec := &creativeFakeExecutor{}
	opts := creative.CreativeWorkerOptions{
		ReserveBlockTimeout: time.Millisecond,
		JobLockTTL:          time.Minute,
		LockConflictDelay:   time.Millisecond,
		DefaultRequeueDelay: time.Millisecond,
		ErrorRetryDelay:     time.Millisecond,
		ErrorBackoff:        time.Millisecond,
		DelayedPollInterval: time.Millisecond,
		RecoveryInterval:    time.Millisecond,
		StaleActiveAfter:    time.Minute,
		DelayedMoveLimit:    10,
		RecoverLimit:        10,
		MaxAttempts:         3,
	}
	return &creativeWorkerFixture{
		worker:  newCreativeWorkerFixtureForSources(queue, repo, store, exec, svc, opts),
		service: svc,
		repo:    repo,
		store:   store,
		billing: billing,
		queue:   queue,
		exec:    exec,
	}
}

// seedCreativeRun 种入一个 queued 任务及其暂存载荷。
func seedCreativeRun(f *creativeWorkerFixture, runID string, withPayload bool) {
	hold := 0.02
	f.repo.runs[runID] = &creative.CreativeRun{
		RunID:                runID,
		UserID:               7,
		GroupID:              12,
		APIKeyID:             900,
		Model:                "gemini-3.1-flash-image",
		Operation:            creative.CreativeOperationGenerate,
		RequestedOutputCount: 1,
		Status:               creative.CreativeRunStatusQueued,
		EstimatedCost:        0.02,
		HoldAmount:           &hold,
	}
	f.repo.outputs[runID] = []*creative.CreativeRunOutput{
		{RunID: runID, OutputIndex: 0, Status: creative.CreativeRunOutputStatusPending},
	}
	if withPayload {
		f.store.payloads[runID] = &creative.CreativeRunPayload{RunID: runID, Prompt: "p"}
	}
}

func TestCreativeWorkerSuccessPath(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workersuccess01"
	seedCreativeRun(f, runID, true)
	f.exec.result = &creative.CreativeExecuteResult{
		Outputs:   []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}},
		AccountID: 55,
	}

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)

	run := f.repo.runs[runID]
	require.Equal(t, creative.CreativeRunStatusSucceeded, run.Status)
	require.NotNil(t, run.AccountID)
	require.Equal(t, int64(55), *run.AccountID)
	require.Equal(t, 1, f.repo.setAccountN)
	require.NotNil(t, run.ActualCost)
	// 输出行进入 succeeded，临时输出已保存。
	output := f.repo.outputs[runID][0]
	require.Equal(t, creative.CreativeRunOutputStatusSucceeded, output.Status)
	require.NotNil(t, output.TransientExpiresAt)
	data, err := f.store.LoadOutput(context.Background(), runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("img"), data)
	// 计费：预占发生在创建阶段（worker 测试直接种入任务，故为 0），worker 只做捕获。
	require.Equal(t, 0, f.billing.reserveN)
	require.Equal(t, 1, f.billing.captureN)
	require.Equal(t, 0, f.billing.releaseN)
}

// TestCreativeWorkerRecoversPersistedProviderOutput 验证输出元数据已落库但状态写入失败时不重复调用 provider。
func TestCreativeWorkerRecoversPersistedProviderOutput(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workerrecover01"
	seedCreativeRun(f, runID, false)
	run := f.repo.runs[runID]
	run.Status = creative.CreativeRunStatusRunning
	accountID := int64(55)
	run.AccountID = &accountID
	f.repo.outputs[runID][0].Status = creative.CreativeRunOutputStatusSucceeded
	f.store.outputs[runID+":0"] = []byte("img")

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, 0, f.exec.calls)
	require.Equal(t, creative.CreativeRunStatusSucceeded, f.repo.runs[runID].Status)
	require.Equal(t, 1, f.billing.captureN)
}

// TestCreativeWorkerUserConcurrencyPending 验证用户槽位未获取时任务保持 queued 并释放 worker。
func TestCreativeWorkerUserConcurrencyPending(t *testing.T) {
	f := newCreativeWorkerFixture()
	seedCreativeRun(f, "crun_worker_user_pending", true)
	userRepo := testassert.MustType[*creativeFakeUserRepo](testassert.MustType[creativeUserReader](f.service.UserRepo).source)
	userRepo.user.Concurrency = 1
	cache := &creativeUserCacheFixture{acquireResult: false}
	f.worker = newCreativeWorkerFixtureForSources(f.queue, f.repo, f.store, f.exec, f.service, f.worker.Options(), scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event,
	},
	))

	result, err := f.worker.Process(context.Background(), "crun_worker_user_pending")
	require.NoError(t, err)
	require.False(t, result.Terminal)
	require.Equal(t, creative.DefaultCreativeConcurrencyRequeueDelay, result.RequeueAfter)
	require.Equal(t, creative.CreativeRunStatusQueued, f.repo.runs["crun_worker_user_pending"].Status)
	require.Equal(t, 0, f.exec.calls)
}

// TestCreativeWorkerAccountConcurrencyPending 验证执行器报告账号槽位不足时不推进任务状态。
func TestCreativeWorkerAccountConcurrencyPending(t *testing.T) {
	f := newCreativeWorkerFixture()
	seedCreativeRun(f, "crun_worker_account_pending", true)
	f.exec.execPrepareErr = creative.ErrCreativeExecutionPending

	result, err := f.worker.Process(context.Background(), "crun_worker_account_pending")
	require.NoError(t, err)
	require.False(t, result.Terminal)
	require.Equal(t, creative.DefaultCreativeConcurrencyRequeueDelay, result.RequeueAfter)
	require.Equal(t, creative.CreativeRunStatusQueued, f.repo.runs["crun_worker_account_pending"].Status)
}

// 保存失败后保持成功事实和一次费用，不再次调用供应商。
func TestCreativeWorkerRetriesTransientOutputFailure(t *testing.T) {
	f := newCreativeWorkerFixture()
	id := "crun_workeroutputretry1"
	seedCreativeRun(f, id, true)
	f.store.saveOutputErr = errors.New("redis unavailable")
	f.exec.result = &creative.CreativeExecuteResult{Outputs: []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}}, AccountID: 55}
	result, err := f.worker.Process(context.Background(), id)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, creative.CreativeRunStatusResultLost, f.repo.runs[id].Status)
	require.NotNil(t, f.repo.runs[id].ProviderResultRecordedAt)
	require.Equal(t, 1, f.billing.captureN)
	require.Zero(t, f.billing.releaseN)
	f.store.saveOutputErr = nil
	result, err = f.worker.Process(context.Background(), id)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, 1, f.exec.calls)
	require.Equal(t, 1, f.billing.captureN)
}

func TestCreativeWorkerRetryableError(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workerretry0001"
	seedCreativeRun(f, runID, true)
	f.exec.err = creative.CreativeHTTPStatusError(503, "upstream busy")

	// 第一次与第二次处理：attempt 递增并重排。
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := f.worker.Process(context.Background(), runID)
		require.NoError(t, err)
		require.False(t, result.Terminal)
		require.Greater(t, result.RequeueAfter, time.Duration(0))
		require.Equal(t, attempt, f.repo.runs[runID].AttemptCount)
	}
	require.Equal(t, 2, f.exec.calls)

	// 第三次：attempt 达到上限 → FailRun 出队并释放预占。
	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	run := f.repo.runs[runID]
	require.Equal(t, creative.CreativeRunStatusFailed, run.Status)
	require.NotNil(t, run.ErrorCode)
	require.Equal(t, 3, run.AttemptCount)
	require.Equal(t, 1, f.billing.releaseN)
}

func TestCreativeWorkerNonRetryableError(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workernonretry01"
	seedCreativeRun(f, runID, true)
	f.exec.err = creative.CreativeNonRetryableError("grok platform does not support creative operation edit")

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, 1, f.exec.calls)
	// 不可重试错误不消耗 attempt，直接失败并释放。
	run := f.repo.runs[runID]
	require.Equal(t, creative.CreativeRunStatusFailed, run.Status)
	require.Equal(t, 0, run.AttemptCount)
	require.Equal(t, 1, f.billing.releaseN)
}

// TestCreativeWorkerCancelRace 校验执行期间进入 cancelled：仍捕获计费，但保留该终态。
func TestCreativeWorkerCancelRace(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workercancel001"
	seedCreativeRun(f, runID, true)
	f.exec.result = &creative.CreativeExecuteResult{
		Outputs:   []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}},
		AccountID: 55,
	}
	// 模拟执行期间进入 cancelled：execute 返回前把任务置为 cancelled。
	f.exec.onExecute = func(runID string) {
		run := f.repo.runs[runID]
		run.Status = creative.CreativeRunStatusCancelled
	}

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)

	run := f.repo.runs[runID]
	require.Equal(t, creative.CreativeRunStatusCancelled, run.Status)
	// provider 已确认成功：仍完成捕获与用量记录，不回写 succeeded。
	require.Equal(t, 1, f.billing.captureN)
	require.Equal(t, 0, f.billing.releaseN)
	require.NotNil(t, run.ActualCost)
	output := f.repo.outputs[runID][0]
	require.Equal(t, creative.CreativeRunOutputStatusSucceeded, output.Status)
}

// TestCreativeWorkerPayloadLost 校验载荷过期：result_lost 且未执行的预占被释放。
func TestCreativeWorkerPayloadLost(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workerlost00001"
	seedCreativeRun(f, runID, false)

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, 0, f.exec.calls)

	run := f.repo.runs[runID]
	require.Equal(t, creative.CreativeRunStatusResultLost, run.Status)
	// provider 未执行：释放预占。
	require.Equal(t, 1, f.billing.releaseN)
	require.Equal(t, 0, f.billing.captureN)
}

// TestCreativeWorkerCancelBeforeExecute 校验执行前发现已 cancelled：不调用上游，直接清理出队。
func TestCreativeWorkerCancelBeforeExecute(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workerprecancel1"
	seedCreativeRun(f, runID, true)
	f.repo.runs[runID].Status = creative.CreativeRunStatusCancelled

	result, err := f.worker.Process(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, 0, f.exec.calls)
	// 任务在入队前已释放预占：worker 只幂等清理，不重复释放。
	require.Equal(t, 0, f.billing.releaseN)
}

// TestCreativeWorkerRunOnceAck 校验完整 RunOnce 链路：Reserve → 锁 → 处理 → Ack。
func TestCreativeWorkerRunOnceAck(t *testing.T) {
	f := newCreativeWorkerFixture()
	runID := "crun_workerrunonce001"
	seedCreativeRun(f, runID, true)
	f.queue.reserveBatch = []string{runID}
	f.exec.result = &creative.CreativeExecuteResult{
		Outputs:   []creative.CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}},
		AccountID: 55,
	}

	require.NoError(t, f.worker.RunOnce(context.Background()))
	require.Equal(t, []string{runID}, f.queue.acked)
	require.Empty(t, f.queue.requeued)
	require.NotNil(t, f.queue.lastLock)
	require.True(t, f.queue.lastLock.released)
	require.Equal(t, creative.CreativeRunStatusSucceeded, f.repo.runs[runID].Status)
}
