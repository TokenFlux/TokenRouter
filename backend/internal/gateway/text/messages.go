// Package text 拥有文本入口的账号尝试次序，HTTP 只执行同步输出。
package text

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
)

// AttemptFailure 仅把原始错误与不可变重试投影一起传递，核心不解析供应商报文。
type AttemptFailure struct {
	Cause  error
	Policy *failover.FailureInfo
}

func (e *AttemptFailure) Error() string { return e.Cause.Error() }
func (e *AttemptFailure) Unwrap() error { return e.Cause }
func (e *AttemptFailure) RetryFailure() *failover.FailureInfo {
	if e == nil {
		return nil
	}
	return e.Policy
}

type FailureKind uint8

const (
	FailureOther FailureKind = iota
	FailurePolicy
	FailurePromptTooLong
)

// Selection 只包含本次尝试的账号视图和既有重试上限，不持有凭据。
type Selection struct {
	Plan         routing.CandidatePlan
	PlanProvided bool
	Account      account.AccountSnapshot
	RetryLimit   int
}
type Outcome struct {
	Attempt       upstream.AttemptResult
	Err           error
	Failure       *AttemptFailure
	Kind          FailureKind
	OutputChanged bool
	HasResult     bool
	Stop          bool
	Skip          bool
}

type AttemptState struct {
	SwitchCount       int
	ForceCacheBilling bool
}
type MessageOptions struct {
	MaxSwitches            int
	CompletePartialFailure bool
	StopOnCanceledContext  bool
	HasBoundSession        bool
	Observe                failover.Observe
}

// MessagePorts 的方法均为单步能力；外部实现不能再包一层账号切换循环。
// 每次请求的输出与目标由适配实例持有，固定依赖在构造 HTTP 入口时绑定。
type MessagePorts interface {
	Context() context.Context
	Begin()
	Finish(bool)
	PrepareAttempt() bool
	Select(map[int64]struct{}) (Selection, error)
	FirstSelectionFailure(error, bool)
	SingleAccountRetry()
	Canceled()
	Exhausted(*AttemptFailure, string, bool)
	Intercept() bool
	Acquire() bool
	Forward(AttemptState) Outcome
	PolicyFailure(error)
	Fallback(error, bool) bool
	OtherFailure(error)
	Complete(AttemptState)
	Success()
	Switched()
	Abandon(int64)
	TempUnscheduleRetryableError(context.Context, int64, *AttemptFailure)
}

// RunMessages 保留原有两层控制：请求只有一次分组回退，每个分组只有一套账号尝试循环。
func RunMessages(options MessageOptions, p MessagePorts) {
	served := false
	defer func() { p.Finish(served) }()
	p.Begin()
	fallbackUsed := false
	for {
		state := failover.NewFailoverState[*AttemptFailure](options.MaxSwitches, options.HasBoundSession, options.Observe)
		retryWithFallback := false
		for {
			if options.StopOnCanceledContext && p.Context().Err() != nil {
				return
			}
			if !p.PrepareAttempt() {
				return
			}
			selected, err := p.Select(state.FailedAccountIDs)
			if err != nil {
				if len(state.FailedAccountIDs) == 0 {
					p.FirstSelectionFailure(err, fallbackUsed)
					return
				}
				switch state.HandleSelectionExhausted(p.Context()) {
				case failover.FailoverContinue:
					p.SingleAccountRetry()
					continue
				case failover.FailoverCanceled:
					p.Canceled()
					return
				default:
					p.Exhausted(state.LastFailoverErr, "", false)
					return
				}
			}
			if p.Intercept() {
				return
			}
			if !p.Acquire() {
				return
			}
			attempt := AttemptState{SwitchCount: state.SwitchCount, ForceCacheBilling: state.ForceCacheBilling}
			outcome := p.Forward(attempt)
			if outcome.Stop {
				return
			}
			if outcome.Skip {
				state.FailedAccountIDs[selected.Account.ID] = struct{}{}
				continue
			}
			if outcome.Err != nil {
				switch outcome.Kind {
				case FailurePolicy:
					p.PolicyFailure(outcome.Err)
					return
				case FailurePromptTooLong:
					if p.Fallback(outcome.Err, fallbackUsed) {
						fallbackUsed = true
						retryWithFallback = true
					}
					// 回退成功才重新开始，失败响应仍由同一个 HTTP 适配器输出。
					if retryWithFallback {
						break
					}
					return
				}
				if retryWithFallback {
					break
				}
				if outcome.Failure != nil {
					if outcome.OutputChanged {
						p.Exhausted(outcome.Failure, selected.Account.Platform, true)
						return
					}
					switch state.HandleFailoverError(p.Context(), p, selected.Account.ID, selected.Account.Platform, selected.RetryLimit, outcome.Failure) {
					case failover.FailoverContinue:
						p.Switched()
						p.Abandon(selected.Account.ID)
						continue
					case failover.FailoverExhausted:
						p.Exhausted(state.LastFailoverErr, selected.Account.Platform, false)
						return
					case failover.FailoverCanceled:
						p.Canceled()
						return
					}
				}
				p.OtherFailure(outcome.Err)
				if options.CompletePartialFailure {
					p.Complete(attempt)
					served = outcome.HasResult
				}
				return
			}
			p.Success()
			p.Complete(attempt)
			served = true
			return
		}
		if !retryWithFallback {
			return
		}
	}
}
