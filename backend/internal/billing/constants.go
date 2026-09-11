// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

// Redeem type constants
const (
	RedeemTypeBalance      = "balance"
	RedeemTypeConcurrency  = "concurrency"
	RedeemTypeSubscription = "subscription"
	RedeemTypeInvitation   = "invitation"
)

// Admin adjustment type constants
const (
	AdjustmentTypeAdminBalance     = "admin_balance"     // 管理员调整余额
	AdjustmentTypeAdminConcurrency = "admin_concurrency" // 管理员调整并发数
)

// Subscription status constants
const (
	SubscriptionStatusActive    = "active"
	SubscriptionStatusPending   = "pending"
	SubscriptionStatusExpired   = "expired"
	SubscriptionStatusSuspended = "suspended"
)

const SubscriptionStatusRevoked = "revoked"

const (
	StatusUnused   = "unused"
	StatusUsed     = "used"
	StatusExpired  = "expired"
	StatusDisabled = "disabled"
	StatusActive   = "active"
)
