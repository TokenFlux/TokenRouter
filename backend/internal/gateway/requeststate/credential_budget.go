package requeststate

import (
	"context"
	"time"
)

// CredentialBudget 属于整个请求，换号时继续使用第一次凭据获取建立的截止时间。
// nil 状态保留无 HTTP 交换时不另加预算的既有行为。
type CredentialBudget struct{ Deadline time.Time }

func (state *CredentialBudget) Acquire(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if state == nil {
		return ctx, nil, false
	}
	deadline := state.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(15 * time.Second)
		state.Deadline = deadline
	}
	if !time.Now().Before(deadline) {
		return ctx, nil, true
	}
	acquireCtx, cancel := context.WithDeadline(ctx, deadline)
	return acquireCtx, cancel, false
}
