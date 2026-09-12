// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	time "time"
)

// CreateUserInput represents input for creating a new user via admin operations.
type CreateUserInput struct {
	Email         string
	Password      string
	Username      string
	Notes         string
	Role          string // 空字符串表示使用默认角色(user);合法值 admin/user
	Balance       *float64
	Concurrency   int
	RPMLimit      int
	APIKeyLimit   *int // nil 表示继承系统默认值，0 表示不限制。
	AllowedGroups []int64
	// DisabledPublicGroups 记录管理员禁止该用户使用的公开分组 ID。
	DisabledPublicGroups []int64
	// ActorAdminID 执行本次操作的管理员ID(来自JWT)，仅用于权限敏感操作的审计日志。
	ActorAdminID int64
}

type UpdateUserInput struct {
	Email         string
	Password      string
	Username      *string
	Notes         *string
	Role          string   // 空字符串表示"未提供"(不修改);合法值 admin/user
	Balance       *float64 // 使用指针区分"未提供"和"设置为0"
	Concurrency   *int     // 使用指针区分"未提供"和"设置为0"
	RPMLimit      *int     // 使用指针区分"未提供"和"设置为0"
	APIKeyLimit   *int     // 使用指针区分"未提供"和"设置为0"
	Status        string
	AllowedGroups *[]int64 // 使用指针区分"未提供"和"设置为空数组"
	// DisabledPublicGroups 使用指针区分"未提供"和"清空公开分组禁用列表"
	DisabledPublicGroups *[]int64
	// GroupRates 用户专属分组倍率配置
	// map[groupID]*rate，nil 表示删除该分组的专属倍率
	GroupRates map[int64]*float64
	// ActorAdminID 执行本次操作的管理员ID(来自JWT)，仅用于权限敏感操作的审计日志。
	ActorAdminID int64
}

type AdminBindAuthIdentityInput struct {
	ProviderType    string
	ProviderKey     string
	ProviderSubject string
	Issuer          *string
	Metadata        map[string]any
	Channel         *AdminBindAuthIdentityChannelInput
}

type AdminBindAuthIdentityChannelInput struct {
	Channel        string
	ChannelAppID   string
	ChannelSubject string
	Metadata       map[string]any
}

type AdminBoundAuthIdentity struct {
	UserID          int64                          `json:"user_id"`
	ProviderType    string                         `json:"provider_type"`
	ProviderKey     string                         `json:"provider_key"`
	ProviderSubject string                         `json:"provider_subject"`
	VerifiedAt      *time.Time                     `json:"verified_at,omitempty"`
	Issuer          *string                        `json:"issuer,omitempty"`
	Metadata        map[string]any                 `json:"metadata"`
	CreatedAt       time.Time                      `json:"created_at"`
	UpdatedAt       time.Time                      `json:"updated_at"`
	Channel         *AdminBoundAuthIdentityChannel `json:"channel,omitempty"`
}

type AdminBoundAuthIdentityChannel struct {
	Channel        string         `json:"channel"`
	ChannelAppID   string         `json:"channel_app_id"`
	ChannelSubject string         `json:"channel_subject"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// ReplaceUserGroupResult 分组替换操作的结果
type ReplaceUserGroupResult struct {
	MigratedKeys int64 // 迁移的 Key 数量
}

// UserRPMStatus describes a user's current per-minute RPM usage.
type UserRPMStatus struct {
	UserRPMUsed  int                  `json:"user_rpm_used"`
	UserRPMLimit int                  `json:"user_rpm_limit"`
	PerGroup     []UserGroupRPMStatus `json:"per_group"`
}

// UserGroupRPMStatus describes current per-minute RPM usage for one user/group pair.
type UserGroupRPMStatus struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	Source    string `json:"source"` // "group" | "override"
}

// UserAdmin 持有用户管理所需窄端口；分组与 Key 只读投影不携带旧服务实例。
type UserAdmin struct{ AdminDependencies }
type AdminDependencies struct {
	Now           func() time.Time
	Users         UserRepository
	Groups        AdminGroupReader
	Keys          AdminKeyReader
	Rates         billing.UserGroupRateRepository
	RPM           AdminRPMReader
	Settings      AdminUserSettings
	Subscriptions DefaultSubscriptionAssigner
	Balances      billing.BalanceAdjuster
	Records       AdminAdjustmentRecords
	Invalidator   AdminInvalidator
	BalanceCache  UserBalanceCache
	Affiliates    AdminAffiliateAccruer
	Transactions  AdminTransactions
	Observer      Observer
	Background    func(string, func()) bool
}

func NewUserAdmin(d AdminDependencies) *UserAdmin { return &UserAdmin{d} }
func (s *UserAdmin) RunBackground(name string, fn func()) bool {
	if s.Background != nil {
		return s.Background(name, fn)
	}
	fn()
	return true
}

type AdminUserSettings interface {
	GetDefaultBalance(context.Context) float64
	GetDefaultUserAPIKeyLimit(context.Context) int
	IsRegistrationEmailNormalizationEnabled(context.Context) bool
	GetDefaultSubscriptions(context.Context) []DefaultSubscriptionSetting
	IsAffiliateAdminRechargeEnabled(context.Context) bool
}
type AdminAdjustmentRecords interface {
	RecordAdjustment(context.Context, int64, string, float64, string) error
	GetUserBalanceHistory(context.Context, int64, int, int, string) ([]billing.RedeemCode, int64, float64, error)
}
type AdminGroup struct {
	ID           int64
	Name, Status string
	IsExclusive  bool
	RPMLimit     int
}
type AdminGroupReader interface {
	GetByID(context.Context, int64) (*AdminGroup, error)
	GetByIDLite(context.Context, int64) (*AdminGroup, error)
}
type AdminKeySummary struct {
	ID      int64
	Key     string
	GroupID *int64
}
type AdminKeyReader interface {
	List(context.Context, int64, int, int, string, string) ([]AdminKeySummary, int64, error)
	ListKeysByUserID(context.Context, int64) ([]string, error)
}
type AdminKeyParticipant interface {
	DeleteWithAudit(context.Context, int64) error
	UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error)
}
type AdminRPMReader interface {
	GetUserRPM(context.Context, int64) (int, error)
	GetUserGroupRPM(context.Context, int64, int64) (int, error)
}
type AdminInvalidator interface {
	InvalidateAuthCacheByUserID(context.Context, int64)
	InvalidateAuthCacheByKey(context.Context, string)
}
type AdminAffiliateAccruer interface {
	AccrueInviteRebate(context.Context, int64, float64) (float64, error)
}
type AdminGroupRateBatchReader interface {
	GetByUserIDs(context.Context, []int64) (map[int64]map[int64]float64, error)
}
type AdminTransactions interface {
	HasDatabase() bool
	DeleteUserAndKeys(context.Context, int64, []AdminKeySummary) error
	ReplaceUserGroup(context.Context, int64, int64, int64) (int64, error)
	BindAuthIdentity(context.Context, PreparedAdminIdentityBinding) (*AdminBoundAuthIdentity, error)
}
type PreparedAdminIdentityBinding struct {
	UserID                                     int64
	ProviderType, ProviderKey, ProviderSubject string
	CompatibleKeys                             []string
	Issuer                                     *string
	Metadata                                   map[string]any
	Channel                                    *AdminBindAuthIdentityChannelInput
	VerifiedAt                                 time.Time
}

const (
	AdjustmentTypeAdminConcurrency = billing.AdjustmentTypeAdminConcurrency
	AdjustmentTypeAdminBalance     = billing.AdjustmentTypeAdminBalance
)

var ErrRPMStatusUnavailable = infraerrors.New(501, "RPM_STATUS_UNAVAILABLE", "RPM cache not available")
