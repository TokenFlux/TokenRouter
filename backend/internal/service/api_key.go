// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	ip "github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	time "time"
)

const StatusAPIKeyActive = apikey.StatusAPIKeyActive

const StatusAPIKeyDisabled = apikey.StatusAPIKeyDisabled

const StatusAPIKeyQuotaExhausted = apikey.StatusAPIKeyQuotaExhausted

const StatusAPIKeyExpired = apikey.StatusAPIKeyExpired

const APIKeyFastModePolicyFollowRequest = apikey.APIKeyFastModePolicyFollowRequest

const APIKeyFastModePolicyForceOn = apikey.APIKeyFastModePolicyForceOn

const APIKeyFastModePolicyForceOff = apikey.APIKeyFastModePolicyForceOff

const APIKeyBillingModeAuto = apikey.APIKeyBillingModeAuto

const APIKeyBillingModeSubscription = apikey.APIKeyBillingModeSubscription

const APIKeyBillingModeBalance = apikey.APIKeyBillingModeBalance

// NormalizeAPIKeyFastModePolicy 委托 Key 模块的唯一实现。
func NormalizeAPIKeyFastModePolicy(value string) (string, bool) {
	return apikey.NormalizeAPIKeyFastModePolicy(value)
}

// NormalizeAPIKeyBillingMode 委托 Key 模块的唯一实现。
func NormalizeAPIKeyBillingMode(value string) (string, bool) {
	return apikey.NormalizeAPIKeyBillingMode(value)
}

// APIKeyEffectiveBillingMode 委托 Key 模块的唯一实现。
func APIKeyEffectiveBillingMode(key *APIKey) string {
	keyView := APIKeyView(key)
	result0 := apikey.APIKeyEffectiveBillingMode(keyView)
	ApplyAPIKeyView(key, keyView)
	return result0
}

const RateLimitWindow5h = apikey.RateLimitWindow5h

const RateLimitWindow1d = apikey.RateLimitWindow1d

const RateLimitWindow7d = apikey.RateLimitWindow7d

// IsWindowExpired 委托 Key 模块的唯一实现。
func IsWindowExpired(windowStart *time.Time, duration time.Duration) bool {
	return apikey.IsWindowExpired(windowStart, duration)
}

type APIKey struct {
	ID     int64
	UserID int64
	TeamID *int64
	// TeamOwnerDisabled 表示团队 Owner 已锁定该 Key，Member 无权解除。
	TeamOwnerDisabled bool
	Key               string
	Name              string
	GroupID           *int64
	// IsComposite 表示该 Key 通过模型前缀选择多个分组。
	IsComposite bool
	// CompositeGroups 按用户配置顺序保存复合 Key 的分组映射。
	CompositeGroups []APIKeyCompositeGroup
	Status          string
	// FastModePolicy 控制该 Key 的请求级 Fast 模式，系统策略仍拥有更高优先级。
	FastModePolicy string
	// BillingMode 控制该 Key 的资金来源；subscription 模式必须携带 PreferredSubscriptionID。
	BillingMode             string
	PreferredSubscriptionID *int64
	// ModelMapping 在渠道和账号映射前把客户端模型重定向到内部目标模型。
	ModelMapping map[string]string
	IPWhitelist  []string
	IPBlacklist  []string
	// 预编译的 IP 规则，用于认证热路径避免重复 ParseIP/ParseCIDR。
	CompiledIPWhitelist *ip.CompiledIPRules `json:"-"`
	CompiledIPBlacklist *ip.CompiledIPRules `json:"-"`
	LastUsedAt          *time.Time
	LastUsedIP          *string // 来自该 Key 最新一条带 IP 的用量日志。
	CreatedAt           time.Time
	UpdatedAt           time.Time
	// User 表示实际承担权限和费用的用户；团队 Key 中为当前 Owner。
	User *User
	// ActorUser 表示创建并实际使用该 Key 的成员；个人 Key 与 User 相同。
	ActorUser      *User
	Team           *Team
	TeamMembership *TeamMembership
	Group          *Group
	// FallbackToDefaultGroupWhenUnavailable 控制绑定分组停用时是否回退到同平台默认分组。
	FallbackToDefaultGroupWhenUnavailable bool
	// CurrentConcurrency 表示当前 API Key 的实时活跃请求数。
	CurrentConcurrency int
	// ManagedBy 标记服务端托管的隐藏 Key（如创作台执行 Key 'creative_studio'），
	// 普通用户接口不得暴露或操作此类 Key；nil 表示普通用户 Key。
	ManagedBy *string

	// Quota fields
	Quota     float64    // Quota limit in USD (0 = unlimited)
	QuotaUsed float64    // Used quota amount
	ExpiresAt *time.Time // Expiration time (nil = never expires)

	// Rate limit fields
	RateLimit5h   float64    // Rate limit in USD per 5h (0 = unlimited)
	RateLimit1d   float64    // Rate limit in USD per 1d (0 = unlimited)
	RateLimit7d   float64    // Rate limit in USD per 7d (0 = unlimited)
	Usage5h       float64    // Used amount in current 5h window
	Usage1d       float64    // Used amount in current 1d window
	Usage7d       float64    // Used amount in current 7d window
	Window5hStart *time.Time // Start of current 5h window
	Window1dStart *time.Time // Start of current 1d window
	Window7dStart *time.Time // Start of current 7d window
}

// APIKeyCompositeGroup 表示复合 API Key 的一个分组前缀映射。
type APIKeyCompositeGroup struct {
	ID               int64
	APIKeyID         int64
	GroupID          int64
	Prefix           string
	NormalizedPrefix string
	SortOrder        int
	// UserGroupRPMOverride 是认证快照中的请求期配置，不写入映射表。
	UserGroupRPMOverride *int
	Group                *Group
}

// IsActive 委托 Key 模块的唯一实现。
func (k *APIKey) IsActive() bool {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.IsActive()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// HasRateLimits 委托 Key 模块的唯一实现。
func (k *APIKey) HasRateLimits() bool {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.HasRateLimits()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// IsExpired 委托 Key 模块的唯一实现。
func (k *APIKey) IsExpired() bool {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.IsExpired()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// IsQuotaExhausted 委托 Key 模块的唯一实现。
func (k *APIKey) IsQuotaExhausted() bool {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.IsQuotaExhausted()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// GetQuotaRemaining 委托 Key 模块的唯一实现。
func (k *APIKey) GetQuotaRemaining() float64 {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.GetQuotaRemaining()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// GetDaysUntilExpiry 委托 Key 模块的唯一实现。
func (k *APIKey) GetDaysUntilExpiry() int {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.GetDaysUntilExpiry()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// EffectiveUsage5h 委托 Key 模块的唯一实现。
func (k *APIKey) EffectiveUsage5h() float64 {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.EffectiveUsage5h()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// EffectiveUsage1d 委托 Key 模块的唯一实现。
func (k *APIKey) EffectiveUsage1d() float64 {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.EffectiveUsage1d()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

// EffectiveUsage7d 委托 Key 模块的唯一实现。
func (k *APIKey) EffectiveUsage7d() float64 {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0 := coreKey.EffectiveUsage7d()
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0
}

type APIKeyListFilters = apikey.APIKeyListFilters
