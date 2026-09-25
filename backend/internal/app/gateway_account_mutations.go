// 执行账号适配只引用原生存储，事务、事件、缓存和资金规则均由其实际拥有者执行。
package app

import (
	context "context"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// UpdateConfiguration 只投影本次配置意图；事务、锁和资金字段保护由账号存储执行。
func (r *executionAccountStore) UpdateConfiguration(ctx context.Context, value *gatewayprovider.ExecutionAccount, change accountcore.ConfigurationChange) error {
	v := gatewayprovider.ExecutionRecord(value)
	err := r.data.UpdateConfiguration(ctx, v, change)
	gatewayprovider.ApplyExecutionRecord(value, v)
	return err
}

// ApplyManagedRecoveryStep 委托唯一账号存储，不在旧入口复制条件或提交规则。
func (r *executionAccountStore) ApplyManagedRecoveryStep(ctx context.Context, step accountcore.ManagedRecoveryStep, v accountcore.ManagedRecoveryVersion) (bool, error) {
	return r.data.ApplyManagedRecoveryStep(ctx, step, v)
}

// UpdateOAuthCredentialsIfUnchanged 委托账号存储比较凭据身份，并在同一事务中写入凭据和 outbox。
func (r *executionAccountStore) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version accountcore.CredentialVersion, credentials map[string]any) (bool, error) {
	return r.data.UpdateOAuthCredentialsIfUnchanged(ctx, version, credentials)
}

func (r *executionAccountStore) Update(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	v := gatewayprovider.ExecutionRecord(account)
	err := r.data.Update(ctx, v)
	gatewayprovider.ApplyExecutionRecord(account, v)
	return err
}

func (r *executionAccountStore) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	return r.data.UpdateCredentials(ctx, id, credentials)
}

func (r *executionAccountStore) UpdateLastUsed(ctx context.Context, id int64) error {
	return r.data.UpdateLastUsed(ctx, id)
}

func (r *executionAccountStore) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return r.data.BatchUpdateLastUsed(ctx, updates)
}

func (r *executionAccountStore) SetError(ctx context.Context, id int64, errorMsg string) error {
	return r.data.SetError(ctx, id, errorMsg)
}

func (r *executionAccountStore) UpdateGrokOAuthCredentialsIfUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	return r.data.UpdateGrokOAuthCredentialsIfUnchanged(ctx, id, expectedCredentials, expectedProxyID, credentials)
}

func (r *executionAccountStore) ClearError(ctx context.Context, id int64) error {
	return r.data.ClearError(ctx, id)
}

func (r *executionAccountStore) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return r.data.SetRateLimited(ctx, id, resetAt)
}

func (r *executionAccountStore) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.data.SetRateLimitedIfLater(ctx, id, resetAt)
}

func (r *executionAccountStore) ClearRateLimitIfObserved(ctx context.Context, id int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	return r.data.ClearRateLimitIfObserved(ctx, id, observedLimitedAt, observedResetAt)
}

func (r *executionAccountStore) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	return r.data.SetModelRateLimit(ctx, id, scope, resetAt, reason...)
}

func (r *executionAccountStore) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return r.data.SetOverloaded(ctx, id, until)
}

func (r *executionAccountStore) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return r.data.SetTempUnschedulable(ctx, id, until, reason)
}

func (r *executionAccountStore) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return r.data.ClearTempUnschedulable(ctx, id)
}

func (r *executionAccountStore) ClearRateLimit(ctx context.Context, id int64) error {
	return r.data.ClearRateLimit(ctx, id)
}

func (r *executionAccountStore) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return r.data.ClearAntigravityQuotaScopes(ctx, id)
}

func (r *executionAccountStore) ClearModelRateLimits(ctx context.Context, id int64) error {
	return r.data.ClearModelRateLimits(ctx, id)
}

func (r *executionAccountStore) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return r.data.UpdateSessionWindow(ctx, id, start, end, status)
}

func (r *executionAccountStore) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	return r.data.UpdateSessionWindowEnd(ctx, id, end)
}

func (r *executionAccountStore) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return r.data.UpdateExtra(ctx, id, updates)
}

func (r *executionAccountStore) UpdateCNUsageMonitorSnapshotCAS(
	ctx context.Context,
	accountID int64,
	expectedUpdatedAt time.Time,
	snapshot *accountcore.CNUsageMonitorSnapshot,
	clearExtraKey string,
) (bool, error) {
	return r.data.UpdateCNUsageMonitorSnapshotCAS(ctx, accountID, expectedUpdatedAt, snapshot, clearExtraKey)
}

// IncrementQuotaUsed 原子递增账号的配额用量（总/日/周三个维度）
// 日/周额度在周期过期时自动重置为 0 再递增。
// 支持滚动窗口（rolling）和固定时间（fixed）两种重置模式。
func (r *executionAccountStore) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return r.usage.IncrementQuotaUsed(ctx, id, amount)
}

func (r *executionAccountStore) BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	return r.data.BulkUpdate(ctx, ids, updates)
}

// 用量观察结果通过账号存储的条件操作写入，避免覆盖已变更的账号身份。
func (r *executionAccountStore) UpdateUsageExtraIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, updates map[string]any) (bool, error) {
	return r.data.UpdateUsageExtraIfUnchanged(ctx, v, updates)
}

func (r *executionAccountStore) SetUsageRateLimitIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, reset time.Time) (bool, error) {
	return r.data.SetUsageRateLimitIfUnchanged(ctx, v, reset)
}

func (r *executionAccountStore) ClearUsageRateLimitIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion) (bool, error) {
	return r.data.ClearUsageRateLimitIfUnchanged(ctx, v)
}

// ClearUsageErrorIfUnchanged 只转交账号存储；原查询用例不得无条件恢复已变化身份。
func (r *executionAccountStore) ClearUsageErrorIfUnchanged(ctx context.Context, v accountcore.UsageRecoveryVersion) (bool, error) {
	return r.data.ClearUsageErrorIfUnchanged(ctx, v)
}

// UpdateUsageSessionWindowEndIfUnchanged 旧入口只转交唯一账号存储。
func (r *executionAccountStore) UpdateUsageSessionWindowEndIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, observed *time.Time, end time.Time) (bool, error) {
	return r.data.UpdateUsageSessionWindowEndIfUnchanged(ctx, v, observed, end)
}
