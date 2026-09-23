//go:build unit

package testkit

import (
	"context"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// ErrorPolicyStore 记录错误策略的实际写入，不代替策略决策。
type ErrorPolicyStore struct {
	HealthStoreBase
	TempCalls           int
	SetErrCalls         int
	LastErrorMsg        string
	ModelRateLimitCalls []ModelLimitCall
}

func (r *ErrorPolicyStore) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.TempCalls++
	return nil
}

func (r *ErrorPolicyStore) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.SetErrCalls++
	r.LastErrorMsg = errorMsg
	return nil
}

func (r *ErrorPolicyStore) SetModelRateLimit(_ context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	call := ModelLimitCall{AccountID: id, Scope: scope, ResetAt: resetAt}
	if len(reason) > 0 {
		call.Reason = reason[0]
	}
	r.ModelRateLimitCalls = append(r.ModelRateLimitCalls, call)
	return nil
}

// HealthStoreRecorder 保留原健康字段写入次数、错误注入和最新参数。
type HealthStoreRecorder struct {
	HealthStoreBase
	SetErrorCalls          int
	TempCalls              int
	UpdateCredentialsCalls int
	UpdateExtraCalls       int
	LastCredentials        map[string]any
	LastExtraUpdates       map[string]any
	LastErrorMsg           string
	LastTempUntil          time.Time
	LastTempReason         string
	LastErrorID            int64
	LastTempID             int64
	TempErr                error
}

func (r *HealthStoreRecorder) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.SetErrorCalls++
	r.LastErrorID = id
	r.LastErrorMsg = errorMsg
	return nil
}

func (r *HealthStoreRecorder) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.TempCalls++
	r.LastTempUntil = until
	r.LastTempID = id
	r.LastTempReason = reason
	return r.TempErr
}

func (r *HealthStoreRecorder) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	r.UpdateCredentialsCalls++
	r.LastCredentials = querycache.ShallowMap(credentials)
	return nil
}

func (r *HealthStoreRecorder) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.UpdateExtraCalls++
	r.LastExtraUpdates = querycache.ShallowMap(updates)
	return nil
}

// ForbiddenCounter 保留预设计数序列与重置记录。
type ForbiddenCounter struct {
	Counts     []int64
	ResetCalls []int64
	Err        error
}

func (s *ForbiddenCounter) IncrementOpenAI403Count(_ context.Context, _ int64, _ int) (int64, error) {
	if s.Err != nil {
		return 0, s.Err
	}
	if len(s.Counts) == 0 {
		return 1, nil
	}
	count := s.Counts[0]
	s.Counts = s.Counts[1:]
	return count, nil
}

func (s *ForbiddenCounter) ResetOpenAI403Count(_ context.Context, accountID int64) error {
	s.ResetCalls = append(s.ResetCalls, accountID)
	return nil
}

// RuntimeBlockRecorder 记录运行时阻断和清理，不创建另一份健康状态。
type RuntimeBlockRecorder struct {
	Accounts   []*gatewayprovider.ExecutionAccount
	Until      []time.Time
	Reasons    []string
	ClearedIDs []int64
}

func (r *RuntimeBlockRecorder) BlockAccountScheduling(account *gatewayprovider.ExecutionAccount, until time.Time, reason string) {
	r.Accounts = append(r.Accounts, account)
	r.Until = append(r.Until, until)
	r.Reasons = append(r.Reasons, reason)
}

func (r *RuntimeBlockRecorder) ClearAccountSchedulingBlock(accountID int64) {
	r.ClearedIDs = append(r.ClearedIDs, accountID)
}

// ModelLimitCall 记录模型窗口写入的原字段。
type ModelLimitCall struct {
	AccountID int64
	Scope     string
	ResetAt   time.Time
	Reason    string
}
