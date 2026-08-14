package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

// AdminService interface defines admin management operations
type AdminService interface {
	// User management
	ListUsers(ctx context.Context, page, pageSize int, filters UserListFilters, sortBy, sortOrder string) ([]User, int64, error)
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserIncludeDeleted(ctx context.Context, id int64) (*User, error)
	CreateUser(ctx context.Context, input *CreateUserInput) (*User, error)
	UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (*User, error)
	DeleteUser(ctx context.Context, id int64) error
	UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*User, error)
	BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error)
	// BatchUpdateLimits 只覆盖非 nil 的并发数或 RPM 上限，并返回实际更新行数。
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]APIKey, int64, error)
	GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error)
	GetUserRPMStatus(ctx context.Context, userID int64) (*UserRPMStatus, error)
	// GetUserBalanceHistory returns paginated balance/concurrency change records for a user.
	// codeType is optional - pass empty string to return all types.
	// Also returns totalRecharged (sum of all positive balance top-ups).
	GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error)
	BindUserAuthIdentity(ctx context.Context, userID int64, input AdminBindAuthIdentityInput) (*AdminBoundAuthIdentity, error)

	// Group management
	ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]Group, int64, error)
	GetAllGroups(ctx context.Context) ([]Group, error)
	GetAllGroupsByPlatform(ctx context.Context, platform string) ([]Group, error)
	// GetAllGroupsIncludingInactive 返回所有状态的分组（启用 + 禁用），按 sort_order 和 id 排序，
	// 供 API Key 分组筛选下拉框使用。
	GetAllGroupsIncludingInactive(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, id int64) (*Group, error)
	GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error)
	CreateGroup(ctx context.Context, input *CreateGroupInput) (*Group, error)
	// DuplicateGroup 创建停用状态的独立配置副本，并保留账号绑定及其优先级。
	DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error)
	// RecoverDuplicateGroup 在重试结果不明确时返回已提交的副本，绝不创建分组。
	RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error)
	UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error)
	DeleteGroup(ctx context.Context, id int64) error
	GetGroupAPIKeys(ctx context.Context, groupID int64, page, pageSize int) ([]APIKey, int64, error)
	GetGroupRateMultipliers(ctx context.Context, groupID int64) ([]UserGroupRateEntry, error)
	ClearGroupRateMultipliers(ctx context.Context, groupID int64) error
	BatchSetGroupRateMultipliers(ctx context.Context, groupID int64, entries []GroupRateMultiplierInput) error
	ClearGroupRPMOverrides(ctx context.Context, groupID int64) error
	BatchSetGroupRPMOverrides(ctx context.Context, groupID int64, entries []GroupRPMOverrideInput) error
	UpdateGroupSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error

	// API Key management (admin)
	AdminResetAPIKeyRateLimitUsage(ctx context.Context, keyID int64) (*APIKey, error)
	AdminUpdateAPIKeyGroupID(ctx context.Context, keyID int64, groupID *int64) (*AdminUpdateAPIKeyGroupIDResult, error)

	// ReplaceUserGroup 替换用户的专属分组：授予新分组权限、迁移 Key、移除旧分组权限
	ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error)

	// Account management
	ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]Account, int64, error)
	// ListAccountsForSchedulerScoreFilter 返回符合过滤条件的全部账号（不分页），
	// 作为账号列表页计算高级调度分数的过滤范围池。
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
	// ListSchedulableAccountsForAdvancedSchedulerScore 返回指定分组内可调度账号，
	// 用于按高级分组计算调度分数。
	ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]Account, error)
	GetAccount(ctx context.Context, id int64) (*Account, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]*Account, error)
	CreateAccount(ctx context.Context, input *CreateAccountInput) (*Account, error)
	// DuplicateAccount 根据已有配置创建独立账号；一级运行态列按普通创建路径重置。
	DuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error)
	// RecoverDuplicateAccount 在重试结果不明时返回先前已提交的复制件，绝不创建账号。
	RecoverDuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error)
	UpdateAccount(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error)
	// UpdateAccountExtra 仅对 Extra 做 JSONB key 级增量合并，不覆盖已有持久化配置。
	UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error
	DeleteAccount(ctx context.Context, id int64) error
	RefreshAccountCredentials(ctx context.Context, id int64) (*Account, error)
	ClearAccountError(ctx context.Context, id int64) (*Account, error)
	SetAccountError(ctx context.Context, id int64, errorMsg string) error
	// EnsureOpenAIPrivacy 检查 OpenAI OAuth 账号 privacy_mode，未设置则尝试关闭训练数据共享并持久化。
	EnsureOpenAIPrivacy(ctx context.Context, account *Account) string
	// EnsureAntigravityPrivacy 检查 Antigravity OAuth 账号 privacy_mode，未设置则调用 setUserSettings 并持久化。
	EnsureAntigravityPrivacy(ctx context.Context, account *Account) string
	// ForceOpenAIPrivacy 强制重新设置 OpenAI OAuth 账号隐私，无论当前状态。
	ForceOpenAIPrivacy(ctx context.Context, account *Account) string
	// ForceAntigravityPrivacy 强制重新设置 Antigravity OAuth 账号隐私，无论当前状态。
	ForceAntigravityPrivacy(ctx context.Context, account *Account) string
	SetAccountSchedulable(ctx context.Context, id int64, schedulable bool) (*Account, error)
	BulkUpdateAccounts(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error)
	CheckMixedChannelRisk(ctx context.Context, currentAccountID int64, currentAccountPlatform string, groupIDs []int64) error
	// RevertAccountProxyFallback 将账号的 proxy_id 切回 proxy_fallback_origin_id，并清空 origin 字段。
	// 若账号不存在返回 ErrAccountNotFound；若账号存在但不在 fallback 状态，返回 ErrAccountNotInFallback。
	RevertAccountProxyFallback(ctx context.Context, id int64) error
	// CreateShadow 为指定 OpenAI OAuth 母账号创建 spark 维度影子账号（一母一影）。
	// 影子账号不持凭据（Credentials 恒为空），透传母账号凭据；继承母账号的 ProxyID。
	CreateShadow(ctx context.Context, parentID int64, opts ShadowOptions) (*Account, error)

	// Proxy management
	ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]Proxy, int64, error)
	ListProxiesWithAccountCount(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]ProxyWithAccountCount, int64, error)
	GetAllProxies(ctx context.Context) ([]Proxy, error)
	GetAllProxiesWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error)
	GetProxy(ctx context.Context, id int64) (*Proxy, error)
	GetProxiesByIDs(ctx context.Context, ids []int64) ([]Proxy, error)
	CreateProxy(ctx context.Context, input *CreateProxyInput) (*Proxy, error)
	UpdateProxy(ctx context.Context, id int64, input *UpdateProxyInput) (*Proxy, error)
	DeleteProxy(ctx context.Context, id int64) error
	BatchDeleteProxies(ctx context.Context, ids []int64) (*ProxyBatchDeleteResult, error)
	GetProxyAccounts(ctx context.Context, proxyID int64) ([]ProxyAccountSummary, error)
	CheckProxyExists(ctx context.Context, host string, port int, username, password string) (bool, error)
	TestProxy(ctx context.Context, id int64) (*ProxyTestResult, error)
	CheckProxyQuality(ctx context.Context, id int64) (*ProxyQualityCheckResult, error)

	// Redeem code management
	ListRedeemCodes(ctx context.Context, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]RedeemCode, int64, error)
	GetRedeemCode(ctx context.Context, id int64) (*RedeemCode, error)
	GenerateRedeemCodes(ctx context.Context, input *GenerateRedeemCodesInput) ([]RedeemCode, error)
	UpdateRedeemCode(ctx context.Context, id int64, input *UpdateRedeemCodeInput) (*RedeemCode, error)
	DeleteRedeemCode(ctx context.Context, id int64) error
	BatchDeleteRedeemCodes(ctx context.Context, ids []int64) (int64, error)
	ExpireRedeemCode(ctx context.Context, id int64) (*RedeemCode, error)
	ResetAccountQuota(ctx context.Context, id int64) error
}

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

type CreateGroupInput struct {
	Name        string
	Description string
	Platform    string
	// SchedulerType 为空时使用基础调度器，保持新分组的历史默认行为。
	SchedulerType string
	// AdvancedSchedulerOverrides 未设置字段继承网关通用高级调度设置。
	AdvancedSchedulerOverrides GroupAdvancedSchedulerOverrides
	DisplayBrand               string
	SortOrder                  *int
	RateMultiplier             float64
	IsExclusive                bool
	IsDefault                  bool
	// DataSharingEnabled 将新建分组标记为数据共享分组。
	DataSharingEnabled bool
	// SessionIsolationEnabled 开启后拒绝其它分组已归属的显式会话切入。
	SessionIsolationEnabled bool
	// LongContextPricingEnabled 为 nil 时默认开启，以兼容未发送新字段的客户端。
	LongContextPricingEnabled *bool
	ModelPricing              []ChannelModelPricing
	// 图片生成计费配置（仅 antigravity 平台使用）
	AllowImageGeneration         bool
	AllowBatchImageGeneration    bool
	ImageRateIndependent         bool
	ImageRateMultiplier          *float64
	BatchImageDiscountMultiplier *float64
	BatchImageHoldMultiplier     *float64
	VideoRateIndependent         bool
	VideoRateMultiplier          *float64
	// 高峰时段倍率配置（PeakRateMultiplier 为 nil 时按 1.0 处理）
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier *float64
	ImagePrice1K       *float64
	ImagePrice2K       *float64
	ImagePrice4K       *float64
	VideoPrice480P     *float64
	VideoPrice720P     *float64
	VideoPrice1080P    *float64
	// VideoModelPrices 可选按模型族×分辨率覆盖视频每秒单价。
	VideoModelPrices map[string]map[string]float64
	// Codex alpha/search 网页搜索单次价格（USD/次，仅 openai 平台使用）；nil/负数按默认价 0.01 处理
	WebSearchPricePerCall *float64
	// 搜索工具每千次单价。
	SearchPricePer1k *float64
	// Grok Voice 显式定价（分组级）
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64
	ClaudeCodeOnly               bool   // 仅允许 Claude Code 客户端
	FallbackGroupID              *int64 // 降级分组 ID
	// 无效请求兜底分组 ID（仅 anthropic 平台使用）
	FallbackGroupIDOnInvalidRequest *int64
	// UnavailableFallbackGroupID 当前分组不可用时 API Key 优先回退到的分组 ID。
	UnavailableFallbackGroupID *int64
	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64
	ModelRoutingEnabled bool // 是否启用模型路由
	MCPXMLInject        *bool
	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes []string
	// AllowedClientProtocols 为 nil 时使用平台默认值；显式空数组对所有平台都合法。
	AllowedClientProtocols []GroupClientProtocol
	// AllowMessagesDispatch 仅在 OpenAI 分组且新字段缺省时作为兼容输入。
	AllowMessagesDispatch       bool
	AllowLive                   bool
	DefaultMappedModel          string
	RequireOAuthOnly            bool
	RequirePrivacySet           bool
	MessagesDispatchModelConfig OpenAIMessagesDispatchModelConfig
	ModelsListConfig            GroupModelsListConfig
	// AvailabilityProbeConfig 控制分组主动可用性探测。
	AvailabilityProbeConfig GroupAvailabilityProbeConfig
	// RPMLimit 分组 RPM 上限（0 = 不限制）
	RPMLimit int
	// MaxReasoningEffort OpenAI/Codex 请求的推理强度上限，空字符串表示不限制。
	MaxReasoningEffort string
	// ReasoningEffortMappings OpenAI/Codex 推理强度精确映射。
	ReasoningEffortMappings []ReasoningEffortMapping
	// 从指定分组复制账号（创建分组后在同一事务内绑定）
	CopyAccountsFromGroupIDs []int64
}

type UpdateGroupInput struct {
	Name        string
	Description *string
	Platform    string
	// SchedulerType 为 nil 时保留原值。
	SchedulerType *string
	// AdvancedSchedulerOverrides 为 nil 时保留原值；空对象表示清除全部覆盖并恢复继承。
	AdvancedSchedulerOverrides *GroupAdvancedSchedulerOverrides
	DisplayBrand               *string
	SortOrder                  *int
	RateMultiplier             *float64 // 使用指针以支持设置为0
	IsExclusive                *bool
	IsDefault                  *bool
	// DataSharingEnabled 控制分组是否进入数据共享采集流程。
	DataSharingEnabled *bool
	// SessionIsolationEnabled 控制目标分组是否开启会话隔离。
	SessionIsolationEnabled   *bool
	Status                    string
	LongContextPricingEnabled *bool
	ModelPricing              *[]ChannelModelPricing
	// 图片生成计费配置（仅 antigravity 平台使用）
	AllowImageGeneration         *bool
	AllowBatchImageGeneration    *bool
	ImageRateIndependent         *bool
	ImageRateMultiplier          *float64
	BatchImageDiscountMultiplier *float64
	BatchImageHoldMultiplier     *float64
	VideoRateIndependent         *bool
	VideoRateMultiplier          *float64
	// 高峰时段倍率配置（nil 表示不修改）
	PeakRateEnabled    *bool
	PeakStart          *string
	PeakEnd            *string
	PeakRateMultiplier *float64
	ImagePrice1K       *float64
	ImagePrice2K       *float64
	ImagePrice4K       *float64
	VideoPrice480P     *float64
	VideoPrice720P     *float64
	VideoPrice1080P    *float64
	// VideoModelPrices 可选按模型族×分辨率覆盖；nil 表示不修改，空 map 表示清除。
	VideoModelPrices map[string]map[string]float64
	// Codex alpha/search 网页搜索单次价格（USD/次）；nil 表示不修改，负数表示清除回默认价 0.01
	WebSearchPricePerCall *float64
	// 搜索工具单价；nil 不修改，负数清除。
	SearchPricePer1k *float64
	// Grok Voice 显式定价；nil 表示不修改，负数表示清除。
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64
	ClaudeCodeOnly               *bool  // 仅允许 Claude Code 客户端
	FallbackGroupID              *int64 // 降级分组 ID
	// 无效请求兜底分组 ID（仅 anthropic 平台使用）
	FallbackGroupIDOnInvalidRequest *int64
	// UnavailableFallbackGroupID 当前分组不可用时 API Key 优先回退到的分组 ID。
	UnavailableFallbackGroupID *int64
	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64
	ModelRoutingEnabled *bool // 是否启用模型路由
	MCPXMLInject        *bool
	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes *[]string
	// AllowedClientProtocols 为 nil 时保留原值；非 nil 表示显式替换完整集合。
	AllowedClientProtocols *[]GroupClientProtocol
	// AllowMessagesDispatch 仅在 OpenAI 分组且新字段缺省时作为兼容输入。
	AllowMessagesDispatch       *bool
	AllowLive                   *bool
	DefaultMappedModel          *string
	RequireOAuthOnly            *bool
	RequirePrivacySet           *bool
	MessagesDispatchModelConfig *OpenAIMessagesDispatchModelConfig
	ModelsListConfig            *GroupModelsListConfig
	// AvailabilityProbeConfig 为 nil 时不修改探测配置。
	AvailabilityProbeConfig *GroupAvailabilityProbeConfig
	// RPMLimit 分组 RPM 上限（0 = 不限制），nil 表示未提供不改动。
	RPMLimit *int
	// MaxReasoningEffort 空字符串表示清除上限；nil 表示未提供不改动。
	MaxReasoningEffort *string
	// ReasoningEffortMappings nil 表示不修改，空数组表示清空，非空数组表示替换。
	ReasoningEffortMappings *[]ReasoningEffortMapping
	// 从指定分组复制账号（同步操作：先清空当前分组的账号绑定，再绑定源分组的账号）
	CopyAccountsFromGroupIDs []int64
}

type CreateAccountInput struct {
	Name               string
	Notes              *string
	Platform           string
	Type               string
	Credentials        map[string]any
	Extra              map[string]any
	ProxyID            *int64
	Concurrency        int
	Priority           int
	RateMultiplier     *float64 // 账号计费倍率（>=0，允许 0）
	LoadFactor         *int
	GroupIDs           []int64
	ExpiresAt          *int64
	AutoPauseOnExpired *bool
	// SkipDefaultGroupBind prevents auto-binding to platform default group when GroupIDs is empty.
	SkipDefaultGroupBind bool
	// SkipMixedChannelCheck skips the mixed channel risk check when binding groups.
	// This should only be set when the caller has explicitly confirmed the risk.
	SkipMixedChannelCheck bool
}

// ShadowOptions is the input for CreateShadow.
// The shadow holds no credentials — the scheduler transparently delegates to the parent account's tokens.
type ShadowOptions struct {
	Name        string
	Priority    int
	Concurrency int
	GroupIDs    []int64
}

type UpdateAccountInput struct {
	Name                  string
	Notes                 *string
	Type                  string // Account type: oauth, setup-token, apikey
	Credentials           map[string]any
	Extra                 map[string]any
	ProxyID               *int64
	Concurrency           *int     // 使用指针区分"未提供"和"设置为0"
	Priority              *int     // 使用指针区分"未提供"和"设置为0"
	RateMultiplier        *float64 // 账号计费倍率（>=0，允许 0）
	LoadFactor            *int
	Status                string
	GroupIDs              *[]int64
	ExpiresAt             *int64
	AutoPauseOnExpired    *bool
	SkipMixedChannelCheck bool // 跳过混合渠道检查（用户已确认风险）
}

// BulkUpdateAccountsInput describes the payload for bulk updating accounts.
type BulkUpdateAccountsInput struct {
	AccountIDs     []int64
	Filters        *BulkUpdateAccountFilters
	Name           string
	ProxyID        *int64
	Concurrency    *int
	Priority       *int
	RateMultiplier *float64 // 账号计费倍率（>=0，允许 0）
	LoadFactor     *int
	Status         string
	Schedulable    *bool
	GroupIDs       *[]int64
	Credentials    map[string]any
	Extra          map[string]any
	// SkipMixedChannelCheck skips the mixed channel risk check when binding groups.
	// This should only be set when the caller has explicitly confirmed the risk.
	SkipMixedChannelCheck bool
}

type BulkUpdateAccountFilters struct {
	Platform    string
	Type        string
	Status      string
	Group       string
	Search      string
	PrivacyMode string
}

// BulkUpdateAccountResult captures the result for a single account update.
type BulkUpdateAccountResult struct {
	AccountID int64  `json:"account_id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// AdminUpdateAPIKeyGroupIDResult is the result of AdminUpdateAPIKeyGroupID.
type AdminUpdateAPIKeyGroupIDResult struct {
	APIKey                 *APIKey
	AutoGrantedGroupAccess bool   // true if a new exclusive group permission was auto-added
	GrantedGroupID         *int64 // the group ID that was auto-granted
	GrantedGroupName       string // the group name that was auto-granted
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

// BulkUpdateAccountsResult is the aggregated response for bulk updates.
type BulkUpdateAccountsResult struct {
	Success    int                       `json:"success"`
	Failed     int                       `json:"failed"`
	SuccessIDs []int64                   `json:"success_ids"`
	FailedIDs  []int64                   `json:"failed_ids"`
	Results    []BulkUpdateAccountResult `json:"results"`
}

type CreateProxyInput struct {
	Name           string
	Protocol       string
	Host           string
	Port           int
	Username       string
	Password       string
	ExpiresAt      *time.Time
	FallbackMode   string
	BackupProxyID  *int64
	ExpiryWarnDays int
}

type UpdateProxyInput struct {
	Name           string
	Protocol       string
	Host           string
	Port           int
	Username       string
	Password       string
	Status         string
	ExpiresAt      *time.Time
	FallbackMode   string
	BackupProxyID  *int64
	ExpiryWarnDays int
}

type GenerateRedeemCodesInput struct {
	Code      string
	Count     int
	Type      string
	Value     float64
	MaxUses   *int
	ExpiresAt *time.Time
	PlanID    *int64 // 订阅类型专用：关联的套餐ID
}

type ProxyBatchDeleteResult struct {
	DeletedIDs []int64                   `json:"deleted_ids"`
	Skipped    []ProxyBatchDeleteSkipped `json:"skipped"`
}

type ProxyBatchDeleteSkipped struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

// ProxyTestResult represents the result of testing a proxy
type ProxyTestResult struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`
	IPAddress   string `json:"ip_address,omitempty"`
	City        string `json:"city,omitempty"`
	Region      string `json:"region,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type ProxyQualityCheckResult struct {
	ProxyID        int64                   `json:"proxy_id"`
	Score          int                     `json:"score"`
	Grade          string                  `json:"grade"`
	Summary        string                  `json:"summary"`
	ExitIP         string                  `json:"exit_ip,omitempty"`
	Country        string                  `json:"country,omitempty"`
	CountryCode    string                  `json:"country_code,omitempty"`
	BaseLatencyMs  int64                   `json:"base_latency_ms,omitempty"`
	PassedCount    int                     `json:"passed_count"`
	WarnCount      int                     `json:"warn_count"`
	FailedCount    int                     `json:"failed_count"`
	ChallengeCount int                     `json:"challenge_count"`
	CheckedAt      int64                   `json:"checked_at"`
	Items          []ProxyQualityCheckItem `json:"items"`
}

type ProxyQualityCheckItem struct {
	Target     string `json:"target"`
	Status     string `json:"status"` // pass/warn/fail/challenge
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Message    string `json:"message,omitempty"`
	CFRay      string `json:"cf_ray,omitempty"`
}

// ProxyExitInfo represents proxy exit information from ip-api.com
type ProxyExitInfo struct {
	IP          string
	City        string
	Region      string
	Country     string
	CountryCode string
}

// ProxyExitInfoProber tests proxy connectivity and retrieves exit information
type ProxyExitInfoProber interface {
	ProbeProxy(ctx context.Context, proxyURL string) (*ProxyExitInfo, int64, error)
}

type groupExistenceBatchReader interface {
	ExistsByIDs(ctx context.Context, ids []int64) (map[int64]bool, error)
}

type proxyQualityTarget struct {
	Target          string
	URL             string
	Method          string
	AllowedStatuses map[int]struct{}
}

var proxyQualityTargets = []proxyQualityTarget{
	{
		Target: "openai",
		URL:    "https://api.openai.com/v1/models",
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
		},
	},
	{
		Target: "anthropic",
		URL:    "https://api.anthropic.com/v1/messages",
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized:     {},
			http.StatusMethodNotAllowed: {},
			http.StatusNotFound:         {},
			http.StatusBadRequest:       {},
		},
	},
	{
		Target: "gemini",
		URL:    "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusOK: {},
		},
	},
	{
		Target: "grok",
		URL:    "https://api.x.ai/v1/models",
		Method: http.MethodGet,
		AllowedStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
		},
	},
}

const (
	proxyQualityRequestTimeout        = 15 * time.Second
	proxyQualityResponseHeaderTimeout = 10 * time.Second
	proxyQualityMaxBodyBytes          = int64(8 * 1024)
	proxyQualityClientUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"
)

var ErrRPMStatusUnavailable = infraerrors.New(http.StatusNotImplemented, "RPM_STATUS_UNAVAILABLE", "RPM cache not available")

// adminServiceImpl implements AdminService
type adminServiceImpl struct {
	userRepo             UserRepository
	groupRepo            GroupRepository
	groupDuplicateRepo   GroupDuplicateRepository
	groupSortOrderRepo   GroupSortOrderRepository
	accountRepo          AccountRepository
	accountDuplicateRepo AccountDuplicateRepository
	proxyRepo            ProxyRepository
	apiKeyRepo           APIKeyRepository
	redeemCodeRepo       RedeemCodeRepository
	userGroupRateRepo    UserGroupRateRepository
	userRPMCache         UserRPMCache
	billingCacheService  *BillingCacheService
	proxyProber          ProxyExitInfoProber
	proxyLatencyCache    ProxyLatencyCache
	authCacheInvalidator APIKeyAuthCacheInvalidator
	entClient            *dbent.Client // 用于开启数据库事务
	settingService       *SettingService
	defaultSubAssigner   DefaultSubscriptionAssigner
	userSubRepo          UserSubscriptionRepository
	privacyClientFactory PrivacyClientFactory
	runtimeBlocker       AccountRuntimeBlocker
	httpUpstream         HTTPUpstream
	tlsFPProfileService  *TLSFingerprintProfileService
	affiliateService     adminRechargeAffiliateAccruer
	// 分组平台变更后失效渠道缓存；可为 nil，此时缓存会在 TTL 到期后自然重建。
	channelCacheInvalidator ChannelCacheInvalidator
}

// ChannelCacheInvalidator 失效渠道缓存。
// 使用窄接口避免管理服务依赖整个 ChannelService。
type ChannelCacheInvalidator interface {
	InvalidateCache()
}

// adminRechargeAffiliateAccruer 抽象管理员充值返利能力，便于隔离测试计提行为。
type adminRechargeAffiliateAccruer interface {
	AccrueInviteRebate(ctx context.Context, inviteeUserID int64, purchasedPoints float64) (float64, error)
}

type userGroupRateBatchReader interface {
	GetByUserIDs(ctx context.Context, userIDs []int64) (map[int64]map[int64]float64, error)
}

// NewAdminService creates a new AdminService
func NewAdminService(
	userRepo UserRepository,
	groupRepo AdminGroupRepository,
	accountRepo AdminAccountRepository,
	proxyRepo ProxyRepository,
	apiKeyRepo APIKeyRepository,
	redeemCodeRepo RedeemCodeRepository,
	userGroupRateRepo UserGroupRateRepository,
	userRPMCache UserRPMCache,
	billingCacheService *BillingCacheService,
	proxyProber ProxyExitInfoProber,
	proxyLatencyCache ProxyLatencyCache,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	entClient *dbent.Client,
	settingService *SettingService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	userSubRepo UserSubscriptionRepository,
	privacyClientFactory PrivacyClientFactory,
	runtimeBlocker AccountRuntimeBlocker,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	affiliateService *AffiliateService,
	channelCacheInvalidator ChannelCacheInvalidator,
) AdminService {
	return &adminServiceImpl{
		userRepo:             userRepo,
		groupRepo:            groupRepo,
		groupDuplicateRepo:   groupRepo,
		groupSortOrderRepo:   groupRepo,
		accountRepo:          accountRepo,
		accountDuplicateRepo: accountRepo,
		proxyRepo:            proxyRepo,
		apiKeyRepo:           apiKeyRepo,
		redeemCodeRepo:       redeemCodeRepo,
		userGroupRateRepo:    userGroupRateRepo,
		userRPMCache:         userRPMCache,
		billingCacheService:  billingCacheService,
		proxyProber:          proxyProber,
		proxyLatencyCache:    proxyLatencyCache,
		authCacheInvalidator: authCacheInvalidator,
		entClient:            entClient,
		settingService:       settingService,
		defaultSubAssigner:   defaultSubAssigner,
		userSubRepo:          userSubRepo,
		privacyClientFactory: privacyClientFactory,
		runtimeBlocker:       runtimeBlocker,
		httpUpstream:         httpUpstream,
		tlsFPProfileService:  tlsFPProfileService,
		affiliateService:     affiliateService,

		channelCacheInvalidator: channelCacheInvalidator,
	}
}

func (s *adminServiceImpl) UpdateRedeemCode(ctx context.Context, id int64, input *UpdateRedeemCodeInput) (*RedeemCode, error) {
	if input == nil {
		return nil, infraerrors.BadRequest("REDEEM_CODE_UPDATE_REQUIRED", "update payload is required")
	}
	err := s.runRedeemCodeMutationTx(ctx, func(opCtx context.Context) error {
		code, err := s.redeemCodeRepo.GetByIDForUpdate(opCtx, id)
		if err != nil {
			return err
		}
		if !isEditableRedeemCodeType(code.Type) {
			return infraerrors.Conflict("REDEEM_CODE_SYSTEM_RECORD", "system redeem records cannot be updated")
		}

		if input.MaxUses != nil {
			if *input.MaxUses < 0 {
				return infraerrors.BadRequest("REDEEM_CODE_MAX_USES_INVALID", "max_uses must be greater than or equal to 0")
			}
			if *input.MaxUses > 0 && *input.MaxUses < code.UsedCount {
				return infraerrors.BadRequest("REDEEM_CODE_MAX_USES_BELOW_USED", "max_uses cannot be less than used_count")
			}
			code.MaxUses = *input.MaxUses
		}

		if input.ExpiresAtSet {
			code.ExpiresAt = input.ExpiresAt
		}

		if input.Value != nil || input.PlanID != nil {
			// 已兑换记录没有面值快照，修改面值或套餐会让历史展示与实际发放权益不一致。
			if code.UsedCount > 0 {
				return infraerrors.Conflict("REDEEM_CODE_VALUE_LOCKED", "value or plan cannot be updated after the code has been redeemed")
			}
			if err := s.applyRedeemCodeValueUpdate(opCtx, code, input); err != nil {
				return err
			}
		}

		if code.Type == RedeemTypeInvitation {
			code.MaxUses = 1
		}
		if code.Status == StatusExpired && !code.IsNaturallyExpired() {
			// 更新次数或过期时间后，允许管理员把手动过期的普通兑换码恢复为可兑换状态。
			code.Status = StatusUnused
		}
		code.Status = code.PersistedStatus()
		if err := s.redeemCodeRepo.Update(opCtx, code); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.redeemCodeRepo.GetByID(ctx, id)
}

func (s *adminServiceImpl) applyRedeemCodeValueUpdate(ctx context.Context, code *RedeemCode, input *UpdateRedeemCodeInput) error {
	switch code.Type {
	case RedeemTypeBalance:
		if input.PlanID != nil {
			return infraerrors.BadRequest("REDEEM_CODE_PLAN_UNSUPPORTED", "plan_id is only supported for subscription redeem codes")
		}
		if input.Value != nil {
			code.Value = *input.Value
		}
	case RedeemTypeConcurrency:
		if input.PlanID != nil {
			return infraerrors.BadRequest("REDEEM_CODE_PLAN_UNSUPPORTED", "plan_id is only supported for subscription redeem codes")
		}
		if input.Value != nil {
			if *input.Value == 0 || *input.Value != float64(int(*input.Value)) {
				return infraerrors.BadRequest("REDEEM_CODE_VALUE_INVALID", "concurrency value must be a non-zero integer")
			}
			code.Value = *input.Value
		}
	case RedeemTypeSubscription:
		if input.Value != nil {
			return infraerrors.BadRequest("REDEEM_CODE_VALUE_UNSUPPORTED", "value is not editable for subscription redeem codes")
		}
		if input.PlanID != nil {
			if *input.PlanID <= 0 {
				return infraerrors.BadRequest("REDEEM_CODE_PLAN_REQUIRED", "plan_id is required for subscription type")
			}
			if _, err := s.entClient.SubscriptionPlan.Get(ctx, *input.PlanID); err != nil {
				return fmt.Errorf("plan not found: %w", err)
			}
			code.PlanID = input.PlanID
		}
	case RedeemTypeInvitation:
		if input.Value != nil || input.PlanID != nil {
			return infraerrors.BadRequest("REDEEM_CODE_VALUE_UNSUPPORTED", "invitation code value cannot be updated")
		}
	}
	return nil
}

func (s *adminServiceImpl) attachAccountProxyForValidation(ctx context.Context, account *Account) {
	if s == nil || s.proxyRepo == nil || account == nil || account.Proxy != nil || account.ProxyID == nil || *account.ProxyID <= 0 {
		return
	}
	if proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID); err == nil && proxy != nil {
		account.Proxy = proxy
	}
}

// clearOtherPlatformDefaultGroups 清理同平台的其他默认分组。
// 只有当当前分组准备成为默认分组时才会执行，避免无意义的额外更新。
func (s *adminServiceImpl) clearOtherPlatformDefaultGroups(ctx context.Context, platform string, excludeID int64, enableDefault bool) error {
	if !enableDefault {
		return nil
	}

	groups, err := s.groupRepo.ListActiveByPlatformLite(ctx, platform)
	if err != nil {
		return fmt.Errorf("list active groups by platform: %w", err)
	}
	for i := range groups {
		group := groups[i]
		if group.ID == excludeID || !group.IsDefault {
			continue
		}
		group.IsDefault = false
		if err := s.groupRepo.Update(ctx, &group); err != nil {
			return translateGroupDefaultConflict(err)
		}
	}
	return nil
}

func (s *adminServiceImpl) createAppliedAdjustmentRedeemRecord(ctx context.Context, userID int64, codeType string, value float64, notes string) error {
	code, err := GenerateRedeemCode()
	if err != nil {
		return fmt.Errorf("generate adjustment redeem code: %w", err)
	}

	usedAt := time.Now()
	record := &RedeemCode{
		Code:      code,
		Type:      codeType,
		Value:     value,
		Status:    StatusUsed,
		MaxUses:   1,
		UsedCount: 1,
		UsedBy:    &userID,
		UsedAt:    &usedAt,
		Notes:     notes,
	}

	if s.entClient == nil {
		if err := s.redeemCodeRepo.Create(ctx, record); err != nil {
			return fmt.Errorf("create adjustment redeem code: %w", err)
		}
		if err := s.redeemCodeRepo.CreateUsage(ctx, &RedeemCodeUsage{
			RedeemCodeID: record.ID,
			UserID:       userID,
			UsedAt:       usedAt,
		}); err != nil {
			return fmt.Errorf("create adjustment redeem usage: %w", err)
		}
		return nil
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin adjustment redeem transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.redeemCodeRepo.Create(txCtx, record); err != nil {
		return fmt.Errorf("create adjustment redeem code: %w", err)
	}
	if err := s.redeemCodeRepo.CreateUsage(txCtx, &RedeemCodeUsage{
		RedeemCodeID: record.ID,
		UserID:       userID,
		UsedAt:       usedAt,
	}); err != nil {
		return fmt.Errorf("create adjustment redeem usage: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit adjustment redeem transaction: %w", err)
	}
	return nil
}

func (s *adminServiceImpl) qoderRefreshHTTPUpstream() HTTPUpstream {
	if s == nil {
		return nil
	}
	return s.httpUpstream
}

func (s *adminServiceImpl) qoderRefreshTLSFingerprintService() *TLSFingerprintProfileService {
	if s == nil {
		return nil
	}
	return s.tlsFPProfileService
}

// runGroupMutationTx 在可用时为分组变更开启事务，保证“切换默认组”过程原子化。
func (s *adminServiceImpl) runGroupMutationTx(ctx context.Context, fn func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil || s.entClient == nil {
		return fn(ctx)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin group mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit group mutation transaction: %w", err)
	}
	return nil
}

// runRedeemCodeMutationTx 在可用时为兑换码变更开启事务，保证行锁校验和写入使用同一个快照。
func (s *adminServiceImpl) runRedeemCodeMutationTx(ctx context.Context, fn func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil || s.entClient == nil {
		return fn(ctx)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin redeem code mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit redeem code mutation transaction: %w", err)
	}
	return nil
}

// validateUnavailableFallbackGroup 校验分组不可用时的指定回退分组。
// 该回退会继承入口平台语义，因此必须指向同平台且当前可用的分组。
func (s *adminServiceImpl) validateUnavailableFallbackGroup(ctx context.Context, currentGroupID int64, platform string, fallbackGroupID int64) error {
	if currentGroupID > 0 && currentGroupID == fallbackGroupID {
		return fmt.Errorf("cannot set self as unavailable fallback group")
	}
	fallbackGroup, err := s.groupRepo.GetByIDLite(ctx, fallbackGroupID)
	if err != nil {
		return fmt.Errorf("unavailable fallback group not found: %w", err)
	}
	if fallbackGroup.Platform != platform {
		return fmt.Errorf("unavailable fallback group must use the same platform")
	}
	if !fallbackGroup.IsActive() {
		return fmt.Errorf("unavailable fallback group must be active")
	}
	return nil
}

func configuredModelsListCandidateIDs(accounts []Account, platform string) []string {
	modelSet := make(map[string]struct{})
	hasAnyConfiguredModels := false
	for _, acc := range accounts {
		if acc.Platform != platform {
			continue
		}
		requestModels := acc.GetConfiguredRequestModels()
		if len(requestModels) == 0 {
			continue
		}
		hasAnyConfiguredModels = true
		for _, model := range requestModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			modelSet[model] = struct{}{}
		}
	}
	if !hasAnyConfiguredModels {
		return nil
	}

	// 候选项按字典序稳定输出，避免编辑分组时下拉列表随机抖动。
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func filterModelsListCandidates(candidates []string, selectedModels []string) []string {
	normalizedSelected := normalizeGroupModelsListConfig(GroupModelsListConfig{
		Enabled: true,
		Models:  selectedModels,
	}).Models
	if len(normalizedSelected) == 0 {
		return nil
	}

	if len(candidates) == 0 {
		return normalizedSelected
	}

	allowed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			allowed = append(allowed, candidate)
		}
	}

	// 按自定义模型列表顺序输出，确保探测下拉与管理员配置顺序一致。
	filtered := make([]string, 0, len(normalizedSelected))
	for _, model := range normalizedSelected {
		if modelsListCandidateAllowsModel(allowed, model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func isEditableRedeemCodeType(codeType string) bool {
	switch codeType {
	case RedeemTypeBalance, RedeemTypeConcurrency, RedeemTypeSubscription, RedeemTypeInvitation:
		return true
	default:
		return false
	}
}

func modelsListCandidateAllowsModel(availablePatterns []string, model string) bool {
	for _, pattern := range availablePatterns {
		if pattern == model {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

// normalizeGroupDefaultState 统一处理默认分组的最终状态。
// 非 active 分组不保留默认标记，避免出现“默认但不可用”的歧义。
func normalizeGroupDefaultState(group *Group) {
	if group == nil {
		return
	}
	if group.Status != StatusActive {
		group.IsDefault = false
	}
}

func translateGroupDefaultConflict(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "groups_platform_default_active_unique") {
		return infraerrors.Conflict("GROUP_DEFAULT_CONFLICT", "default group already exists for this platform").WithCause(err)
	}
	return err
}

type UpdateRedeemCodeInput struct {
	Value        *float64
	MaxUses      *int
	ExpiresAt    *time.Time
	ExpiresAtSet bool
	PlanID       *int64 // 订阅类型专用：关联的套餐ID
}
