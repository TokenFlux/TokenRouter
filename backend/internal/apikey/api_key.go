// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	time "time"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	ipmatch "github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// API Key status constants
const (
	StatusAPIKeyActive         = "active"
	StatusAPIKeyDisabled       = "disabled"
	StatusAPIKeyQuotaExhausted = "quota_exhausted"
	StatusAPIKeyExpired        = "expired"
)

// API Key Fast 模式策略常量。
const (
	APIKeyFastModePolicyFollowRequest = "follow_request"
	APIKeyFastModePolicyForceOn       = "force_on"
	APIKeyFastModePolicyForceOff      = "force_off"
)

// API Key 结算模式常量。auto 保持存量 Key 的订阅优先、余额补足行为。
const (
	APIKeyBillingModeAuto         = "auto"
	APIKeyBillingModeSubscription = "subscription"
	APIKeyBillingModeBalance      = "balance"
)

// NormalizeAPIKeyFastModePolicy 校验并规范化 API Key Fast 模式策略。
// 空值用于兼容旧客户端，按跟随下游请求处理。
func NormalizeAPIKeyFastModePolicy(value string) (string, bool) {
	switch value {
	case "", APIKeyFastModePolicyFollowRequest:
		return APIKeyFastModePolicyFollowRequest, true
	case APIKeyFastModePolicyForceOn, APIKeyFastModePolicyForceOff:
		return value, true
	default:
		return "", false
	}
}

// NormalizeAPIKeyBillingMode 委托唯一资金来源规则。
func NormalizeAPIKeyBillingMode(value string) (string, bool) {
	return billing.NormalizeAPIKeyBillingMode(value)
}

// APIKeyEffectiveBillingMode 返回 Key 实际生效的结算模式。
// 历史记录在缺少该字段时按 auto 处理，避免滚动升级期间错误拒绝请求。
func APIKeyEffectiveBillingMode(key *APIKey) string {
	if key == nil {
		return APIKeyBillingModeAuto
	}
	mode, ok := NormalizeAPIKeyBillingMode(key.BillingMode)
	if !ok {
		return APIKeyBillingModeAuto
	}
	return mode
}

// Rate limit window durations
const (
	RateLimitWindow5h = 5 * time.Hour
	RateLimitWindow1d = 24 * time.Hour
	RateLimitWindow7d = 7 * 24 * time.Hour
)

// IsWindowExpired returns true if the window starting at windowStart has exceeded the given duration.
// A nil windowStart is treated as expired — no initialized window means any accumulated usage is stale.
func IsWindowExpired(start *time.Time, window time.Duration) bool {
	return billing.IsWindowExpired(start, window)
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
	CompiledIPWhitelist *ipmatch.CompiledIPRules `json:"-"`
	CompiledIPBlacklist *ipmatch.CompiledIPRules `json:"-"`
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
	Group          *routing.
		// FallbackToDefaultGroupWhenUnavailable 控制绑定分组停用时是否回退到同平台默认分组。
		Group

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
	Group                *routing.Group
}

func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive && !k.TeamOwnerDisabled
}

// HasRateLimits returns true if any rate limit window is configured
func (k *APIKey) HasRateLimits() bool {
	return k.RateLimit5h > 0 || k.RateLimit1d > 0 || k.RateLimit7d > 0
}

// IsExpired checks if the API key has expired
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return k.IsExpiredAt(time.Now())
}

// IsQuotaExhausted checks if the API key quota is exhausted
func (k *APIKey) IsQuotaExhausted() bool {
	if k.Quota <= 0 {
		return false // unlimited
	}
	return k.QuotaUsed >= k.Quota
}

// GetQuotaRemaining returns remaining quota (-1 for unlimited)
func (k *APIKey) GetQuotaRemaining() float64 {
	if k.Quota <= 0 {
		return -1 // unlimited
	}
	remaining := k.Quota - k.QuotaUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetDaysUntilExpiry returns days until expiry (-1 for never expires)
func (k *APIKey) GetDaysUntilExpiry() int {
	if k.ExpiresAt == nil {
		return -1 // never expires
	}
	duration := time.Until(*k.ExpiresAt)
	if duration < 0 {
		return 0
	}
	return int(duration.Hours() / 24)
}

// EffectiveUsage5h returns the 5h window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage5h() float64 {
	if IsWindowExpired(k.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return k.Usage5h
}

// EffectiveUsage1d returns the 1d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage1d() float64 {
	if IsWindowExpired(k.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return k.Usage1d
}

// EffectiveUsage7d returns the 7d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage7d() float64 {
	if IsWindowExpired(k.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return k.Usage7d
}

// APIKeyListFilters holds optional filtering parameters for listing API keys.
type APIKeyListFilters struct {
	Search  string
	Status  string
	GroupID *int64 // nil=不筛选, 0=无分组, >0=指定分组
	Scope   string // personal 或 team；空值兼容历史调用并返回全部
}

// IsExpiredAt 允许用例使用已经注入的时钟，旧无参入口继续保持默认行为。
func (k *APIKey) IsExpiredAt(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}
