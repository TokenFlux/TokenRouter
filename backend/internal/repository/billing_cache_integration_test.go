//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type BillingCacheSuite struct {
	IntegrationRedisSuite
}

func (s *BillingCacheSuite) TestUserBalance() {
	tests := []struct {
		name string
		fn   func(ctx context.Context, rdb *redis.Client, cache service.BillingCache)
	}{
		{
			name: "missing_key_returns_redis_nil",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				_, err := cache.GetUserBalance(ctx, 1)
				require.ErrorIs(s.T(), err, redis.Nil, "expected redis.Nil for missing balance key")
			},
		},
		{
			name: "deduct_on_nonexistent_is_noop",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				userID := int64(1)
				balanceKey := fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)

				require.NoError(s.T(), cache.DeductUserBalance(ctx, userID, 1), "DeductUserBalance should not error")

				_, err := rdb.Get(ctx, balanceKey).Result()
				require.ErrorIs(s.T(), err, redis.Nil, "expected missing key after deduct on non-existent")
			},
		},
		{
			name: "set_and_get_with_ttl",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				userID := int64(2)
				balanceKey := fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)

				require.NoError(s.T(), cache.SetUserBalance(ctx, userID, 10.5), "SetUserBalance")

				got, err := cache.GetUserBalance(ctx, userID)
				require.NoError(s.T(), err, "GetUserBalance")
				require.Equal(s.T(), 10.5, got, "balance mismatch")

				ttl, err := rdb.TTL(ctx, balanceKey).Result()
				require.NoError(s.T(), err, "TTL")
				s.AssertTTLWithin(ttl, 1*time.Second, billingCacheTTL)
			},
		},
		{
			name: "deduct_reduces_balance",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				userID := int64(3)

				require.NoError(s.T(), cache.SetUserBalance(ctx, userID, 10.5), "SetUserBalance")
				require.NoError(s.T(), cache.DeductUserBalance(ctx, userID, 2.25), "DeductUserBalance")

				got, err := cache.GetUserBalance(ctx, userID)
				require.NoError(s.T(), err, "GetUserBalance after deduct")
				require.Equal(s.T(), 8.25, got, "deduct mismatch")
			},
		},
		{
			name: "invalidate_removes_key",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				userID := int64(100)
				balanceKey := fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)

				require.NoError(s.T(), cache.SetUserBalance(ctx, userID, 50.0), "SetUserBalance")

				exists, err := rdb.Exists(ctx, balanceKey).Result()
				require.NoError(s.T(), err, "Exists")
				require.Equal(s.T(), int64(1), exists, "expected balance key to exist")

				require.NoError(s.T(), cache.InvalidateUserBalance(ctx, userID), "InvalidateUserBalance")

				exists, err = rdb.Exists(ctx, balanceKey).Result()
				require.NoError(s.T(), err, "Exists after invalidate")
				require.Equal(s.T(), int64(0), exists, "expected balance key to be removed after invalidate")

				_, err = cache.GetUserBalance(ctx, userID)
				require.ErrorIs(s.T(), err, redis.Nil, "expected redis.Nil after invalidate")
			},
		},
		{
			name: "deduct_refreshes_ttl",
			fn: func(ctx context.Context, rdb *redis.Client, cache service.BillingCache) {
				userID := int64(103)
				balanceKey := fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)

				require.NoError(s.T(), cache.SetUserBalance(ctx, userID, 100.0), "SetUserBalance")

				ttl1, err := rdb.TTL(ctx, balanceKey).Result()
				require.NoError(s.T(), err, "TTL before deduct")
				s.AssertTTLWithin(ttl1, 1*time.Second, billingCacheTTL)

				require.NoError(s.T(), cache.DeductUserBalance(ctx, userID, 25.0), "DeductUserBalance")

				balance, err := cache.GetUserBalance(ctx, userID)
				require.NoError(s.T(), err, "GetUserBalance")
				require.Equal(s.T(), 75.0, balance, "expected balance 75.0")

				ttl2, err := rdb.TTL(ctx, balanceKey).Result()
				require.NoError(s.T(), err, "TTL after deduct")
				s.AssertTTLWithin(ttl2, 1*time.Second, billingCacheTTL)
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			rdb := testRedis(s.T())
			cache := NewBillingCache(rdb)
			ctx := context.Background()

			tt.fn(ctx, rdb, cache)
		})
	}
}

// TestDeductUserBalance_ErrorPropagation 验证 P2-12 修复：
// Redis 真实错误应传播，key 不存在（redis.Nil）应返回 nil。
func (s *BillingCacheSuite) TestDeductUserBalance_ErrorPropagation() {
	tests := []struct {
		name      string
		fn        func(ctx context.Context, cache service.BillingCache)
		expectErr bool
	}{
		{
			name: "key_not_exists_returns_nil",
			fn: func(ctx context.Context, cache service.BillingCache) {
				// key 不存在时，Lua 脚本返回 0（redis.Nil），应返回 nil 而非错误
				err := cache.DeductUserBalance(ctx, 99999, 1.0)
				require.NoError(s.T(), err, "DeductUserBalance on non-existent key should return nil")
			},
		},
		{
			name: "existing_key_deducts_successfully",
			fn: func(ctx context.Context, cache service.BillingCache) {
				require.NoError(s.T(), cache.SetUserBalance(ctx, 200, 50.0))
				err := cache.DeductUserBalance(ctx, 200, 10.0)
				require.NoError(s.T(), err, "DeductUserBalance should succeed")

				bal, err := cache.GetUserBalance(ctx, 200)
				require.NoError(s.T(), err)
				require.Equal(s.T(), 40.0, bal, "余额应为 40.0")
			},
		},
		{
			name: "cancelled_context_propagates_error",
			fn: func(ctx context.Context, cache service.BillingCache) {
				require.NoError(s.T(), cache.SetUserBalance(ctx, 201, 50.0))

				cancelCtx, cancel := context.WithCancel(ctx)
				cancel() // 立即取消

				err := cache.DeductUserBalance(cancelCtx, 201, 10.0)
				require.Error(s.T(), err, "cancelled context should propagate error")
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			rdb := testRedis(s.T())
			cache := NewBillingCache(rdb)
			ctx := context.Background()
			tt.fn(ctx, cache)
		})
	}
}

func TestBillingCacheSuite(t *testing.T) {
	suite.Run(t, new(BillingCacheSuite))
}
