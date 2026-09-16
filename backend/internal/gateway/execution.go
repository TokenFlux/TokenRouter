package gateway

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// RequestMetadata 是 HTTP 适配在执行前取得的值快照；不保留响应器或请求对象。
type RequestMetadata = execution.RequestMetadata

// FundingState 固化已经认证且完成复合选择的访问投影与指定权益。
// 资金算法和最终授权仍由 billing 执行。
type FundingState = execution.FundingState

// ExecutionResult 独立返回最后一次已执行尝试，即使该尝试同时失败也保留观测用量。
type ExecutionResult = execution.ExecutionResult

// QoderRuntime 在组合根绑定一次；HTTP 调用只提供请求值与同步输出端口。
// 恢复和选择端口不拥有额外的账号尝试循环。
type QoderRuntime interface {
	Prepare(context.Context, Request) (Request, error)
	Check(context.Context, Request, bool) error
	Select(context.Context, Request, map[int64]struct{}) (*Selection, error)
	CanRefresh(error) bool
	CanFailover(error) bool
	RefreshPending(error) bool
	QueueFailure(string, error)
}

// ExecutionObserver 只同步报告 HTTP 观测和等待心跳，不交给异步完成任务。
type ExecutionObserver interface {
	Prepared(Request)
	Selected(account.AccountSnapshot)
	Waiting(string) scheduler.WaitObserver
}

// NewQoderExecutor 将固定依赖接到唯一的既有尝试状态机。
func NewQoderExecutor(maxAccounts int, waitTimeout time.Duration, concurrency *scheduler.ConcurrencyService, runtime QoderRuntime) *QoderUseCase {
	return &QoderUseCase{MaxAccounts: maxAccounts, WaitTimeout: waitTimeout, concurrency: concurrency, runtime: runtime}
}

// Execute 不要求调用者组装选择、刷新、计费或完成回调。
func (u *QoderUseCase) Execute(ctx context.Context, request Request, output upstream.OutputSink) (ExecutionResult, error) {
	var result ExecutionResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	prepared, err := u.runtime.Prepare(ctx, request)
	if err != nil {
		return result, err
	}
	observer, _ := output.(ExecutionObserver)
	if observer != nil {
		observer.Prepared(prepared)
	}
	ports := RequestPorts{Concurrency: u.concurrency, CanRefresh: u.runtime.CanRefresh, CanFailover: u.runtime.CanFailover, RefreshPending: u.runtime.RefreshPending, QueueFailure: u.runtime.QueueFailure}
	ports.Check = func(ctx context.Context, afterWait bool) error { return u.runtime.Check(ctx, prepared, afterWait) }
	ports.Select = func(ctx context.Context, excluded map[int64]struct{}) (*Selection, error) {
		selected, err := u.runtime.Select(ctx, prepared, excluded)
		if err != nil {
			return nil, err
		}
		return selected, nil
	}
	if observer != nil {
		ports.WaitObserver = observer.Waiting
	}
	tracker := &OutputTracker{Sink: output}
	err = u.run(ctx, prepared, ports, tracker, &result)
	return result, err
}
