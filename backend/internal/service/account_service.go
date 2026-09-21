package service

import (
	context "context"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// OAuthRefreshCandidatePage 保留原始 SQL ID 页面的游标元数据。
// 即使详情加载时记录被并发删除，调用方仍可越过原始页面继续扫描，避免截断或重复。
type OAuthRefreshCandidatePage struct {
	Accounts    []Account
	NextAfterID int64
	HasMore     bool
}

// OAuthRefreshCandidatePager 是刻意窄于 AccountRepository 的有界分页接口。
// 生产刷新周期在仓储未实现该契约时会安全失败，不会静默退回无分页扫描。
type OAuthRefreshCandidatePager interface {
	ListOAuthRefreshCandidatePage(ctx context.Context, options accountcore.OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error)
}

type AccountRepository interface {
	Create(ctx context.Context, account *Account) error
	GetByID(ctx context.Context, id int64) (*Account, error)
	// GetByIDs fetches accounts by IDs in a single query.
	// It should return all accounts found (missing IDs are ignored).
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
	// ExistsByID 检查账号是否存在，仅返回布尔值，用于删除前的轻量级存在性检查
	ExistsByID(ctx context.Context, id int64) (bool, error)
	// GetByCRSAccountID finds an account previously synced from CRS.
	// Returns (nil, nil) if not found.
	GetByCRSAccountID(ctx context.Context, crsAccountID string) (*Account, error)
	// FindByExtraField 根据 extra 字段中的键值对查找账号
	FindByExtraField(ctx context.Context, key string, value any) ([]Account, error)
	// ListCRSAccountIDs returns a map of crs_account_id -> local account ID
	// for all accounts that have been synced from CRS.
	ListCRSAccountIDs(ctx context.Context) (map[string]int64, error)
	Update(ctx context.Context, account *Account) error
	Delete(ctx context.Context, id int64) error

	List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error)
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, *pagination.PaginationResult, error)
	// ListAllWithFilters 返回符合过滤条件的全部账号（不分页），用于账号列表页
	// 计算 OpenAI 调度分数的过滤范围池。
	ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
	ListByGroup(ctx context.Context, groupID int64) ([]Account, error)
	ListActive(ctx context.Context) ([]Account, error)
	ListByPlatform(ctx context.Context, platform string) ([]Account, error)

	UpdateLastUsed(ctx context.Context, id int64) error
	BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error
	SetError(ctx context.Context, id int64, errorMsg string) error
	ClearError(ctx context.Context, id int64) error
	SetSchedulable(ctx context.Context, id int64, schedulable bool) error
	AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error)
	BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error

	ListSchedulable(ctx context.Context) ([]Account, error)
	ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error)
	ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error)
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
	ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error)
	ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error)
	ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error)
	ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error)
	// ListModelAvailabilityCandidates 返回持久配置为 active 且 schedulable 的模型诊断候选账号，
	// 不过滤限流、过载、临时不可调度或到期窗口等瞬时状态。groupID 为 nil 时，
	// includeGrouped 决定查询全部匹配账号，还是只查询没有分组绑定的账号。
	ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error)

	SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error
	SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error
	SetOverloaded(ctx context.Context, id int64, until time.Time) error
	SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error
	ClearTempUnschedulable(ctx context.Context, id int64) error
	ClearRateLimit(ctx context.Context, id int64) error
	ClearAntigravityQuotaScopes(ctx context.Context, id int64) error
	ClearModelRateLimits(ctx context.Context, id int64) error
	UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error
	// UpdateSessionWindowEnd 仅更新 5h 窗口的结束时间，不动 start / status。
	// 用于 active poll 拿到新 ResetsAt 后回写，避免覆盖请求路径上记录的 status。
	UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error
	UpdateExtra(ctx context.Context, id int64, updates map[string]any) error
	BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error)
	// IncrementQuotaUsed 原子递增 API Key 账号的配额用量（总/日/周）
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error
	// ResetQuotaUsedAndClearRateLimitCooldown atomically resets API Key quota usage
	// and clears only the account-level rate-limit cooldown.
	ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error
	// RevertProxyFallback 将账号的 proxy_id 切回 proxy_fallback_origin_id，并清空 origin 字段。
	// 仅当 proxy_fallback_origin_id IS NOT NULL 时更新，否则视为账号不存在（返回 ErrAccountNotFound）。
	RevertProxyFallback(ctx context.Context, accountID int64) error
	// ListShadowsByParent 返回指定父账号的影子账号；当前实现仅查 quota_dimension='spark'（唯一预设）。
	// ⚠️ 新增影子维度时：须更新此函数（或新增维度专用列举），并检查所有调用点（级联删除/一母一影校验/type 守卫），否则会静默漏掉新维度。
	ListShadowsByParent(ctx context.Context, parentID int64) ([]*Account, error)
}

// CNUsageMonitorSnapshotRepository 提供国产供应商监控快照的仓储级 CAS 写入。
// 该能力保持为窄接口，避免所有 AccountRepository 测试替身被迫实现后台任务细节。
type CNUsageMonitorSnapshotRepository interface {
	UpdateCNUsageMonitorSnapshotCAS(
		ctx context.Context,
		accountID int64,
		expectedUpdatedAt time.Time,
		snapshot *accountcore.CNUsageMonitorSnapshot,
		clearExtraKey string,
	) (bool, error)
}

// AccountBulkUpdate 兼容旧消费者，值类型归账号模块。
