package billing

import (
	context "context"
	time "time"
)

// APIKeyRateLimitCacheData holds rate limit usage data cached in Redis.
type APIKeyRateLimitCacheData struct {
	Usage5h  float64 `json:"usage_5h"`
	Usage1d  float64 `json:"usage_1d"`
	Usage7d  float64 `json:"usage_7d"`
	Window5h int64   `json:"window_5h"` // unix timestamp, 0 = not started
	Window1d int64   `json:"window_1d"`
	Window7d int64   `json:"window_7d"`
}

// UserPlatformQuotaKey 标识一个 user×platform，用于脏集出入与批量读。
type UserPlatformQuotaKey struct {
	UserID   int64
	Platform string
}

// UserPlatformQuotaCacheEntry Redis hash 反序列化结果。
//
// SchemaVersion 用于向后兼容：
//   - 0（旧 entry，无 SchemaVersion 字段）→ 视为 cache MISS，强制 refresh
//   - 1（当前版本）→ 包含 limits 和 window_start，可免 DB 查询
//
// limit 字段为 nil 表示"无限额"（DB 中对应列为 NULL）。
const UserPlatformQuotaCacheSchemaV1 = int64(1)

type UserPlatformQuotaCacheEntry struct {
	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64
	Version         int64
	SchemaVersion   int64

	// 以下字段仅在 SchemaVersion >= 1 时有效
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time
}

// BillingCache defines cache operations for billing service
type BillingCache interface {
	// Balance operations
	GetUserBalance(ctx context.Context, userID int64) (float64, error)
	SetUserBalance(ctx context.Context, userID int64, balance float64) error
	DeductUserBalance(ctx context.Context, userID int64, amount float64) error
	InvalidateUserBalance(ctx context.Context, userID int64) error

	// API Key rate limit operations
	GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*APIKeyRateLimitCacheData, error)
	SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *APIKeyRateLimitCacheData) error
	UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error

	// user × platform quota 缓存
	GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error)
	SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *UserPlatformQuotaCacheEntry, ttl time.Duration) error
	DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error
	// IncrUserPlatformQuotaUsageCache 在缓存命中时累加用量；缓存未命中（key 不存在）静默返回 nil。
	// markDirty=true 时将该 key 的 member 写入 Redis 脏集，供 flusher 批量回写 DB。
	IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error

	// 脏集读写，供 flusher 使用。
	PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]UserPlatformQuotaKey, error)
	ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []UserPlatformQuotaKey) error
	BatchGetUserPlatformQuotaCache(ctx context.Context, keys []UserPlatformQuotaKey) ([]*UserPlatformQuotaCacheEntry, error)
}
