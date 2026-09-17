// 旧 worker 仅转换执行目标与准入端口，状态由 creative 唯一持有。
package service

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	native "github.com/TokenFlux/TokenRouter/internal/creative"
)

type CreativeRunWorker struct {
	*native.CreativeRunWorker
	opts    CreativeWorkerOptions
	service *CreativePublicService
}
type CreativeOutput = native.CreativeOutput
type CreativeExecuteResult = native.CreativeExecuteResult

// CreativeExecution 是一次已经完成账号调度与账号槽位预占的执行上下文。
// worker 在标记任务 running 前创建它，确保并发未准入的任务仍保持 queued。
type CreativeExecution struct {
	Native        *native.CreativeExecution
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

var ErrCreativeExecutionPending = native.ErrCreativeExecutionPending

type CreativeWorkerOptions = native.CreativeWorkerOptions
type CreativeProcessResult = native.CreativeProcessResult

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
	return native.NormalizeCreativeWorkerOptions(opts)
}
func NewCreativeRunWorker(queue CreativeRunQueue, repo CreativeRunRepository, store CreativeTransientStore, executor CreativeRunExecutor, service *CreativePublicService, opts CreativeWorkerOptions, concurrencyServices ...*ConcurrencyService) *CreativeRunWorker {
	var ports native.WorkerPorts
	ports.Observe = creativeLegacyObserve
	var concurrency *ConcurrencyService
	if len(concurrencyServices) > 0 {
		concurrency = concurrencyServices[0]
	}
	if concurrency != nil && service != nil && service.UserRepo != nil {
		ports.UserMissing = func(err error) bool { return errors.Is(err, ErrUserNotFound) }
		ports.AcquireUser = func(ctx context.Context, id int64) (func(), bool, error) {
			user, err := service.UserRepo.GetByID(ctx, id)
			if err != nil {
				return nil, false, err
			}
			if user == nil {
				return nil, false, errors.New("creative run user is unavailable")
			}
			acquired, err := concurrency.AcquireUserSlot(ctx, id, user.Concurrency)
			if err != nil {
				return nil, false, err
			}
			if acquired == nil {
				return nil, false, nil
			}
			return acquired.ReleaseFunc, acquired.Acquired, nil
		}
	}
	var adapter native.CreativeRunExecutor
	if executor != nil {
		adapter = creativeExecutorProjection{executor}
	}
	core := native.NewCreativeRunWorker(queue, repo, store, adapter, service.nativeResults(), opts, ports)
	return &CreativeRunWorker{CreativeRunWorker: core, opts: core.Options(), service: service}
}

type creativeExecutorProjection struct{ executor CreativeRunExecutor }

func (p creativeExecutorProjection) Prepare(ctx context.Context, run CreativeRun) (*native.CreativeExecution, error) {
	value, err := p.executor.Prepare(ctx, run)
	if err != nil {
		return nil, err
	}
	if value != nil && value.Native != nil {
		return value.Native, nil
	}
	if value == nil || value.Account == nil {
		return nil, nil
	}
	release := value.ReleaseFunc
	if release == nil && value.Selection != nil {
		release = value.Selection.ReleaseFunc
	}
	return &native.CreativeExecution{AccountID: value.Account.ID, UpstreamModel: value.UpstreamModel, ReleaseFunc: release, Target: creativeExecutionProjection{executor: p.executor, value: value}}, nil
}
func (p creativeExecutorProjection) IsRetryable(err error) bool { return p.executor.IsRetryable(err) }

type creativeExecutionProjection struct {
	executor CreativeRunExecutor
	value    *CreativeExecution
}

func (p creativeExecutionProjection) Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload) (*CreativeExecuteResult, error) {
	return p.executor.Execute(ctx, run, payload, p.value)
}
