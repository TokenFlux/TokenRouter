//go:build integration

package rediscache_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"

	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RedeemCacheSuite struct {
	rediscontainer.Suite
	cache *billingredis.RedeemCache
}

func (s *RedeemCacheSuite) SetupTest() {
	s.Suite.SetupTest()
	s.cache = billingredis.NewRedeemCache(s.RDB)
}

func (s *RedeemCacheSuite) TestGetRedeemAttemptCount_Missing() {
	missingUserID := int64(99999)
	count, err := s.cache.GetRedeemAttemptCount(s.Ctx, missingUserID)
	require.NoError(s.T(), err, "expected nil error for missing rate-limit key")
	require.Equal(s.T(), 0, count, "expected zero count for missing key")
}

func (s *RedeemCacheSuite) TestIncrementAndGetRedeemAttemptCount() {
	userID := int64(1)
	key := fmt.Sprintf("%s%d", "redeem:ratelimit:", userID)

	require.NoError(s.T(), s.cache.IncrementRedeemAttemptCount(s.Ctx, userID), "IncrementRedeemAttemptCount")
	count, err := s.cache.GetRedeemAttemptCount(s.Ctx, userID)
	require.NoError(s.T(), err, "GetRedeemAttemptCount")
	require.Equal(s.T(), 1, count, "count mismatch")

	ttl, err := s.RDB.TTL(s.Ctx, key).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, 1*time.Second, (24 * time.Hour))
}

func (s *RedeemCacheSuite) TestMultipleIncrements() {
	userID := int64(2)

	require.NoError(s.T(), s.cache.IncrementRedeemAttemptCount(s.Ctx, userID))
	require.NoError(s.T(), s.cache.IncrementRedeemAttemptCount(s.Ctx, userID))
	require.NoError(s.T(), s.cache.IncrementRedeemAttemptCount(s.Ctx, userID))

	count, err := s.cache.GetRedeemAttemptCount(s.Ctx, userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, count, "count after 3 increments")
}

func (s *RedeemCacheSuite) TestAcquireAndReleaseRedeemLock() {
	ok, err := s.cache.AcquireRedeemLock(s.Ctx, "CODE", 10*time.Second)
	require.NoError(s.T(), err, "AcquireRedeemLock")
	require.True(s.T(), ok)

	// Second acquire should fail
	ok, err = s.cache.AcquireRedeemLock(s.Ctx, "CODE", 10*time.Second)
	require.NoError(s.T(), err, "AcquireRedeemLock 2")
	require.False(s.T(), ok, "expected lock to be held")

	// Release
	require.NoError(s.T(), s.cache.ReleaseRedeemLock(s.Ctx, "CODE"), "ReleaseRedeemLock")

	// Now acquire should succeed
	ok, err = s.cache.AcquireRedeemLock(s.Ctx, "CODE", 10*time.Second)
	require.NoError(s.T(), err, "AcquireRedeemLock after release")
	require.True(s.T(), ok)
}

func (s *RedeemCacheSuite) TestAcquireRedeemLock_TTL() {
	lockKey := "redeem:lock:" + "CODE2"
	lockTTL := 15 * time.Second

	ok, err := s.cache.AcquireRedeemLock(s.Ctx, "CODE2", lockTTL)
	require.NoError(s.T(), err, "AcquireRedeemLock CODE2")
	require.True(s.T(), ok)

	ttl, err := s.RDB.TTL(s.Ctx, lockKey).Result()
	require.NoError(s.T(), err, "TTL lock key")
	s.AssertTTLWithin(ttl, 1*time.Second, lockTTL)
}

func (s *RedeemCacheSuite) TestReleaseRedeemLock_Idempotent() {
	// Release a lock that doesn't exist should not error
	require.NoError(s.T(), s.cache.ReleaseRedeemLock(s.Ctx, "NONEXISTENT"))

	// Acquire, release, release again
	ok, err := s.cache.AcquireRedeemLock(s.Ctx, "IDEMPOTENT", 10*time.Second)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	require.NoError(s.T(), s.cache.ReleaseRedeemLock(s.Ctx, "IDEMPOTENT"))
	require.NoError(s.T(), s.cache.ReleaseRedeemLock(s.Ctx, "IDEMPOTENT"), "second release should be idempotent")
}

func TestRedeemCacheSuite(t *testing.T) {
	suite.Run(t, new(RedeemCacheSuite))
}
