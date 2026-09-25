package billing

import (
	context "context"
	"fmt"
)

// FundsStore 以闭合命令隐藏 SQL 锁序及持久化细节。
type FundsStore interface {
	Apply(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error)
	Reserve(context.Context, *TaskFundsCommand) (*TaskFundsResult, error)
	Capture(context.Context, *TaskFundsCommand) (*TaskFundsResult, error)
	Release(context.Context, *TaskFundsCommand) (*TaskFundsResult, error)
}

// Funds 是普通结算与任务资金的唯一入口。
type Funds struct{ store FundsStore }

func NewFunds(store FundsStore) *Funds { return &Funds{store: store} }
func (f *Funds) Settle(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	return f.store.Apply(ctx, cmd)
}
func (f *Funds) Reserve(ctx context.Context, cmd *TaskFundsCommand) (*TaskFundsResult, error) {
	if cmd != nil && (cmd.Task.Scope == "" || cmd.Task.ReserveRequestID == "") {
		return nil, fmt.Errorf("billing task reference is incomplete: %s", cmd.Task.Scope)
	}
	return f.store.Reserve(ctx, cmd)
}
func (f *Funds) Capture(ctx context.Context, cmd *TaskFundsCommand) (*TaskFundsResult, error) {
	if cmd != nil && (cmd.Task.Scope == "" || cmd.Task.ReserveRequestID == "") {
		return nil, fmt.Errorf("billing task reference is incomplete: %s", cmd.Task.Scope)
	}
	return f.store.Capture(ctx, cmd)
}
func (f *Funds) Release(ctx context.Context, cmd *TaskFundsCommand) (*TaskFundsResult, error) {
	if cmd != nil && (cmd.Task.Scope == "" || cmd.Task.ReserveRequestID == "") {
		return nil, fmt.Errorf("billing task reference is incomplete: %s", cmd.Task.Scope)
	}
	return f.store.Release(ctx, cmd)
}
