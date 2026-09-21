// Package repository 实现数据访问层（Repository Pattern）。
//
// 该包提供了与数据库交互的所有操作，包括 CRUD、复杂查询和批量操作。
// 采用 Repository 模式将数据访问逻辑与业务逻辑分离，便于测试和维护。
//
// 主要特性：
//   - 使用 Ent ORM 进行类型安全的数据库操作
//   - 对于复杂查询（如批量更新、聚合统计）使用原生 SQL
//   - 提供统一的错误翻译机制，将数据库错误转换为业务错误
//   - 支持软删除，所有查询自动过滤已删除记录
package repository

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	context "context"

	sql "database/sql"

	time "time"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// accountRepository 实现 service.AccountRepository 接口。
// 提供 AI API 账户的完整数据访问功能。
//
// 设计说明：
//   - client: Ent 客户端，用于类型安全的 ORM 操作
//   - sql: 原生 SQL 执行器，用于复杂查询和批量操作
//   - schedulerCache: 调度器缓存，用于在账号状态变更时同步快照
type accountRepository struct {
	usage  *billingpostgres.AccountUsageStore
	data   *accountpostgres.AccountStore
	client *dbent.Client     // Ent ORM 客户端
	sql    postgres.Executor // 原生 SQL 执行接口
	// schedulerCache 用于在账号状态变更时主动同步快照到缓存，
	// 确保粘性会话能及时感知账号不可用状态。
	// Used to proactively sync account snapshot to cache when status changes,
	// ensuring sticky sessions can promptly detect unavailable accounts.
	schedulerCache service.SchedulerCache
}

const (
	// 废弃账号扩展键仅用于写入边界清理，仓储不得再赋予它们业务语义。
	deprecatedUpstreamBillingProbeExtraKey        = "upstream_billing_probe"
	deprecatedUpstreamBillingProbeEnabledExtraKey = "upstream_billing_probe_enabled"
	deprecatedOpenAILongContextBillingExtraKey    = "openai_long_context_billing_enabled"
)

// NewAccountRepository 创建账户仓储实例。
// 这是对外暴露的构造函数，返回接口类型以便于依赖注入。
func NewAccountRepository(client *dbent.Client, sqlDB *sql.DB, schedulerCache service.SchedulerCache) service.AccountRepository {
	return newAccountRepositoryWithSQL(client, sqlDB, schedulerCache)
}

// newAccountRepositoryWithSQL 是内部构造函数，支持依赖注入 SQL 执行器。
// 这种设计便于单元测试时注入 mock 对象。
func newAccountRepositoryWithSQL(client *dbent.Client, sqlq postgres.Executor, schedulerCache service.SchedulerCache) *accountRepository {
	return &accountRepository{client: client, sql: sqlq, schedulerCache: schedulerCache}
}

func (r *accountRepository) Create(ctx context.Context, account *service.Account) error {
	v := service.AccountRecordView(account)
	err := r.accountData().Create(ctx, v)
	service.ApplyAccountRecord(account, v)
	return err
}

func (r *accountRepository) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	v, err := r.accountData().GetByID(ctx, id)
	return service.AccountFromRecord(v), err
}

func (r *accountRepository) GetByIDs(ctx context.Context, ids []int64) ([]*service.Account, error) {
	values, err := r.accountData().GetByIDs(ctx, ids)
	if values == nil {
		return nil, err
	}
	out := make([]*service.Account, len(values))
	for i := range values {
		out[i] = service.AccountFromRecord(values[i])
	}
	return out, err
}

func (r *accountRepository) ExistsByID(ctx context.Context, id int64) (bool, error) {
	return r.accountData().ExistsByID(ctx, id)
}

func (r *accountRepository) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*service.Account, error) {
	v, err := r.accountData().GetByCRSAccountID(ctx, crsAccountID)
	return service.AccountFromRecord(v), err
}

func (r *accountRepository) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return r.accountData().ListCRSAccountIDs(ctx)
}

func (r *accountRepository) Update(ctx context.Context, account *service.Account) error {
	v := service.AccountRecordView(account)
	err := r.accountData().Update(ctx, v)
	service.ApplyAccountRecord(account, v)
	return err
}

func (r *accountRepository) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	return r.accountData().UpdateCredentials(ctx, id, credentials)
}

func (r *accountRepository) Delete(ctx context.Context, id int64) error {
	return r.accountData().Delete(ctx, id)
}

func (r *accountRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.Account, *pagination.PaginationResult, error) {
	v, p, err := r.accountData().List(ctx, params)
	return service.AccountsFromRecords(v), p, err
}

func (r *accountRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]service.Account, *pagination.PaginationResult, error) {
	v, p, err := r.accountData().ListWithFilters(ctx, params, platform, accountType, status, search, groupID, privacyMode)
	return service.AccountsFromRecords(v), p, err
}

func (r *accountRepository) ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]service.Account, error) {
	v, err := r.accountData().ListAllWithFilters(ctx, platform, accountType, status, search, groupID, privacyMode)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListOpsAccountsForStats(ctx context.Context, platformFilter string, groupIDFilter *int64) ([]service.Account, error) {
	v, err := r.accountData().ListOpsAccountsForStats(ctx, platformFilter, groupIDFilter)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListByGroup(ctx context.Context, groupID int64) ([]service.Account, error) {
	v, err := r.accountData().ListByGroup(ctx, groupID)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListActive(ctx context.Context) ([]service.Account, error) {
	v, err := r.accountData().ListActive(ctx)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListOAuthRefreshCandidatePage(ctx context.Context, options accountcore.OAuthRefreshPageOptions) (*service.OAuthRefreshCandidatePage, error) {
	v, err := r.accountData().ListOAuthRefreshCandidatePage(ctx, options)
	if v == nil {
		return nil, err
	}
	return &service.OAuthRefreshCandidatePage{Accounts: service.AccountsFromRecords(v.Accounts), NextAfterID: v.NextAfterID, HasMore: v.HasMore}, err
}

func (r *accountRepository) ListByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	v, err := r.accountData().ListByPlatform(ctx, platform)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) UpdateLastUsed(ctx context.Context, id int64) error {
	return r.accountData().UpdateLastUsed(ctx, id)
}

func (r *accountRepository) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return r.accountData().BatchUpdateLastUsed(ctx, updates)
}

func (r *accountRepository) SetError(ctx context.Context, id int64, errorMsg string) error {
	return r.accountData().SetError(ctx, id, errorMsg)
}

func (r *accountRepository) SetGrokCredentialErrorIfMatch(
	ctx context.Context,
	id int64,
	snapshot accountcore.CredentialMutationSnapshot,
	errorMsg string,
) (bool, error) {
	return r.accountData().SetGrokCredentialErrorIfMatch(ctx, id, snapshot, errorMsg, string(forwardcore.GrokCredentialReasonProxyInvalid))
}

func (r *accountRepository) SetGrokOAuthErrorIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	errorMsg string,
) (bool, error) {
	return r.accountData().SetGrokOAuthErrorIfCredentialsUnchanged(ctx, id, expectedCredentials, errorMsg)
}

func (r *accountRepository) UpdateGrokOAuthCredentialsIfUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	return r.accountData().UpdateGrokOAuthCredentialsIfUnchanged(ctx, id, expectedCredentials, expectedProxyID, credentials)
}

func (r *accountRepository) SetGrokOAuthRefreshErrorIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	errorMsg string,
) (bool, error) {
	return r.accountData().SetGrokOAuthRefreshErrorIfCredentialsUnchanged(ctx, id, expectedCredentials, expectedProxyID, errorMsg)
}

func (r *accountRepository) SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	until time.Time,
	reason string,
) (bool, error) {
	return r.accountData().SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(ctx, id, expectedCredentials, expectedProxyID, until, reason)
}

func (r *accountRepository) ClearError(ctx context.Context, id int64) error {
	return r.accountData().ClearError(ctx, id)
}

func (r *accountRepository) AddToGroup(ctx context.Context, accountID, groupID int64) error {
	return r.accountData().AddToGroup(ctx, accountID, groupID)
}

func (r *accountRepository) RemoveFromGroup(ctx context.Context, accountID, groupID int64) error {
	return r.accountData().RemoveFromGroup(ctx, accountID, groupID)
}

func (r *accountRepository) GetGroups(ctx context.Context, accountID int64) ([]routing.Group, error) {
	values, err := r.accountData().GetGroups(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]routing.Group, 0, len(values))
	for i := range values {
		out = append(out, *routing.CloneGroup((*routing.Group)(&values[i])))
	}
	return out, nil
}

func (r *accountRepository) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return r.accountData().BindGroups(ctx, accountID, groupIDs)
}

func (r *accountRepository) ListSchedulable(ctx context.Context) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulable(ctx)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableAccountLoads(ctx context.Context) ([]scheduler.AccountWithConcurrency, error) {
	rows, err := r.accountData().ListSchedulableAccountLoads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.AccountWithConcurrency, len(rows))
	for i, v := range rows {
		out[i] = scheduler.AccountWithConcurrency{ID: v.ID, MaxConcurrency: v.MaxConcurrency}
	}
	return out, nil
}

func (r *accountRepository) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableByGroupID(ctx, groupID)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableCapacityByGroupIDs(ctx context.Context, groupIDs []int64) ([]accountcore.GroupAccountCapacityRow, error) {
	return r.accountData().ListSchedulableCapacityByGroupIDs(ctx, groupIDs)
}

func (r *accountRepository) ListSchedulableByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableByPlatform(ctx, platform)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableByPlatforms(ctx, platforms)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableUngroupedByPlatform(ctx, platform)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]service.Account, error) {
	v, err := r.accountData().ListSchedulableByGroupIDAndPlatforms(ctx, groupID, platforms)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) ListModelAvailabilityCandidates(
	ctx context.Context,
	groupID *int64,
	platforms []string,
	includeGrouped bool,
) ([]service.Account, error) {
	v, err := r.accountData().ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
	return service.AccountsFromRecords(v), err
}

func (r *accountRepository) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return r.accountData().SetRateLimited(ctx, id, resetAt)
}

func (r *accountRepository) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.accountData().SetRateLimitedIfLater(ctx, id, resetAt)
}

func (r *accountRepository) ClearRateLimitIfObserved(ctx context.Context, id int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	return r.accountData().ClearRateLimitIfObserved(ctx, id, observedLimitedAt, observedResetAt)
}

func (r *accountRepository) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	return r.accountData().SetModelRateLimit(ctx, id, scope, resetAt, reason...)
}

func (r *accountRepository) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return r.accountData().SetOverloaded(ctx, id, until)
}

func (r *accountRepository) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return r.accountData().SetTempUnschedulable(ctx, id, until, reason)
}

func (r *accountRepository) SetGrokCredentialTempUnschedulableIfMatch(
	ctx context.Context,
	id int64,
	snapshot accountcore.CredentialMutationSnapshot,
	until time.Time,
	reason string,
) (bool, error) {
	return r.accountData().SetGrokCredentialTempUnschedulableIfMatch(ctx, id, snapshot, until, reason)
}

func (r *accountRepository) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return r.accountData().ClearTempUnschedulable(ctx, id)
}

func (r *accountRepository) ClearRateLimit(ctx context.Context, id int64) error {
	return r.accountData().ClearRateLimit(ctx, id)
}

func (r *accountRepository) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return r.accountData().ClearAntigravityQuotaScopes(ctx, id)
}

func (r *accountRepository) ClearModelRateLimits(ctx context.Context, id int64) error {
	return r.accountData().ClearModelRateLimits(ctx, id)
}

func (r *accountRepository) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return r.accountData().UpdateSessionWindow(ctx, id, start, end, status)
}

func (r *accountRepository) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	return r.accountData().UpdateSessionWindowEnd(ctx, id, end)
}

func (r *accountRepository) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return r.accountData().SetSchedulable(ctx, id, schedulable)
}

func (r *accountRepository) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	return r.accountData().AutoPauseExpiredAccounts(ctx, now)
}

func (r *accountRepository) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return r.accountData().UpdateExtra(ctx, id, updates)
}

func (r *accountRepository) UpdateCNUsageMonitorSnapshotCAS(
	ctx context.Context,
	accountID int64,
	expectedUpdatedAt time.Time,
	snapshot *accountcore.CNUsageMonitorSnapshot,
	clearExtraKey string,
) (bool, error) {
	return r.accountData().UpdateCNUsageMonitorSnapshotCAS(ctx, accountID, expectedUpdatedAt, snapshot, clearExtraKey)
}

func (r *accountRepository) BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	return r.accountData().BulkUpdate(ctx, ids, updates)
}

func (r *accountRepository) accountsToService(ctx context.Context, accounts []*dbent.Account) ([]service.Account, error) {
	v, err := r.accountData().RecordsFromEntities(ctx, accounts)
	return service.AccountsFromRecords(v), err
}

// buildSchedulerGroupPayload 构造 EventAccountChanged / EventAccountGroupsChanged
// 事件的 payload。空 groupIDs 必须返回 untyped nil（any 而非 map[string]any(nil)），
// 否则 enqueueSchedulerOutbox 的 "payload != nil" 接口判空会被 typed-nil 欺骗，
// 把 payload marshal 成 "null" 写入 dedup_key 哈希，破坏与其他 nil-payload 调用的去重一致性。
func buildSchedulerGroupPayload(groupIDs []int64) any { return scheduler.GroupPayload(groupIDs) }

func accountEntityToService(m *dbent.Account) *service.Account {
	return service.AccountFromRecord(accountpostgres.RecordFromEntity(m))
}

func (r *accountRepository) FindByExtraField(ctx context.Context, key string, value any) ([]service.Account, error) {
	v, err := r.accountData().FindByExtraField(ctx, key, value)
	return service.AccountsFromRecords(v), err
}

// IncrementQuotaUsed 原子递增账号的配额用量（总/日/周三个维度）
// 日/周额度在周期过期时自动重置为 0 再递增。
// 支持滚动窗口（rolling）和固定时间（fixed）两种重置模式。
func (r *accountRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return r.accountUsage().IncrementQuotaUsed(ctx, id, amount)
}

// ResetQuotaUsedAndClearRateLimitCooldown 委托资金写入，保留原账号事件与快照发布顺序。
func (r *accountRepository) ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error {
	return r.accountUsage().ResetQuotaUsedAndClearRateLimitCooldown(ctx, id)
}

func (r *accountRepository) RevertProxyFallback(ctx context.Context, accountID int64) error {
	return r.accountData().RevertProxyFallback(ctx, accountID)
}

func (r *accountRepository) ListShadowsByParent(ctx context.Context, parentID int64) ([]*service.Account, error) {
	v, err := r.accountData().ListShadowsByParent(ctx, parentID)
	if v == nil {
		return nil, err
	}
	out := make([]*service.Account, len(v))
	for i := range v {
		out[i] = service.AccountFromRecord(v[i])
	}
	return out, err
}
