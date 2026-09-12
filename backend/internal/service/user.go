package service

import (
	"time"
)

type User struct {
	ID             int64
	Email          string
	Username       string
	Notes          string
	AvatarURL      string
	AvatarSource   string
	AvatarMIME     string
	AvatarByteSize int
	AvatarSHA256   string
	PasswordHash   string
	Role           string
	Balance        float64
	FrozenBalance  float64
	Concurrency    int
	Status         string
	AllowedGroups  []int64
	// DisabledPublicGroups 保存管理员为该用户显式禁止使用的公开分组 ID。
	DisabledPublicGroups []int64
	// GroupRestrictionsLoaded 表示用户分组授权/禁用列表已从仓储加载，避免认证缓存误判空列表。
	GroupRestrictionsLoaded bool
	TokenVersion            int64 // Incremented on password change to invalidate existing tokens
	// TokenVersionResolved indicates TokenVersion already contains the fingerprint-derived
	// value expected in JWT claims and refresh-token state.
	TokenVersionResolved bool
	SignupSource         string
	LastLoginAt          *time.Time
	LastActiveAt         *time.Time
	LastUsedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            *time.Time // 非 nil 表示用户已软删除

	// GroupRates 用户专属分组倍率配置
	// map[groupID]rateMultiplier
	GroupRates map[int64]float64

	// TOTP 双因素认证字段
	TotpSecretEncrypted *string    // AES-256-GCM 加密的 TOTP 密钥
	TotpEnabled         bool       // 是否启用 TOTP
	TotpEnabledAt       *time.Time // TOTP 启用时间

	// 余额不足通知
	BalanceNotifyEnabled       bool
	BalanceNotifyThresholdType string // "fixed" (default) | "percentage"
	BalanceNotifyThreshold     *float64
	BalanceNotifyExtraEmails   []NotifyEmailEntry
	TotalRecharged             float64

	// RPMLimit 用户级每分钟请求数上限（0 = 不限制）。仅在所用分组未设置 rpm_limit
	// 且该 (用户, 分组) 无 rpm_override 时作为全局兜底生效，计数键 rpm:u:{userID}:{min}。
	RPMLimit int
	// APIKeyLimit 是用户可创建的 API Key 数量上限，0 表示不限制。
	APIKeyLimit int

	// UserGroupRPMOverride 来自 auth cache snapshot 的 (user, group) RPM 覆盖值。
	// nil = 该 API Key 对应的 (user, group) 无 override；非 nil 时 checkRPM 直接使用，
	// 避免每请求查 DB。字段不持久化到数据库。
	UserGroupRPMOverride *int

	APIKeys       []APIKey
	Subscriptions []UserSubscription
}

func (u *User) IsAdmin() bool { return IdentityUser(u).IsAdmin() }

func (u *User) IsActive() bool { return IdentityUser(u).IsActive() }

func (u *User) CanBindGroup(groupID int64, isExclusive bool) bool {
	return IdentityUser(u).CanBindGroup(groupID, isExclusive)
}

func (u *User) SetPassword(password string) error {
	value := IdentityUser(u)
	err := value.SetPassword(password)
	u.PasswordHash = value.PasswordHash
	return err
}

func (u *User) CheckPassword(password string) bool { return IdentityUser(u).CheckPassword(password) }
