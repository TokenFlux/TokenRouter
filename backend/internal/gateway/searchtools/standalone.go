package searchtools

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

// Selection 只携带选中标识和取得资源，不向核心暴露账号凭据或旧实体。
type Selection struct {
	AccountID int64
	Acquired  bool
	Release   func()
	WaitPlan  *scheduler.AccountWaitPlan
}
type StandalonePorts interface {
	Select(context.Context, string, map[int64]struct{}) (Selection, bool, error)
	Acquire(context.Context, Selection) (func(), bool, error)
	Execute(context.Context, int64, StandaloneRequest, string, int) (*contract.SearchResponse, string, error)
	CanSwitch(error) bool
}
type StandaloneResult struct {
	AccountID int64
	Response  *contract.SearchResponse
	Provider  string
}
type StandaloneFailure struct {
	Stage string
	Cause error
}

func (e *StandaloneFailure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "web search failed"
}
func (e *StandaloneFailure) Unwrap() error { return e.Cause }

// noAvailableAccountsError 保留原 HTTP 使用的首字母大写消息，表示选择阶段没有账号。
type noAvailableAccountsError struct{}

func (noAvailableAccountsError) Error() string { return "No available accounts" }

// RunStandalone 保留最多四次账号选择、首次失败映射及后续 failover 的原有区别。
// 请求 Lease 由 HTTP 在写完响应后释放；失败切换只提前释放当前 attempt。
func RunStandalone(ctx context.Context, request StandaloneRequest, model string, maxResults int, ports StandalonePorts, lease *scheduler.Lease) (StandaloneResult, error) {
	failed := make(map[int64]struct{})
	var result StandaloneResult
	hasAccount := false
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		selection, present, err := ports.Select(ctx, model, failed)
		if err != nil {
			if attempt == 0 {
				return result, &StandaloneFailure{Stage: "selection", Cause: err}
			}
			break
		}
		if !present {
			if attempt == 0 {
				return result, &StandaloneFailure{Stage: "selection", Cause: noAvailableAccountsError{}}
			}
			break
		}
		release, ok, err := ports.Acquire(ctx, selection)
		if !ok {
			if attempt == 0 && err != nil {
				return result, &StandaloneFailure{Stage: "concurrency", Cause: err}
			}
			failed[selection.AccountID] = struct{}{}
			continue
		}
		attemptLease := scheduler.NewAttemptLease(lease, nil, release)
		result.AccountID = selection.AccountID
		hasAccount = true
		result.Response, result.Provider, lastErr = ports.Execute(ctx, selection.AccountID, request, model, maxResults)
		if lastErr == nil {
			break
		}
		if !ports.CanSwitch(lastErr) {
			break
		}
		failed[selection.AccountID] = struct{}{}
		attemptLease.Release()
		result.AccountID = 0
		hasAccount = false
	}
	if lastErr != nil || result.Response == nil {
		return result, &StandaloneFailure{Stage: "execute", Cause: lastErr}
	}
	if !hasAccount {
		return result, &StandaloneFailure{Stage: "selection", Cause: noAvailableAccountsError{}}
	}
	return result, nil
}
