package text

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
)

// InputTokensPorts 不提供用户并发、资金提交或完成任务；选择器若交付账号槽，单次 Forward 负责释放。
type InputTokensPorts interface {
	Context() context.Context
	Select(map[int64]struct{}) (Selection, bool, error)
	SelectionFailed(error, *AttemptFailure, bool)
	Forward(Selection) *AttemptFailure
	ForwardFailed(Selection, error)
	Exhausted(*AttemptFailure)
}

// RunInputTokens 保留原端点独立预算，不继承普通生成请求的 429/首输出恢复策略。
func RunInputTokens(p InputTokensPorts, maxSwitches int) {
	excluded := make(map[int64]struct{})
	same := make(map[int64]int)
	var last *AttemptFailure
	switches := 0
	for {
		selected, available, err := p.Select(excluded)
		if err != nil || !available {
			p.SelectionFailed(err, last, available)
			return
		}
		failure := p.Forward(selected)
		if failure == nil {
			return
		}
		if failure.Policy == nil {
			p.ForwardFailed(selected, failure.Cause)
			return
		}
		if !failure.Policy.RetryNext {
			p.Exhausted(failure)
			return
		}
		if failure.Policy.RetryableOnSameAccount && same[selected.Account.ID] < selected.RetryLimit {
			same[selected.Account.ID]++
			if !failover.SleepWithContext(p.Context(), failover.SameAccountRetryDelay) {
				return
			}
			continue
		}
		excluded[selected.Account.ID] = struct{}{}
		last = failure
		if switches >= maxSwitches {
			p.Exhausted(failure)
			return
		}
		switches++
	}
}
