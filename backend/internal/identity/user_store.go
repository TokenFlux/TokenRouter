// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	_ "image/png"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// UserListFilters 包含用户列表的所有筛选项。
type UserListFilters struct {
	Status    string // 用户状态筛选
	Role      string // 用户角色筛选
	Search    string // 按邮箱、用户名搜索
	GroupName string // 按用户授权分组名称模糊筛选
	// APIKeyGroupID 筛选至少拥有一个未软删除且绑定到该分组的 API Key 的用户。
	// 0 表示不筛选；该条件直接匹配 api_keys.group_id，不依赖 allowed_groups。
	APIKeyGroupID int64
	Attributes    map[int64]string // 自定义属性筛选：attributeID -> value
	// IncludeSubscriptions controls whether ListWithFilters should load active subscriptions.
	// For large datasets this can be expensive; admin list pages should enable it on demand.
	// nil means not specified (default: load subscriptions for backward compatibility).
	IncludeSubscriptions *bool
	// IncludeDeleted 为 true 时绕过软删除过滤，返回含已删除（deleted_at 非空）的用户。
	// 仅供 /admin/usage 的 SearchUsers 端点使用，其他列表调用方不要设置。
	IncludeDeleted bool
}

// UserUpdateFields 声明 UserRepository.Update 允许写回的列。
//
// 未声明的列保持数据库当前值，不会被调用方手里的快照覆盖。用户行上有多条
// 不经过 Update 的原子写入路径（DeductBalance/UpdateBalance 扣加余额、
// UpdateConcurrency、BatchUpdateLimits、UpdateUserLastActiveAt 等），
// status/role 也可能被其他流程并发改写。若 Update 无条件整行回写，
// 一次"读-改-写"就会静默回滚这些并发结果（lost update），
// 因此每个调用方必须显式声明它真正要改的列。
//
// 注意这里没有 balance / total_recharged：余额只能经由 AdjustBalance、
// SetBalance、UpdateBalance、DeductBalance 等原子接口修改，Update 永远不碰它们。
type UserUpdateFields struct {
	Email        bool
	Username     bool
	Notes        bool
	PasswordHash bool
	Role         bool
	Status       bool
	Concurrency  bool
	RPMLimit     bool
	// APIKeyLimit 是 fork 的用户级 API Key 数量上限。
	APIKeyLimit  bool
	SignupSource bool
	LastLoginAt  bool
	LastActiveAt bool
	// BalanceNotifySettings 覆盖 balance_notify_enabled / _threshold_type / _threshold。
	BalanceNotifySettings bool
	// BalanceNotifyExtraEmails 与上一项分开，避免"改通知阈值"覆盖并发的"加通知邮箱"。
	BalanceNotifyExtraEmails bool
	// AllowedGroups 为 true 时才同步 user_allowed_groups 关联表。
	AllowedGroups bool
	// DisabledPublicGroups 为 true 时才同步 fork 的公共分组禁用关联表。
	DisabledPublicGroups bool
}

// BalanceChange 保留旧资金结果类型名。
type BalanceChange = billing.BalanceChange

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	CreateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string) error
	GetByID(ctx context.Context, id int64) (*User, error)
	// GetByIDIncludeDeleted 绕过软删除过滤按 ID 取用户（含已删）。仅供管理员审计/usage 点击使用。
	GetByIDIncludeDeleted(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetFirstAdmin(ctx context.Context) (*User, error)
	// Update 只写 fields 中显式声明的列，其余列保持库中当前值。
	Update(ctx context.Context, user *User, fields UserUpdateFields) error
	// UpdateWithNormalizedEmailGuard 在相同字段掩码基础上增加 fork 的注册邮箱归一化互斥保护。
	UpdateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string, fields UserUpdateFields) error
	Delete(ctx context.Context, id int64) error
	GetUserAvatar(ctx context.Context, userID int64) (*UserAvatar, error)
	UpsertUserAvatar(ctx context.Context, userID int64, input UpsertUserAvatarInput) (*UserAvatar, error)
	DeleteUserAvatar(ctx context.Context, userID int64) error

	List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error)
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error)
	GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error)
	GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error)
	UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error

	AddBalance(ctx context.Context, id int64, amount float64) error
	UpdateBalance(ctx context.Context, id int64, amount float64) error
	DeductBalance(ctx context.Context, id int64, amount float64) (float64, error)
	// AdjustBalance 原子地把 delta 累加到余额上，并返回变更前后的值。结果为负时
	// 拒绝写入并返回 ErrBalanceNegative。管理员的加/扣款必须走这里而不是
	// "读余额→算新值→整行写回"，否则并发的计费扣款会被旧快照抹掉。
	AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error)
	// SetBalance 原子地把余额置为 value（value 必须 >= 0），返回变更前后的值。
	SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error)
	UpdateConcurrency(ctx context.Context, id int64, amount int) error
	// BatchSetConcurrency 批量设置用户并发数，负数会按 0 处理。
	BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error)
	// BatchAddConcurrency 批量增减用户并发数，结果不会低于 0。
	BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error)
	// BatchUpdateLimits 在一次写入中只覆盖非 nil 的用户限制字段。
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error)
	LockRegistrationEmail(ctx context.Context, normalizedEmail string) error
	RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error)
	// AddGroupToAllowedGroups 将指定分组增量添加到用户的 allowed_groups（幂等，冲突忽略）
	AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	// RemoveGroupFromUserAllowedGroups 移除单个用户的指定分组权限
	RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	ListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error)
	UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) error

	// TOTP 双因素认证
	UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error
	EnableTotp(ctx context.Context, userID int64) error
	DisableTotp(ctx context.Context, userID int64) error
}

// RegistrationEmailDomainRepository 为非白名单域名单账户策略提供原子仓储能力。
// 独立成窄接口，避免注册专用方法扩散到所有 UserRepository 测试桩和消费者。
type RegistrationEmailDomainRepository interface {
	CountUsersByEmailDomain(ctx context.Context, domain string) (int, error)
	CreateWithRegistrationEmailGuards(ctx context.Context, user *User, normalizedEmail, domain string) error
}

// RedeemUserAdjustmentRepository 为负值兑换码提供原子更新，并保证结果不低于 0。
// 该接口刻意小于 UserRepository，因为常规用量计费允许余额透支。
type RedeemUserAdjustmentRepository interface {
	ApplyRedeemBalanceAdjustment(ctx context.Context, id int64, delta float64) error
	ApplyRedeemConcurrencyAdjustment(ctx context.Context, id int64, delta int) error
}

type UserAuthIdentityRecord struct {
	ProviderType    string
	ProviderKey     string
	ProviderSubject string
	VerifiedAt      *time.Time
	Issuer          *string
	Metadata        map[string]any
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UserIdentitySummary struct {
	Provider      string     `json:"provider"`
	Bound         bool       `json:"bound"`
	BoundCount    int        `json:"bound_count"`
	DisplayName   string     `json:"display_name,omitempty"`
	AvatarURL     string     `json:"-"`
	SubjectHint   string     `json:"subject_hint,omitempty"`
	ProviderKey   string     `json:"provider_key,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	BindStartPath string     `json:"bind_start_path,omitempty"`
	CanBind       bool       `json:"can_bind"`
	CanUnbind     bool       `json:"can_unbind"`
	NoteKey       string     `json:"note_key,omitempty"`
	Note          string     `json:"note,omitempty"`
}

type UserIdentitySummarySet struct {
	Email    UserIdentitySummary `json:"email"`
	LinuxDo  UserIdentitySummary `json:"linuxdo"`
	OIDC     UserIdentitySummary `json:"oidc"`
	WeChat   UserIdentitySummary `json:"wechat"`
	DingTalk UserIdentitySummary `json:"dingtalk"`
}

type StartUserIdentityBindingRequest struct {
	Provider   string
	RedirectTo string
}

type StartUserIdentityBindingResult struct {
	Provider           string `json:"provider"`
	AuthorizeURL       string `json:"authorize_url"`
	Method             string `json:"method"`
	UseBrowserRedirect bool   `json:"use_browser_redirect"`
}

// UpdateProfileRequest 更新用户资料请求
type UpdateProfileRequest struct {
	Email                  *string  `json:"email"`
	Username               *string  `json:"username"`
	AvatarURL              *string  `json:"avatar_url"`
	Concurrency            *int     `json:"concurrency"`
	BalanceNotifyEnabled   *bool    `json:"balance_notify_enabled"`
	BalanceNotifyThreshold *float64 `json:"balance_notify_threshold"`
}

type UserAvatar struct {
	StorageProvider string
	StorageKey      string
	URL             string
	ContentType     string
	ByteSize        int
	SHA256          string
}

type UpsertUserAvatarInput struct {
	StorageProvider string
	StorageKey      string
	URL             string
	ContentType     string
	ByteSize        int
	SHA256          string
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// IsEmpty 报告该次 Update 是否不写任何列（此时仓储直接返回，不产生写操作）。
func (f UserUpdateFields) IsEmpty() bool {
	return f == UserUpdateFields{}
}
