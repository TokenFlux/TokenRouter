package text

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
)

// ResponseSelection 保留 HTTP continuation 的逐候选跳过，不把它计为账号切换。
type ResponseSelection struct {
	Selection
	Available bool
	Skip      *AttemptFailure
	OAuth     failover.OAuth429Account
}

// ResponseOutcome 将图片部分成功和首输出恢复资格与普通错误明确分开。
type ResponseOutcome struct {
	Outcome
	Images              bool
	FirstOutputRecovery bool
}

type ResponseOptions struct {
	MaxSwitches       int
	FirstOutputBudget bool
}

// ResponsePorts 只执行一次选取、转发、观测或完成，核心统一拥有重试计数。
type ResponsePorts interface {
	Context() context.Context
	CanAttempt() bool
	Select(map[int64]struct{}) (ResponseSelection, error)
	SelectionFailure(error, int, *AttemptFailure)
	Acquire() bool
	Forward() ResponseOutcome
	PartialImages(error)
	RetryReady(*AttemptFailure) bool
	RetryWait(*AttemptFailure, int, int, time.Duration)
	Exhausted(*AttemptFailure)
	Switched()
	Switching(*AttemptFailure, int, int)
	OtherFailure(error)
	Failed()
	Complete()
	Success()
	Completed(int)
}

// RunResponses 保留同账号恢复、一次请求的账号预算和首输出后的禁止重放边界。
func RunResponses(options ResponseOptions, p ResponsePorts) {
	excluded := make(map[int64]struct{})
	sameAccount := make(map[int64]int)
	switches, firstOutputSwitches := 0, 0
	var last *AttemptFailure
	var oauth failover.OAuth429State
	for {
		if !p.CanAttempt() {
			return
		}
		selected, err := p.Select(excluded)
		if err != nil {
			p.SelectionFailure(err, len(excluded), last)
			return
		}
		if !selected.Available {
			return
		}
		if selected.Skip != nil {
			excluded[selected.Account.ID] = struct{}{}
			last = selected.Skip
			continue
		}
		if !p.Acquire() {
			return
		}
		outcome := p.Forward()
		if outcome.Stop {
			return
		}
		if outcome.Err != nil {
			if outcome.Images {
				p.PartialImages(outcome.Err)
			} else {
				if outcome.Failure != nil {
					failure := outcome.Failure
					if !p.RetryReady(failure) {
						return
					}
					if failure.Policy == nil || !failure.Policy.RetryNext {
						p.Exhausted(failure)
						return
					}
					if options.FirstOutputBudget && failover.FirstOutputExhausted(outcome.FirstOutputRecovery, &firstOutputSwitches) {
						p.Exhausted(failure)
						return
					}
					retryLimit := failover.EffectiveSameAccountRetryLimit(failure.Policy, selected.RetryLimit)
					if failure.Policy.RetryableOnSameAccount && failover.SameAccountRetryAllowed(failure.Policy, sameAccount[selected.Account.ID], retryLimit) {
						sameAccount[selected.Account.ID]++
						delay := failover.SameAccountRetryDelayFor(failure.Policy, sameAccount[selected.Account.ID])
						p.RetryWait(failure, retryLimit, sameAccount[selected.Account.ID], delay)
						if !failover.SleepWithContext(p.Context(), delay) {
							return
						}
						continue
					}
					p.Switched()
					excluded[selected.Account.ID] = struct{}{}
					last = failure
					if switches >= options.MaxSwitches {
						p.Exhausted(failure)
						return
					}
					switches++
					if failover.StopOAuth429(selected.OAuth, failure.Policy.StatusCode, switches, &oauth) {
						p.Exhausted(failure)
						return
					}
					p.Switching(failure, switches, options.MaxSwitches)
					continue
				}
				p.OtherFailure(outcome.Err)
				p.Complete()
				p.Failed()
				return
			}
		}
		p.Success()
		p.Complete()
		p.Completed(switches)
		return
	}
}
