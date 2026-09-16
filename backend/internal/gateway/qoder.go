// Package gateway 拥有请求级准入、尝试与完成次序，平台只执行单次调用。
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Request 使用已认证主体及最终路由计划，不定义第二套身份或路由实体。
type Request struct {
	Access      *apikey.AccessSnapshot
	Route       routing.RoutePlan
	UserID      int64
	Concurrency int
	Stream      bool
	Body        []byte
	Model       string
}

type FailureStage string

const (
	FailureBilling        FailureStage = "billing"
	FailureUserQueue      FailureStage = "user_queue"
	FailureUserSlot       FailureStage = "user_slot"
	FailureAccountSlot    FailureStage = "account_slot"
	FailureSelection      FailureStage = "selection"
	FailureExhausted      FailureStage = "exhausted"
	FailureRefreshPending FailureStage = "refresh_pending"
	FailureUpstream       FailureStage = "upstream"
)

// Failure 保留原始错误与失败阶段，协议状态码由 HTTP 适配器映射。
type Failure struct {
	Stage     FailureStage
	Cause     error
	Attempted bool
}

func (e *Failure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Stage)
}
func (e *Failure) Unwrap() error { return e.Cause }

// Selection 的回调只提供当前目标的旧能力过渡端口，不拥有重试循环或共享状态。
type Selection struct {
	WaitWithoutCounter bool
	Snapshot           account.AccountSnapshot
	Plan               routing.CandidatePlan
	Acquired           bool
	Release            func()
	WaitPlan           *scheduler.AccountWaitPlan
	Input              upstream.AttemptInput
	Executor           upstream.Executor
	Refresh            func(context.Context) (*Selection, error)
	Observe            func(upstream.AttemptResult, error)
	Bind               func(context.Context, upstream.AttemptResult)
	Complete           func(context.Context, upstream.AttemptResult)
	Switched           func()
}

// RequestPorts 在 app 绑定实际唯一实例；Check(afterWait) 复查资金时不再次累计 RPM。
type RequestPorts struct {
	Concurrency    *scheduler.ConcurrencyService
	Check          func(context.Context, bool) error
	Select         func(context.Context, map[int64]struct{}) (*Selection, error)
	CanRefresh     func(error) bool
	CanFailover    func(error) bool
	RefreshPending func(error) bool
	WaitObserver   func(string) scheduler.WaitObserver
	QueueFailure   func(string, error)
}

// QoderUseCase 只保存静态尝试上限和等待预算，所有请求状态位于 Run 栈上。
type QoderUseCase struct {
	MaxAccounts int
	WaitTimeout time.Duration
	Enter       func() (func(), error)
}

func NewQoderUseCase(maxAccounts int, waitTimeout time.Duration) *QoderUseCase {
	return &QoderUseCase{MaxAccounts: maxAccounts, WaitTimeout: waitTimeout}
}

// Run 贯通已有准入、Lease、平台执行与完成处理，只有这里拥有本请求的账号尝试循环。
// @project-doc docs/architecture/gateway_request_lifecycle.md#qoder_gateway_execution
func (u *QoderUseCase) Run(ctx context.Context, request Request, ports RequestPorts, output *OutputTracker) error {
	if u.Enter != nil {
		done, err := u.Enter()
		if err != nil {
			return err
		}
		defer done()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ports.Check != nil {
		if err := ports.Check(ctx, false); err != nil {
			return &Failure{Stage: FailureBilling, Cause: err}
		}
	}
	mode := scheduler.ReleaseOnCancel
	if request.Stream {
		mode = scheduler.ReleaseOnCompletion
	}
	lease := scheduler.NewLease(ctx, mode)
	defer lease.Release()
	if ports.Concurrency != nil {
		// 保留 Qoder 在首次抢用户槽前登记外层等待名额的时机。
		waiting, waitErr := ports.Concurrency.EnterUserWait(ctx, request.UserID, scheduler.CalculateMaxWait(request.Concurrency))
		defer waiting.Release()
		if waitErr != nil {
			if ports.QueueFailure != nil {
				ports.QueueFailure("user", waitErr)
			}
		} else if !waiting.Allowed {
			return &Failure{Stage: FailureUserQueue, Cause: &scheduler.WaitQueueFullError{SlotType: "user"}}
		}
		observer := scheduler.WaitObserver{}
		if ports.WaitObserver != nil {
			observer = ports.WaitObserver("user")
		}
		waited := false
		begin := observer.Begin
		observer.Begin = func() error {
			waited = true
			if begin != nil {
				return begin()
			}
			return nil
		}
		keyID := int64(0)
		if request.Access != nil {
			keyID = request.Access.KeyID
		}
		userLease, _, err := ports.Concurrency.AcquireUser(ctx, scheduler.UserAcquireOptions{UserID: request.UserID, APIKeyID: keyID, Limit: request.Concurrency, Timeout: u.WaitTimeout, Mode: scheduler.ReleaseOnCompletion, Observer: observer})
		if err != nil {
			return &Failure{Stage: FailureUserSlot, Cause: err}
		}
		lease.Own(userLease.Release)
		waiting.Release()
		if waited && ports.Check != nil {
			if err := ports.Check(ctx, true); err != nil {
				return &Failure{Stage: FailureBilling, Cause: err}
			}
		}
	}
	ctx = scheduler.WithRequestLease(ctx, lease)
	excluded := make(map[int64]struct{})
	var lastErr error
	refreshPending := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		selected, err := ports.Select(ctx, excluded)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if refreshPending {
				return &Failure{Stage: FailureRefreshPending, Cause: err, Attempted: true}
			}
			if len(excluded) == 0 {
				return &Failure{Stage: FailureSelection, Cause: err}
			}
			if lastErr != nil {
				err = lastErr
			}
			return &Failure{Stage: FailureExhausted, Cause: err, Attempted: true}
		}
		refreshPending = false
		result, attemptErr := u.attempt(ctx, request, ports, lease, selected, output)
		if selected.Observe != nil {
			selected.Observe(result, attemptErr)
		}
		if attemptErr != nil && result.Served && result.HasUsage {
			if selected.Complete != nil {
				selected.Complete(ctx, result)
			}
			return &Failure{Stage: FailureUpstream, Cause: attemptErr, Attempted: true}
		}
		if attemptErr != nil && (errors.Is(attemptErr, context.Canceled) || ctx.Err() != nil) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return attemptErr
		}
		var slotErr *Failure
		if errors.As(attemptErr, &slotErr) {
			return slotErr
		}
		if attemptErr != nil && !output.AttemptCommitted && selected.Refresh != nil && ports.CanRefresh != nil && ports.CanRefresh(attemptErr) {
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			refreshed, refreshErr := selected.Refresh(refreshCtx)
			cancel()
			if refreshErr == nil && refreshed != nil {
				selected = refreshed
				result, attemptErr = u.attempt(ctx, request, ports, lease, selected, output)
				if selected.Observe != nil {
					selected.Observe(result, attemptErr)
				}
				if attemptErr != nil && result.Served && result.HasUsage {
					if selected.Complete != nil {
						selected.Complete(ctx, result)
					}
					return &Failure{Stage: FailureUpstream, Cause: attemptErr, Attempted: true}
				}
				if attemptErr != nil && (errors.Is(attemptErr, context.Canceled) || ctx.Err() != nil) {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return attemptErr
				}
				if errors.As(attemptErr, &slotErr) {
					return slotErr
				}
			} else if ports.RefreshPending != nil && ports.RefreshPending(refreshErr) {
				if !output.AttemptCommitted {
					excluded[selected.Snapshot.ID] = struct{}{}
					if len(excluded) < u.MaxAccounts {
						refreshPending = true
						if selected.Switched != nil {
							selected.Switched()
						}
						continue
					}
				}
				return &Failure{Stage: FailureRefreshPending, Cause: refreshErr, Attempted: true}
			}
		}
		if attemptErr == nil {
			if selected.Bind != nil {
				selected.Bind(ctx, result)
			}
			if selected.Complete != nil {
				selected.Complete(ctx, result)
			}
			return nil
		}
		if !output.AttemptCommitted && ports.CanFailover != nil && ports.CanFailover(attemptErr) {
			excluded[selected.Snapshot.ID] = struct{}{}
			if len(excluded) < u.MaxAccounts {
				lastErr = attemptErr
				if selected.Switched != nil {
					selected.Switched()
				}
				continue
			}
		}
		return &Failure{Stage: FailureUpstream, Cause: attemptErr, Attempted: true}
	}
}

func (u *QoderUseCase) attempt(ctx context.Context, request Request, ports RequestPorts, parent *scheduler.Lease, selected *Selection, output *OutputTracker) (upstream.AttemptResult, error) {
	output.BeginAttempt()
	attempt := scheduler.NewAttemptLease(parent, nil, selected.Release)
	defer attempt.Release()
	if !selected.Acquired {
		if ports.Concurrency == nil || selected.WaitPlan == nil {
			return upstream.AttemptResult{}, &Failure{Stage: FailureAccountSlot, Cause: errors.New("no available accounts")}
		}
		plan := selected.WaitPlan
		var waiting scheduler.WaitResult
		var err error
		if !selected.WaitWithoutCounter {
			waiting, err = ports.Concurrency.EnterAccountWait(ctx, selected.Snapshot.ID, plan.MaxWaiting)
		} else {
			waiting.Allowed = true
		}
		defer waiting.Release()
		if err != nil {
			if ports.QueueFailure != nil {
				ports.QueueFailure("account", err)
			}
		} else if !waiting.Allowed {
			return upstream.AttemptResult{}, &Failure{Stage: FailureAccountSlot, Cause: &scheduler.WaitQueueFullError{SlotType: "account"}}
		}
		observer := scheduler.WaitObserver{}
		if ports.WaitObserver != nil {
			observer = ports.WaitObserver("account")
		}
		release, err := ports.Concurrency.WaitForSlot(ctx, "account", selected.Snapshot.ID, plan.MaxConcurrency, plan.Timeout, true, observer)
		if err != nil {
			return upstream.AttemptResult{}, &Failure{Stage: FailureAccountSlot, Cause: err}
		}
		waiting.Release()
		attempt = scheduler.NewAttemptLease(parent, nil, release)
		defer attempt.Release()
	}
	if err := ctx.Err(); err != nil {
		return upstream.AttemptResult{}, err
	}
	result, err := selected.Executor.Execute(ctx, selected.Input, output)
	attempt.Finish(scheduler.AttemptOutcome{Served: result.Served})
	return result, err
}
