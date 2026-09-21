//go:build unit

package batchimage_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// recoveryFundsFixture 保留原恢复测试的释放记录、错误及请求去重，不提供无关结算入口。
type recoveryFundsFixture struct {
	releases   []*billing.TaskFundsCommand
	releaseErr error
	seen       map[string]struct{}
}

func (r *recoveryFundsFixture) Reserve(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	panic("unexpected Reserve")
}
func (r *recoveryFundsFixture) Capture(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	panic("unexpected Capture")
}
func (r *recoveryFundsFixture) Release(_ context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if r.releaseErr != nil {
		r.releases = append(r.releases, cmd)
		return nil, r.releaseErr
	}
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	if cmd != nil {
		cmd.Normalize()
		if _, ok := r.seen[cmd.RequestID]; ok {
			r.releases = append(r.releases, cmd)
			return &billing.TaskFundsResult{Applied: false}, nil
		}
		r.seen[cmd.RequestID] = struct{}{}
	}
	r.releases = append(r.releases, cmd)
	return &billing.TaskFundsResult{Applied: true}, nil
}
