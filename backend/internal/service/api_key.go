package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
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

// Rate limit window durations
const (
	RateLimitWindow5h = 5 * time.Hour
	RateLimitWindow1d = 24 * time.Hour
	RateLimitWindow7d = 7 * 24 * time.Hour
)

// IsWindowExpired returns true if the window starting at windowStart has exceeded the given duration.
// A nil windowStart is treated as expired — no initialized window means any accumulated usage is stale.
func IsWindowExpired(windowStart *time.Time, duration time.Duration) bool {
	return windowStart == nil || time.Since(*windowStart) >= duration
}

type APIKey struct {
	ID      int64
	UserID  int64
	Key     string
	Name    string
	GroupID *int64
	Status  string
	// FastModePolicy 控制该 Key 的请求级 Fast 模式，系统策略仍拥有更高优先级。
	FastModePolicy string
	IPWhitelist    []string
	IPBlacklist    []string
	// 预编译的 IP 规则，用于认证热路径避免重复 ParseIP/ParseCIDR。
	CompiledIPWhitelist *ip.CompiledIPRules `json:"-"`
	CompiledIPBlacklist *ip.CompiledIPRules `json:"-"`
	LastUsedAt          *time.Time
	LastUsedIP          *string // 来自该 Key 最新一条带 IP 的用量日志。
	CreatedAt           time.Time
	UpdatedAt           time.Time
	User                *User
	Group               *Group
	// DataSharingNoticeVersion 记录当前 Key 最近确认的数据共享须知版本。
	DataSharingNoticeVersion int
	// DataSharingConfirmedGroupID 记录最近一次确认对应的数据共享分组。
	DataSharingConfirmedGroupID *int64
	// DataSharingConfirmedAt 记录最近一次用户点击确认的时间。
	DataSharingConfirmedAt *time.Time
	// FallbackToDefaultGroupWhenUnavailable 控制绑定分组停用时是否回退到同平台默认分组。
	FallbackToDefaultGroupWhenUnavailable bool
	// CurrentConcurrency 表示当前 API Key 的实时活跃请求数。
	CurrentConcurrency int

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

func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive
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
	return time.Now().After(*k.ExpiresAt)
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
}
