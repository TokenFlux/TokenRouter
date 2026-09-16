package service

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"go.uber.org/zap"
)

type CreativeOutput = upstream.ImageOutput

// CreativeExecuteResult 是任务执行结果，由 CreativeRunExecutor 返回。
type CreativeExecuteResult struct {
	Outputs      []CreativeOutput
	AccountID    int64
	ProviderCost float64
}

// CreativeExecution 是一次已经完成账号调度与账号槽位预占的执行上下文。
// worker 在标记任务 running 前创建它，确保并发未准入的任务仍保持 queued。
type CreativeExecution struct {
	Account       *Account
	UpstreamModel string
	Selection     *AccountSelectionResult
	ReleaseFunc   func()
}

// CreativeRunExecutor 抽象创作台任务的上游执行能力。
// 由按平台分派的 HTTP 执行器实现（openai/grok/gemini），不经过本地 HTTP 回环。
type CreativeRunExecutor interface {
	// Prepare 选择可用账号并预占账号并发槽位；暂时没有槽位时返回 ErrCreativeExecutionPending。
	Prepare(ctx context.Context, run CreativeRun) (*CreativeExecution, error)
	// Execute 使用 Prepare 返回的上下文调用上游，不能在此阶段重新选择账号。
	Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload, execution *CreativeExecution) (*CreativeExecuteResult, error)
	// IsRetryable 判断瞬时错误是否值得有限重试。
	IsRetryable(err error) bool
}

// ErrCreativeExecutionPending 表示任务暂时没有用户或账号执行槽位，应保留 queued 并重排。
var ErrCreativeExecutionPending = errors.New("creative execution is pending concurrency admission")

const (
	defaultCreativeWorkerLockTTL             = 5 * time.Minute
	defaultCreativeWorkerLockConflictDelay   = 5 * time.Second
	defaultCreativeWorkerErrorRetryDelay     = time.Minute
	defaultCreativeWorkerRequeueDelay        = 30 * time.Second
	defaultCreativeWorkerDelayedPollInterval = 5 * time.Second
	defaultCreativeWorkerRecoveryInterval    = 5 * time.Minute
	defaultCreativeWorkerStaleActiveAfter    = 10 * time.Minute
	defaultCreativeWorkerDelayedMoveLimit    = 100
	defaultCreativeWorkerRecoverLimit        = 100
	defaultCreativeWorkerErrorBackoff        = time.Second
	defaultCreativeWorkerReserveBlockTimeout = 5 * time.Second
	defaultCreativeConcurrencyRequeueDelay   = time.Second
)

// CreativeWorkerOptions 是创作台 worker 的运行参数（全部可由配置覆盖）。
type CreativeWorkerOptions struct {
	ReserveBlockTimeout time.Duration
	JobLockTTL          time.Duration
	LockConflictDelay   time.Duration
	DefaultRequeueDelay time.Duration
	ErrorRetryDelay     time.Duration
	ErrorBackoff        time.Duration
	DelayedPollInterval time.Duration
	RecoveryInterval    time.Duration
	StaleActiveAfter    time.Duration
	DelayedMoveLimit    int
	RecoverLimit        int
	MaxAttempts         int
}

// NewCreativeWorkerOptionsFromConfig 从配置构造 worker 运行参数。
func NewCreativeWorkerOptionsFromConfig(cfg *config.Config) CreativeWorkerOptions {
	if cfg == nil {
		return normalizeCreativeWorkerOptions(CreativeWorkerOptions{})
	}
	return normalizeCreativeWorkerOptions(CreativeWorkerOptions{
		JobLockTTL:          time.Duration(cfg.Creative.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.Creative.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.Creative.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.Creative.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.Creative.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.Creative.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.Creative.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.Creative.DelayedMoveLimit,
		RecoverLimit:        cfg.Creative.RecoverLimit,
		MaxAttempts:         cfg.Creative.MaxExecuteAttempts,
	})
}

func normalizeCreativeWorkerOptions(opts CreativeWorkerOptions) CreativeWorkerOptions {
	if opts.ReserveBlockTimeout <= 0 {
		opts.ReserveBlockTimeout = defaultCreativeWorkerReserveBlockTimeout
	}
	if opts.JobLockTTL <= 0 {
		opts.JobLockTTL = defaultCreativeWorkerLockTTL
	}
	if opts.LockConflictDelay <= 0 {
		opts.LockConflictDelay = defaultCreativeWorkerLockConflictDelay
	}
	if opts.DefaultRequeueDelay <= 0 {
		opts.DefaultRequeueDelay = defaultCreativeWorkerRequeueDelay
	}
	if opts.ErrorRetryDelay <= 0 {
		opts.ErrorRetryDelay = defaultCreativeWorkerErrorRetryDelay
	}
	if opts.ErrorBackoff <= 0 {
		opts.ErrorBackoff = defaultCreativeWorkerErrorBackoff
	}
	if opts.DelayedPollInterval <= 0 {
		opts.DelayedPollInterval = defaultCreativeWorkerDelayedPollInterval
	}
	if opts.RecoveryInterval <= 0 {
		opts.RecoveryInterval = defaultCreativeWorkerRecoveryInterval
	}
	if opts.StaleActiveAfter <= 0 {
		opts.StaleActiveAfter = defaultCreativeWorkerStaleActiveAfter
	}
	if opts.DelayedMoveLimit <= 0 {
		opts.DelayedMoveLimit = defaultCreativeWorkerDelayedMoveLimit
	}
	if opts.RecoverLimit <= 0 {
		opts.RecoverLimit = defaultCreativeWorkerRecoverLimit
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultCreativeMaxAttempts
	}
	return opts
}

// CreativeProcessResult 是单次任务处理结果：Terminal 表示任务可 Ack，否则按 RequeueAfter 重排。
type CreativeProcessResult struct {
	RequeueAfter time.Duration
	Terminal     bool
}

// CreativeRunWorker 是创作台队列 worker：Reserve → 锁 → 执行 → 结算 → Ack/Requeue。
type CreativeRunWorker struct {
	queue       CreativeRunQueue
	repo        CreativeRunRepository
	store       CreativeTransientStore
	executor    CreativeRunExecutor
	service     *CreativePublicService
	concurrency *ConcurrencyService
	opts        CreativeWorkerOptions
	// busy 记录正在处理任务的 worker 数量，供管理端展示当前使用情况。
	busy atomic.Int32
}

type creativeLeaseStateKey struct{}

type creativeLeaseState struct {
	lost atomic.Bool
}

// NewCreativeRunWorker 创建创作台 worker。
func NewCreativeRunWorker(queue CreativeRunQueue, repo CreativeRunRepository, store CreativeTransientStore, executor CreativeRunExecutor, service *CreativePublicService, opts CreativeWorkerOptions, concurrencyServices ...*ConcurrencyService) *CreativeRunWorker {
	var concurrency *ConcurrencyService
	if len(concurrencyServices) > 0 {
		concurrency = concurrencyServices[0]
	}
	return &CreativeRunWorker{
		queue:       queue,
		repo:        repo,
		store:       store,
		executor:    executor,
		service:     service,
		concurrency: concurrency,
		opts:        normalizeCreativeWorkerOptions(opts),
	}
}

// Run 是 worker 主循环；ctx 取消后退出。
func (w *CreativeRunWorker) Run(ctx context.Context) {
	w.RunUntilStopped(ctx, nil)
}

// RunUntilStopped 运行一个可优雅排空的 worker；stop 关闭后不再领取新任务。
func (w *CreativeRunWorker) RunUntilStopped(ctx context.Context, stop <-chan struct{}) {
	if w == nil {
		return
	}
	for {
		if ctx.Err() != nil || creativeWorkerStopRequested(stop) {
			return
		}
		if err := w.runOnce(ctx, stop); err != nil && ctx.Err() == nil {
			if !sleepOrCreativeWorkerStop(ctx, w.opts.ErrorBackoff, stop) {
				return
			}
		}
	}
}

func creativeWorkerStopRequested(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func sleepOrCreativeWorkerStop(ctx context.Context, delay time.Duration, stop <-chan struct{}) bool {
	if delay <= 0 {
		return ctx.Err() == nil && !creativeWorkerStopRequested(stop)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

// BusyCount 返回正在处理任务的 worker 数量。
func (w *CreativeRunWorker) BusyCount() int {
	if w == nil {
		return 0
	}
	return int(w.busy.Load())
}

// RunOnce 处理一个队列任务。
func (w *CreativeRunWorker) RunOnce(ctx context.Context) error {
	return w.runOnce(ctx, nil)
}

func (w *CreativeRunWorker) runOnce(ctx context.Context, stop <-chan struct{}) error {
	if w == nil || w.queue == nil || w.repo == nil || w.service == nil || w.executor == nil {
		return nil
	}

	reserved, err := w.queue.Reserve(ctx, w.opts.ReserveBlockTimeout)
	if errors.Is(err, ErrCreativeQueueEmpty) {
		return nil
	}
	if err != nil {
		return err
	}
	if creativeWorkerStopRequested(stop) {
		// 缩容信号在 Reserve 阻塞期间到达时，把刚取出的任务立即放回 ready。
		return w.queue.RequeueAfter(ctx, reserved.RunID, reserved.LeaseToken, 0)
	}

	lock, ok, err := w.queue.TryAcquireJobLock(ctx, reserved.RunID, w.opts.JobLockTTL)
	if err != nil {
		if requeueErr := w.queue.RequeueAfter(ctx, reserved.RunID, reserved.LeaseToken, w.opts.LockConflictDelay); requeueErr != nil {
			return requeueErr
		}
		return err
	}
	if !ok {
		// 锁被其他实例持有：按冲突延迟重新入队，避免任务滞留 active 停摆。
		return w.queue.RequeueAfter(ctx, reserved.RunID, reserved.LeaseToken, w.opts.LockConflictDelay)
	}
	defer func() {
		_ = lock.Release(ctx)
	}()

	// 处理期间持续心跳：刷新 active 时间戳防止 stale 恢复误重投，并续期锁。
	hbStop := make(chan struct{})
	hbDone := make(chan struct{})
	leaseLost := make(chan struct{}, 1)
	processCtx, cancelProcess := context.WithCancel(ctx)
	leaseState := &creativeLeaseState{}
	processCtx = context.WithValue(processCtx, creativeLeaseStateKey{}, leaseState)
	go w.runJobHeartbeat(processCtx, reserved.RunID, reserved.LeaseToken, lock, hbStop, hbDone, leaseLost, cancelProcess, leaseState)

	w.busy.Add(1)
	result, processErr := w.process(processCtx, reserved.RunID)
	w.busy.Add(-1)
	close(hbStop)
	<-hbDone
	cancelProcess()
	select {
	case <-leaseLost:
		// 当前 worker 已失去队列租约，不能再 ACK、重排队或提交任何结果。
		return ErrCreativeLeaseLost
	default:
	}
	if processErr != nil {
		logger.L().Warn("creative.worker_process_failed",
			zap.String("run_id", reserved.RunID),
			zap.Error(processErr),
		)
		return w.queue.RequeueAfter(ctx, reserved.RunID, reserved.LeaseToken, w.opts.ErrorRetryDelay)
	}
	if result.Terminal {
		return w.queue.Ack(ctx, reserved.RunID, reserved.LeaseToken)
	}
	delay := result.RequeueAfter
	if delay <= 0 {
		delay = w.opts.DefaultRequeueDelay
	}
	return w.queue.RequeueAfter(ctx, reserved.RunID, reserved.LeaseToken, delay)
}

// process 处理单个任务：加载载荷 → 执行 → 结算。所有结算动作幂等。
func (w *CreativeRunWorker) process(ctx context.Context, runID string) (CreativeProcessResult, error) {
	run, err := w.repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrCreativeRunNotFound) {
			// 任务已被删除：直接出队。
			return CreativeProcessResult{Terminal: true}, nil
		}
		return CreativeProcessResult{}, err
	}
	if run == nil {
		return CreativeProcessResult{}, errors.New("creative run is unavailable")
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return CreativeProcessResult{Terminal: true}, nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		if err := w.service.ReleaseRun(ctx, runID); err != nil {
			if _, incErr := w.repo.IncrementCreativeRunReleaseAttempt(ctx, runID); incErr != nil {
				return CreativeProcessResult{}, incErr
			}
			_ = w.repo.SetCreativeRunReconcileError(ctx, runID, sanitizeCreativeMessage(err.Error()), time.Now().Add(w.opts.ErrorRetryDelay))
			return CreativeProcessResult{}, err
		}
		_ = w.repo.SetCreativeRunReconcileError(ctx, runID, "", time.Time{})
		return CreativeProcessResult{Terminal: true}, nil
	}
	if IsCreativeRunSettlementPending(run.Status) {
		if err := w.service.SettleRun(ctx, runID); err != nil {
			attempts, incErr := w.repo.IncrementCreativeRunSettlementAttempt(ctx, runID)
			if incErr != nil {
				return CreativeProcessResult{}, incErr
			}
			_ = w.repo.SetCreativeRunReconcileError(ctx, runID, sanitizeCreativeMessage(err.Error()), time.Now().Add(w.opts.ErrorRetryDelay))
			if attempts >= w.opts.MaxAttempts*2 {
				logger.L().Warn("creative.worker_settlement_pending_retries_exhausted",
					zap.String("run_id", runID),
					zap.Int("attempts", attempts),
					zap.Error(err),
				)
			}
			return CreativeProcessResult{RequeueAfter: w.opts.ErrorRetryDelay}, nil
		}
		_ = w.repo.SetCreativeRunReconcileError(ctx, runID, "", time.Time{})
		return CreativeProcessResult{Terminal: true}, nil
	}
	if recovered, recoverErr := w.recoverPersistedProviderResult(ctx, run); recoverErr != nil {
		return CreativeProcessResult{}, recoverErr
	} else if recovered {
		return CreativeProcessResult{Terminal: true}, nil
	}

	payload, err := w.loadPayload(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrCreativeTransientUnavailable) {
			// Redis 基础设施故障不能变成终态；保持 active 由调用方重排队。
			return CreativeProcessResult{}, err
		}
		// 载荷/输入过期或损坏：provider 未执行，按 result_lost 处理并释放预占。
		if markErr := w.service.MarkResultLost(ctx, runID, false); markErr != nil {
			logger.L().Warn("creative.worker_mark_result_lost_failed",
				zap.String("run_id", runID),
				zap.Error(markErr),
			)
			return CreativeProcessResult{}, markErr
		}
		return CreativeProcessResult{Terminal: true}, nil
	}
	// 异步任务只在真正执行阶段占用用户并发槽位；未获取槽位时让出 worker 并重排任务。
	var userRelease func()
	if w.concurrency != nil && w.service.UserRepo != nil {
		user, userErr := w.service.UserRepo.GetByID(ctx, run.UserID)
		if userErr != nil {
			if errors.Is(userErr, ErrUserNotFound) {
				_ = w.service.MarkResultLost(ctx, runID, false)
				return CreativeProcessResult{Terminal: true}, nil
			}
			return CreativeProcessResult{}, userErr
		}
		if user == nil {
			return CreativeProcessResult{}, errors.New("creative run user is unavailable")
		}
		acquired, acquireErr := w.concurrency.AcquireUserSlot(ctx, run.UserID, user.Concurrency)
		if acquireErr != nil {
			return CreativeProcessResult{}, acquireErr
		}
		if acquired == nil || !acquired.Acquired {
			return CreativeProcessResult{RequeueAfter: defaultCreativeConcurrencyRequeueDelay}, nil
		}
		userRelease = acquired.ReleaseFunc
		defer func() {
			if userRelease != nil {
				userRelease()
			}
		}()
	}

	execution, err := w.executor.Prepare(ctx, *run)
	if errors.Is(err, ErrCreativeExecutionPending) {
		return CreativeProcessResult{RequeueAfter: defaultCreativeConcurrencyRequeueDelay}, nil
	}
	if err != nil {
		return w.handleExecuteError(ctx, runID, err)
	}
	if execution == nil || execution.Account == nil {
		return w.handleExecuteError(ctx, runID, errors.New("creative execution account is unavailable"))
	}
	if execution.ReleaseFunc == nil && execution.Selection != nil {
		execution.ReleaseFunc = execution.Selection.ReleaseFunc
	}
	// 槽位只覆盖上游生图阶段；provider 返回后立即释放，结算/计费不占用并发名额。
	releaseSlots := func() {
		if execution.ReleaseFunc != nil {
			execution.ReleaseFunc()
			execution.ReleaseFunc = nil
		}
		if userRelease != nil {
			userRelease()
			userRelease = nil
		}
	}
	defer releaseSlots()

	// 幂等推进 running；账号已在 Prepare 阶段准入，成功结算时再写入实际账号。
	if err := w.service.MarkRunning(ctx, runID, 0); err != nil {
		return CreativeProcessResult{}, err
	}
	// 先持久化实际账号，再调用 provider，确保结果元数据已落库时可恢复结算。
	if execution.Account.ID > 0 {
		if err := w.repo.SetCreativeRunAccountID(ctx, runID, execution.Account.ID, time.Now()); err != nil {
			return CreativeProcessResult{}, err
		}
	}
	// 执行前再次检查：任务已进入 cancelled 则不再调用上游。
	current, err := w.repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return CreativeProcessResult{}, err
	}
	if current == nil {
		return CreativeProcessResult{}, errors.New("creative run is unavailable")
	}
	if current.Status == CreativeRunStatusCancelled || IsTerminalCreativeRunStatus(current.Status) {
		if err := w.service.CancelRunByWorker(ctx, runID); err != nil {
			return CreativeProcessResult{}, err
		}
		return CreativeProcessResult{Terminal: true}, nil
	}

	result, err := w.executor.Execute(ctx, *current, *payload, execution)
	releaseSlots()
	if err != nil {
		return w.handleExecuteError(ctx, runID, err)
	}
	if leaseState, _ := ctx.Value(creativeLeaseStateKey{}).(*creativeLeaseState); leaseState != nil && leaseState.lost.Load() {
		return CreativeProcessResult{}, ErrCreativeLeaseLost
	}
	if result == nil {
		return w.handleExecuteError(ctx, runID, errors.New("creative executor returned no result"))
	}
	results := make([]CreativeOutputResult, 0, len(result.Outputs))
	for _, output := range result.Outputs {
		results = append(results, CreativeOutputResult{
			Index:   output.Index,
			Success: true,
			Bytes:   output.Bytes,
			Mime:    output.Mime,
		})
	}
	if _, err := w.service.SucceedRun(ctx, runID, result.AccountID, results); err != nil {
		// provider 已成功但结果记录失败：短暂重排当前任务，不能把错误当作成功 ACK。
		attempts, incErr := w.repo.IncrementCreativeRunAttempt(ctx, runID)
		if incErr != nil {
			return CreativeProcessResult{}, incErr
		}
		if attempts < w.opts.MaxAttempts {
			return CreativeProcessResult{RequeueAfter: w.opts.ErrorRetryDelay}, nil
		}
		return CreativeProcessResult{}, err
	}
	// 优先在当前 lease 内完成结算；失败时 run 已是 provider_succeeded，后续只会重试结算。
	if err := w.service.SettleRun(ctx, runID); err != nil {
		return CreativeProcessResult{}, err
	}
	return CreativeProcessResult{Terminal: true}, nil
}

// loadPayload 从临时存储加载任务载荷与输入字节；缺失即视为结果丢失。
func (w *CreativeRunWorker) loadPayload(ctx context.Context, runID string) (*CreativeRunPayload, error) {
	if w.store == nil {
		return nil, errors.New("creative transient store is not configured")
	}
	payload, err := w.store.LoadPayload(ctx, runID)
	if err != nil {
		return nil, err
	}
	if payload.SourceCount > 0 {
		inputs, err := w.store.LoadInputs(ctx, runID, payload.SourceCount)
		if err != nil {
			return nil, err
		}
		payload.Sources = make([]CreativeInputImage, 0, len(inputs))
		// 载荷中没有逐图 MIME，输入键按字节保存；执行器按魔数嗅探。
		for _, data := range inputs {
			payload.Sources = append(payload.Sources, CreativeInputImage{Bytes: data, Mime: sniffCreativeImageMime(data)})
		}
	}
	if payload.HasMask {
		mask, err := w.store.LoadMask(ctx, runID)
		if err != nil {
			return nil, err
		}
		payload.Mask = &CreativeInputImage{Bytes: mask, Mime: sniffCreativeImageMime(mask)}
	}
	return payload, nil
}

// recoverPersistedProviderResult 检查 provider 已返回但 run 状态写入前进程崩溃的窗口。
// 输出元数据已经落库时无需再次调用 provider，直接补记 provider 成功并进入结算。
func (w *CreativeRunWorker) recoverPersistedProviderResult(ctx context.Context, run *CreativeRun) (bool, error) {
	if w == nil || run == nil || run.Status != CreativeRunStatusRunning {
		return false, nil
	}
	outputs, err := w.repo.ListCreativeRunOutputs(ctx, run.RunID)
	if err != nil {
		return false, err
	}
	if len(outputs) == 0 {
		return false, nil
	}
	successCount := 0
	for _, output := range outputs {
		if output == nil || output.Status == CreativeRunOutputStatusPending {
			return false, nil
		}
		if output.Status == CreativeRunOutputStatusSucceeded || output.Status == CreativeRunOutputStatusAcked {
			successCount++
		}
	}
	if successCount == 0 || run.AccountID == nil || *run.AccountID <= 0 {
		return false, nil
	}
	if err := w.repo.MarkCreativeRunProviderSucceeded(ctx, run.RunID, *run.AccountID, time.Now()); err != nil {
		return false, err
	}
	if err := w.service.ensureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxSettle); err != nil {
		return false, err
	}
	if err := w.service.SettleRun(ctx, run.RunID); err != nil {
		return false, err
	}
	return true, nil
}

// handleExecuteError 处理执行错误：可重试且未达上限 → 递增 attempt 并重排；否则 FailRun 出队。
func (w *CreativeRunWorker) handleExecuteError(ctx context.Context, runID string, execErr error) (CreativeProcessResult, error) {
	if w.executor.IsRetryable(execErr) {
		attempts, err := w.repo.IncrementCreativeRunAttempt(ctx, runID)
		if err != nil {
			return CreativeProcessResult{}, err
		}
		if attempts < w.opts.MaxAttempts {
			return CreativeProcessResult{RequeueAfter: w.opts.ErrorRetryDelay}, nil
		}
		logger.L().Warn("creative.worker_attempts_exhausted",
			zap.String("run_id", runID),
			zap.Int("attempts", attempts),
			zap.Error(execErr),
		)
	}
	code, message := creativeExecuteErrorParts(execErr)
	if err := w.service.FailRun(ctx, runID, code, message); err != nil {
		return CreativeProcessResult{}, err
	}
	return CreativeProcessResult{Terminal: true}, nil
}

// creativeExecuteErrorParts 把执行错误映射为落库的错误码与消息。
func creativeExecuteErrorParts(err error) (string, string) {
	var upstreamErr *CreativeUpstreamError
	if errors.As(err, &upstreamErr) {
		code := "PROVIDER_FAILED"
		if upstreamErr.StatusCode > 0 {
			code = "UPSTREAM_STATUS_" + itoaPositive(upstreamErr.StatusCode)
		}
		return code, upstreamErr.Message
	}
	return "PROVIDER_FAILED", sanitizeCreativeMessage(err.Error())
}

func itoaPositive(v int) string {
	if v <= 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// runJobHeartbeat 处理期间持续心跳：刷新 active 时间戳并续期锁。
func (w *CreativeRunWorker) runJobHeartbeat(ctx context.Context, runID, leaseToken string, lock CreativeRunJobLock, stop <-chan struct{}, done chan<- struct{}, leaseLost chan<- struct{}, cancel context.CancelFunc, leaseState *creativeLeaseState) {
	defer close(done)
	ticker := time.NewTicker(w.heartbeatInterval())
	defer ticker.Stop()
	var lost atomic.Bool
	markLost := func() {
		if lost.CompareAndSwap(false, true) {
			if leaseState != nil {
				leaseState.lost.Store(true)
			}
			leaseLost <- struct{}{}
			if cancel != nil {
				cancel()
			}
		}
	}
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			owned, err := w.queue.Heartbeat(ctx, runID, leaseToken)
			if err != nil && ctx.Err() == nil {
				logger.L().Warn("creative.worker_heartbeat_failed",
					zap.String("run_id", runID),
					zap.Error(err),
				)
				// 无法确认租约归属时按失去租约处理，避免 Redis 分区期间旧 worker 继续写结果。
				markLost()
				return
			} else if err == nil && !owned {
				logger.L().Warn("creative.worker_lease_lost",
					zap.String("run_id", runID),
				)
				markLost()
				return
			}
			if refresher, ok := lock.(CreativeRunJobLockRefresher); ok {
				owned, err := refresher.Refresh(ctx, w.opts.JobLockTTL)
				if err != nil && ctx.Err() == nil {
					logger.L().Warn("creative.worker_lock_refresh_failed",
						zap.String("run_id", runID),
						zap.Error(err),
					)
					markLost()
					return
				} else if err == nil && !owned {
					logger.L().Warn("creative.worker_lock_lost",
						zap.String("run_id", runID),
					)
					markLost()
					return
				}
			}
		}
	}
}

func (w *CreativeRunWorker) heartbeatInterval() time.Duration {
	interval := w.opts.JobLockTTL
	if w.opts.StaleActiveAfter < interval {
		interval = w.opts.StaleActiveAfter
	}
	interval /= 3
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}

// MoveDueDelayedOnce 把到期的 delayed 任务搬回 ready。
func (w *CreativeRunWorker) MoveDueDelayedOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	return w.queue.MoveDueDelayedToReady(ctx, w.opts.DelayedMoveLimit)
}

// RunDelayedMover 是 delayed mover 主循环。
func (w *CreativeRunWorker) RunDelayedMover(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		moved, _ := w.MoveDueDelayedOnce(ctx)
		if moved > 0 {
			continue
		}
		sleepOrDone(ctx, w.opts.DelayedPollInterval)
	}
}

// RecoverStaleActiveOnce 把超时未心跳的 active 任务重投回 ready（worker 重启恢复）。
func (w *CreativeRunWorker) RecoverStaleActiveOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	return w.queue.RecoverStaleActive(ctx, w.opts.StaleActiveAfter, w.opts.RecoverLimit)
}

// RunStaleActiveRecovery 是 stale active 恢复主循环。
func (w *CreativeRunWorker) RunStaleActiveRecovery(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_, _ = w.RecoverStaleActiveOnce(ctx)
		sleepOrDone(ctx, w.opts.RecoveryInterval)
	}
}
