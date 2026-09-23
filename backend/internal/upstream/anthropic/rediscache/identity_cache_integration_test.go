//go:build integration

package rediscache_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type IdentityCacheSuite struct {
	rediscontainer.Suite
	cache *rediscache.FingerprintStore
}

func (s *IdentityCacheSuite) SetupTest() {
	s.Suite.SetupTest()
	cache, ok := rediscache.NewFingerprintStore(s.RDB).(*rediscache.FingerprintStore)
	s.Require().True(ok, "identity cache constructor type")
	s.cache = cache
}

func (s *IdentityCacheSuite) TestGetFingerprint_Missing() {
	_, err := s.cache.GetFingerprint(s.Ctx, 1)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing fingerprint")
}

func (s *IdentityCacheSuite) TestSetAndGetFingerprint() {
	fp := &anthropic.Fingerprint{ClientID: "c1", UserAgent: "ua"}
	require.NoError(s.T(), s.cache.SetFingerprint(s.Ctx, 1, fp), "SetFingerprint")
	gotFP, err := s.cache.GetFingerprint(s.Ctx, 1)
	require.NoError(s.T(), err, "GetFingerprint")
	require.Equal(s.T(), "c1", gotFP.ClientID)
	require.Equal(s.T(), "ua", gotFP.UserAgent)
}

func (s *IdentityCacheSuite) TestFingerprint_TTL() {
	fp := &anthropic.Fingerprint{ClientID: "c1", UserAgent: "ua"}
	require.NoError(s.T(), s.cache.SetFingerprint(s.Ctx, 2, fp))

	fpKey := fmt.Sprintf("%s%d", rediscache.FingerprintKeyPrefix, 2)
	ttl, err := s.RDB.TTL(s.Ctx, fpKey).Result()
	require.NoError(s.T(), err, "TTL fpKey")
	s.AssertTTLWithin(ttl, 1*time.Second, rediscache.FingerprintTTL)
}

func (s *IdentityCacheSuite) TestGetFingerprint_JSONCorruption() {
	fpKey := fmt.Sprintf("%s%d", rediscache.FingerprintKeyPrefix, 999)
	require.NoError(s.T(), s.RDB.Set(s.Ctx, fpKey, "invalid-json-data", 1*time.Minute).Err(), "Set invalid JSON")

	_, err := s.cache.GetFingerprint(s.Ctx, 999)
	require.Error(s.T(), err, "expected error for corrupted JSON")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected decoding error, not redis.Nil")
}

func (s *IdentityCacheSuite) TestSetFingerprint_Nil() {
	err := s.cache.SetFingerprint(s.Ctx, 100, nil)
	require.NoError(s.T(), err, "SetFingerprint(nil) should succeed")
}

func TestIdentityCacheSuite(t *testing.T) {
	suite.Run(t, new(IdentityCacheSuite))
}
