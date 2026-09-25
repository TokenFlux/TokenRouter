// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// AdminStore 只表达管理用例实际需要的读取、状态写入与删除，不暴露数据库连接。
type AdminStore interface {
	UpdateExtra(context.Context, int64, map[string]any) error
	BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error)
	RevertProxyFallback(context.Context, int64) error
	Update(context.Context, *Record) error
	FindByExtraField(context.Context, string, any) ([]Record, error)
	Create(context.Context, *Record) error
	BindGroups(context.Context, int64, []int64) error
	ListByGroup(context.Context, int64) ([]Record, error)
	GetByID(context.Context, int64) (*Record, error)
	GetByIDs(context.Context, []int64) ([]*Record, error)
	ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, string, int64, string) ([]Record, *pagination.PaginationResult, error)
	ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Record, error)
	ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]Record, error)
	ListSchedulableUngroupedByPlatforms(context.Context, []string) ([]Record, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Record, error)
	ListSchedulableUngroupedByPlatform(context.Context, string) ([]Record, error)
	ListShadowsByParent(context.Context, int64) ([]*Record, error)
	Delete(context.Context, int64) error
	ClearError(context.Context, int64) error
	ClearRateLimit(context.Context, int64) error
	ClearAntigravityQuotaScopes(context.Context, int64) error
	ClearModelRateLimits(context.Context, int64) error
	ClearTempUnschedulable(context.Context, int64) error
	SetError(context.Context, int64, string) error
	SetSchedulable(context.Context, int64, bool) error
}
type AccountQuotaResetter interface {
	ResetQuotaUsedAndClearRateLimitCooldown(context.Context, int64) error
}
type RuntimeUnblocker interface{ ClearAccountSchedulingBlock(int64) }
type AdminOptions struct {
	ShadowModels   func() map[string]any
	Duplicates     DuplicateStore
	Groups         AdminGroups
	Proxies        PrivacyProxyReader
	Creation       CreationOptions
	Credentials    CreateCredentialHooks
	Background     func(string, func()) bool
	Error          func(string, ...any)
	Privacy        *PrivacyService
	Quotas         AccountQuotaResetter
	RuntimeBlocker RuntimeUnblocker
}

// Admin 拥有账号管理规则，资金操作和运行时阻断通过消费者侧接口注入。
type Admin struct {
	accountRepo AdminStore
	options     AdminOptions
}

func NewAdmin(store AdminStore, options AdminOptions) *Admin {
	return &Admin{accountRepo: store, options: options}
}

// Privacy 提供同一账号模块的隐私用例，供旧管理入口转接。
func (s *Admin) Privacy() *PrivacyService { return s.options.Privacy }
