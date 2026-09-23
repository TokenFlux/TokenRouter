package provider

import (
	context "context"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// ExecutionAccountStore 只定义执行适配所需操作，实际持久化与资金写入由各原生存储负责。
type ExecutionAccountStore interface {
	GetByID(ctx context.Context, id int64) (*ExecutionAccount, error)
	// GetByIDs 保持单次批量查询，忽略缺失 ID。
	GetByIDs(ctx context.Context, ids []int64) ([]*ExecutionAccount, error)

	Update(ctx context.Context, account *ExecutionAccount) error

	ListByPlatform(ctx context.Context, platform string) ([]ExecutionAccount, error)

	UpdateLastUsed(ctx context.Context, id int64) error
	BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error
	SetError(ctx context.Context, id int64, errorMsg string) error
	ClearError(ctx context.Context, id int64) error

	ListSchedulable(ctx context.Context) ([]ExecutionAccount, error)
	ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]ExecutionAccount, error)
	ListSchedulableByPlatform(ctx context.Context, platform string) ([]ExecutionAccount, error)
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]ExecutionAccount, error)
	ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]ExecutionAccount, error)
	ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]ExecutionAccount, error)
	ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]ExecutionAccount, error)
	ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]ExecutionAccount, error)
	// ListModelAvailabilityCandidates 返回持久配置为 active 且 schedulable 的模型诊断候选账号，
	// 不过滤限流、过载、临时不可调度或到期窗口等瞬时状态。groupID 为 nil 时，
	// includeGrouped 决定查询全部匹配账号，还是只查询没有分组绑定的账号。
	ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]ExecutionAccount, error)

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

	// IncrementQuotaUsed 原子递增 API Key 账号的配额用量（总/日/周）
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error
	BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error)
}
