package text

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
)

// CountPorts 只提供无槽选择、单次计数与失败清理；不存在资金提交或完成任务入口。
type CountPorts interface {
	Context() context.Context
	Select(map[int64]struct{}) (Selection, error)
	SelectionFailed(error, *AttemptFailure)
	Prepare(Selection) bool
	Forward(Selection) *AttemptFailure
	ForwardFailed(Selection, error)
	ReleaseSession(Selection)
	Exhausted(Selection, *AttemptFailure)
	Canceled()
	failover.TempUnscheduler[*AttemptFailure]
}

// RunCountTokens 保留辅助计数的单一重试循环；每次尝试重新构造报文，不取得请求槽。
func RunCountTokens(p CountPorts, maxSwitches int, observe failover.Observe) {
	state := failover.NewFailoverState[*AttemptFailure](maxSwitches, false, observe)
	for {
		selected, err := p.Select(state.FailedAccountIDs)
		if err != nil {
			p.SelectionFailed(err, state.LastFailoverErr)
			return
		}
		if !p.Prepare(selected) {
			return
		}
		failure := p.Forward(selected)
		if failure == nil {
			return
		}
		if failure.Policy == nil {
			p.ForwardFailed(selected, failure.Cause)
			p.ReleaseSession(selected)
			return
		}
		action := state.HandleFailoverError(p.Context(), p, selected.Account.ID, selected.Account.Platform, selected.RetryLimit, failure)
		p.ReleaseSession(selected)
		switch action {
		case failover.FailoverContinue:
			continue
		case failover.FailoverCanceled:
			p.Canceled()
			return
		default:
			p.Exhausted(selected, state.LastFailoverErr)
			return
		}
	}
}
