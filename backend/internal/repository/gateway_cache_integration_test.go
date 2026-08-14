//go:build integration

package repository

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GatewayCacheSuite struct {
	IntegrationRedisSuite
	cache service.GatewayCache
}

func (s *GatewayCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewGatewayCache(s.rdb)
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_Missing() {
	_, err := s.cache.GetSessionAccountID(s.ctx, 1, "nonexistent")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing session")
}

func (s *GatewayCacheSuite) TestSetAndGetSessionAccountID() {
	sessionID := "s1"
	accountID := int64(99)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sid, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err, "GetSessionAccountID")
	require.Equal(s.T(), accountID, sid, "session id mismatch")
}

func (s *GatewayCacheSuite) TestSessionAccountID_TTL() {
	sessionID := "s2"
	accountID := int64(100)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL sessionKey after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL() {
	sessionID := "s3"
	accountID := int64(101)
	groupID := int64(1)
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, initialTTL), "SetSessionAccountID")

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, refreshTTL), "RefreshSessionTTL")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL after Refresh")
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL_MissingKey() {
	// RefreshSessionTTL on a missing key should not error (no-op)
	err := s.cache.RefreshSessionTTL(s.ctx, 1, "missing-session", 1*time.Minute)
	require.NoError(s.T(), err, "RefreshSessionTTL on missing key should not error")
}

func (s *GatewayCacheSuite) TestDeleteSessionAccountID() {
	sessionID := "openai:s4"
	accountID := int64(102)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID), "DeleteSessionAccountID")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_CorruptedValue() {
	sessionID := "corrupted"
	groupID := int64(1)
	sessionKey := buildSessionKey(groupID, sessionID)

	// Set a non-integer value
	require.NoError(s.T(), s.rdb.Set(s.ctx, sessionKey, "not-a-number", 1*time.Minute).Err(), "Set invalid value")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.Error(s.T(), err, "expected error for corrupted value")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected parsing error, not redis.Nil")
}

func (s *GatewayCacheSuite) TestSessionOwnerGroupID_SetNXAndGet() {
	sessionTTL := 1 * time.Minute

	written, err := s.cache.SetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceGateway, "session-owner", 11, sessionTTL)
	require.NoError(s.T(), err)
	require.True(s.T(), written)

	written, err = s.cache.SetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceGateway, "session-owner", 22, sessionTTL)
	require.NoError(s.T(), err)
	require.False(s.T(), written)

	ownerID, err := s.cache.GetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceGateway, "session-owner")
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(11), ownerID)
}

func (s *GatewayCacheSuite) TestSessionOwnerGroupID_TTLAndRefresh() {
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute
	key := buildSessionOwnerKey(7, service.SessionIsolationSourceOpenAI, "session-owner-ttl")

	written, err := s.cache.SetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceOpenAI, "session-owner-ttl", 11, initialTTL)
	require.NoError(s.T(), err)
	require.True(s.T(), written)

	ttl, err := s.rdb.TTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(ttl, 1*time.Second, initialTTL)

	require.NoError(s.T(), s.cache.RefreshSessionOwnerTTL(s.ctx, 7, service.SessionIsolationSourceOpenAI, "session-owner-ttl", refreshTTL))
	ttl, err = s.rdb.TTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestSessionOwnerGroupID_ConcurrentFirstBindAllowsSingleOwner() {
	start := make(chan struct{})
	var wg sync.WaitGroup
	var writtenCount int32
	var winner int64
	errCh := make(chan error, 8)
	for groupID := int64(1); groupID <= 8; groupID++ {
		groupID := groupID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			written, err := s.cache.SetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceGemini, "session-owner-race", groupID, time.Minute)
			if err != nil {
				errCh <- err
				return
			}
			if written {
				atomic.AddInt32(&writtenCount, 1)
				atomic.StoreInt64(&winner, groupID)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(s.T(), err)
	}

	require.Equal(s.T(), int32(1), atomic.LoadInt32(&writtenCount))
	ownerID, err := s.cache.GetSessionOwnerGroupID(s.ctx, 7, service.SessionIsolationSourceGemini, "session-owner-race")
	require.NoError(s.T(), err)
	require.Equal(s.T(), atomic.LoadInt64(&winner), ownerID)
}

func TestGatewayCacheSuite(t *testing.T) {
	suite.Run(t, new(GatewayCacheSuite))
}
