//go:build integration

package rediscache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// End-to-end Redis concurrency scenarios spanning concurrency_cache.go,
// concurrency_slots.go, concurrency_ws_ingress.go, and
// concurrency_live_leases.go. Discovery scores can be due while ordinary
// members are still live, and unindexed primary sets can remain outside
// maintenance queries; these fixtures seed both states alongside slot
// admission, lease renewal, queue counters, and fail-open wait cleanup.

// testSlotTTLMinutes selects the default ordinary-slot lifetime so
// integration admission and expiry assertions use the same configured minute
// window.
const testSlotTTLMinutes = 15

// testSlotTTL supplies fixture expiry and score cutoffs in duration form for
// the selected ordinary-slot lifetime used throughout this suite.
var testSlotTTL = time.Duration(testSlotTTLMinutes) * time.Minute

// ConcurrencyCacheSuite exercises Redis admission, expiry observations and
// ordinary-load discovery maintenance.
type ConcurrencyCacheSuite struct {
	IntegrationRedisSuite // namespaced Redis test fixture
	// cache and rawCache refer to the same adapter rebuilt after SetupTest
	// creates its namespaced client; interface calls exercise admission while
	// concrete helpers inspect maintenance state.
	cache    scheduler.ConcurrencyCache // shared admission interface subject
	rawCache *concurrencyCache          // concrete maintenance test subject
}

// apiKeyConcurrencyCacheForTest exposes API-key statistics operations needed
// by the Redis integration suite.
type apiKeyConcurrencyCacheForTest interface {
	// TrackAPIKeySlot records or refreshes an ordinary request member used for
	// API-key statistics. Callers release the tracked identity when that counted
	// request finishes, and command failures propagate.
	TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	// ReleaseAPIKeySlot removes the ordinary statistics member for a request
	// identity. Missing members are harmless, while Redis failures return to the
	// fixture so cleanup assertions can report them.
	ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	// GetAPIKeyConcurrencyBatch returns combined ordinary/Live statistics for
	// the supplied API-key identities. Empty input produces an empty map, and
	// absent member sets contribute zero counts for requested IDs.
	GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error)
}

// waitIncrementFault reports unconfirmed admission while delegating real wait
// observations and releases.
type waitIncrementFault struct {
	// ConcurrencyCache supplies the real Redis reads and cleanup methods; the
	// fixture overrides user/provider increments so new admission remains
	// unconfirmed.
	scheduler.ConcurrencyCache // delegated Redis concurrency subject
}

// TestConcurrencyCacheSuite runs Redis slot, lease, queue and maintenance
// scenarios through the shared suite. Per-method clients prefix known key
// arguments and register fixture cleanup.
func TestConcurrencyCacheSuite(t *testing.T) {
	suite.Run(t, new(ConcurrencyCacheSuite))
}

// SetupTest creates the namespaced Redis fixture and checks the constructor's
// concrete concurrency adapter. The interface and concrete references share
// that adapter for admission calls and maintenance inspection.
func (s *ConcurrencyCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	cache, ok := NewConcurrencyCache(s.rdb, testSlotTTLMinutes, int(testSlotTTL.Seconds())).(*concurrencyCache)
	s.Require().True(ok, "concurrency cache constructor type")
	s.rawCache = cache
	s.cache = s.rawCache
}

// apiKeyConcurrencyCache retrieves the fixture's API-key statistics methods
// and asserts that the adapter implements them. The returned interface lets
// tests inspect request-member statistics alongside public admission
// operations.
func (s *ConcurrencyCacheSuite) apiKeyConcurrencyCache() apiKeyConcurrencyCacheForTest {
	cache, ok := s.cache.(apiKeyConcurrencyCacheForTest)
	require.True(s.T(), ok)
	return cache
}

// TestOpenAIWSIngressAPIKeySlot_HardLimitRefreshAndRelease acquires one
// ingress connection at capacity one. It checks rejection of another ID,
// successful renewal and new admission immediately after release.
func (s *ConcurrencyCacheSuite) TestOpenAIWSIngressAPIKeySlot_HardLimitRefreshAndRelease() {
	apiKeyID := int64(9011)
	firstLeaseID := "ingress-first"
	secondLeaseID := "ingress-second"

	ok, err := s.rawCache.AcquireOpenAIWSIngressLease(s.ctx, apiKeyID, 1, firstLeaseID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	ok, err = s.rawCache.AcquireOpenAIWSIngressLease(s.ctx, apiKeyID, 1, secondLeaseID)
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "a second live session must not exceed the API key limit")

	ok, err = s.rawCache.RefreshOpenAIWSIngressLease(s.ctx, apiKeyID, firstLeaseID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok, "the current owner must be able to refresh its lease")

	require.NoError(s.T(), s.rawCache.ReleaseOpenAIWSIngressLease(s.ctx, apiKeyID, firstLeaseID))
	ok, err = s.rawCache.AcquireOpenAIWSIngressLease(s.ctx, apiKeyID, 1, secondLeaseID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok, "released capacity must become available immediately")
}

// TestOpenAIWSIngressAPIKeySlot_ReapsCrashedLeaseWithoutDeletingLiveOtherInstance
// seeds one expired and one fresh ingress member. It checks pruning the old
// member while retaining the fresh peer and admitting another connection.
func (s *ConcurrencyCacheSuite) TestOpenAIWSIngressAPIKeySlot_ReapsCrashedLeaseWithoutDeletingLiveOtherInstance() {
	apiKeyID := int64(9012)
	key := openAIWSIngressLeaseKey(apiKeyID)
	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	// Seed both sides of the sixty-second inactivity cutoff so acquisition must
	// prune the old connection while preserving a still-fresh peer.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, key,
		redis.Z{Score: float64(now - openAIWSIngressLeaseTTLSeconds - 1), Member: "crashed-instance"},
		redis.Z{Score: float64(now), Member: "live-other-instance"},
	).Err())
	require.NoError(s.T(), s.rdb.Expire(s.ctx, key, time.Duration(openAIWSIngressLeaseTTLSeconds)*time.Second).Err())

	ok, err := s.rawCache.AcquireOpenAIWSIngressLease(s.ctx, apiKeyID, 2, "new-instance")
	require.NoError(s.T(), err)
	require.True(s.T(), ok, "the crashed member should be reaped before enforcing the limit")

	_, err = s.rdb.ZScore(s.ctx, key, "crashed-instance").Result()
	require.ErrorIs(s.T(), err, redis.Nil)
	_, err = s.rdb.ZScore(s.ctx, key, "live-other-instance").Result()
	require.NoError(s.T(), err, "a live lease owned by another instance must be preserved")
	count, err := s.rdb.ZCard(s.ctx, key).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(2), count)
}

// TestLiveLease_CountsTowardRegularProviderAndUserLimits reserves one Live
// lease at provider/user capacity one. It checks ordinary rejection on both
// identities, successful renewal and provider admission after the Live
// release.
func (s *ConcurrencyCacheSuite) TestLiveLease_CountsTowardRegularProviderAndUserLimits() {
	liveCache, ok := s.cache.(scheduler.LiveConcurrencyCache)
	require.True(s.T(), ok)

	providerID := int64(9101)
	userID := int64(9102)
	apiKeyID := int64(9103)
	acquired, err := liveCache.AcquireLiveLease(
		s.ctx,
		providerID,
		1,
		userID,
		1,
		apiKeyID,
		"live-integration",
		false,
	)
	require.NoError(s.T(), err)
	require.True(s.T(), acquired)

	regularProvider, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 1, "regular-provider")
	require.NoError(s.T(), err)
	require.False(s.T(), regularProvider)
	regularUser, err := s.cache.AcquireUserSlot(s.ctx, userID, 1, "regular-user")
	require.NoError(s.T(), err)
	require.False(s.T(), regularUser)

	refreshed, err := liveCache.RefreshLiveLease(s.ctx, providerID, userID, apiKeyID, "live-integration")
	require.NoError(s.T(), err)
	require.True(s.T(), refreshed)
	require.NoError(s.T(), liveCache.ReleaseLiveLease(s.ctx, providerID, userID, apiKeyID, "live-integration"))
	regularProvider, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 1, "regular-provider")
	require.NoError(s.T(), err)
	require.True(s.T(), regularProvider)
}

// TestProviderSlot_AcquireAndRelease fills two ordinary provider slots and
// attempts a third request. It checks release counts, then combines the
// remaining ordinary member with Live membership and prunes both expiration
// windows through the batch entry.
func (s *ConcurrencyCacheSuite) TestProviderSlot_AcquireAndRelease() {
	providerID := int64(10)
	reqID1, reqID2, reqID3 := "req1", "req2", "req3"

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID1)
	require.NoError(s.T(), err, "AcquireProviderSlot 1")
	require.True(s.T(), ok)

	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID2)
	require.NoError(s.T(), err, "AcquireProviderSlot 2")
	require.True(s.T(), ok)

	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID3)
	require.NoError(s.T(), err, "AcquireProviderSlot 3")
	require.False(s.T(), ok, "expected third acquire to fail")

	cur, err := s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err, "GetProviderConcurrency")
	require.Equal(s.T(), 2, cur, "concurrency mismatch")

	require.NoError(s.T(), s.cache.ReleaseProviderSlot(s.ctx, providerID, reqID1), "ReleaseProviderSlot")

	cur, err = s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err, "GetProviderConcurrency after release")
	require.Equal(s.T(), 1, cur, "expected 1 after release")

	now, err := s.rdb.Time(s.ctx).Result()
	require.NoError(s.T(), err)
	slotKey := providerSlotKey(providerID)
	liveKey := liveProviderSlotKey(providerID)
	// Leave ten seconds beyond each cutoff so later Redis clock samples keep
	// the seeded stale members expired under their respective activity windows.
	expiredOrdinary := float64(now.Unix() - int64(s.rawCache.slotTTLSeconds) - 10)
	expiredLive := float64(now.Unix() - liveLeaseTTLSeconds - 10)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, slotKey,
		redis.Z{Score: expiredOrdinary, Member: "expired-ordinary"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, liveKey,
		redis.Z{Score: expiredLive, Member: "expired-live"},
		redis.Z{Score: float64(now.Unix()), Member: "current-live"},
	).Err())
	// Give the seeded Live set its production lifetime in Redis seconds.
	require.NoError(s.T(), s.rdb.Expire(s.ctx, liveKey, time.Duration(liveLeaseTTLSeconds)*time.Second).Err())

	counts, err := s.cache.GetProviderConcurrencyBatch(s.ctx, []int64{providerID, providerID + 1, providerID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[int64]int{providerID: 2, providerID + 1: 0}, counts)
	ordinaryMembers, err := s.rdb.ZRange(s.ctx, slotKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{reqID2}, ordinaryMembers)
	liveMembers, err := s.rdb.ZRange(s.ctx, liveKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"current-live"}, liveMembers)

	// Age the surviving memberships by score so the batch must prune each set
	// while the fixture keys still exist under their storage TTLs.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, slotKey,
		redis.Z{Score: expiredOrdinary, Member: reqID2},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, liveKey,
		redis.Z{Score: expiredLive, Member: "current-live"},
	).Err())
	counts, err = s.cache.GetProviderConcurrencyBatch(s.ctx, []int64{providerID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[int64]int{providerID: 0}, counts)
}

// TestProviderActiveIndex_AcquireAndRelease acquires one ordinary provider
// request before releasing it. It checks a future discovery score and removal
// of that index member when observed load is empty.
func (s *ConcurrencyCacheSuite) TestProviderActiveIndex_AcquireAndRelease() {
	providerID := int64(610)
	member := strconv.FormatInt(providerID, 10)
	reqID := "active-index-req"

	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	score, err := s.rdb.ZScore(s.ctx, providerActiveIndexKey, member).Result()
	require.NoError(s.T(), err)
	require.Greater(s.T(), int64(score), now, "index score should be a future expiry")

	require.NoError(s.T(), s.cache.ReleaseProviderSlot(s.ctx, providerID, reqID))

	_, err = s.rdb.ZScore(s.ctx, providerActiveIndexKey, member).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "index member should be removed after load drops to zero")
}

// TestProviderActiveIndex_WaitLifecycle records one provider waiter without
// occupied request slots. It checks discovery of queue-only load and removal
// of its index member after decrement.
func (s *ConcurrencyCacheSuite) TestProviderActiveIndex_WaitLifecycle() {
	providerID := int64(611)
	member := strconv.FormatInt(providerID, 10)

	ok, err := s.cache.IncrementProviderWaitCount(s.ctx, providerID, 2)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	_, err = s.rdb.ZScore(s.ctx, providerActiveIndexKey, member).Result()
	require.NoError(s.T(), err, "wait increment should register index member")

	require.NoError(s.T(), s.cache.DecrementProviderWaitCount(s.ctx, providerID))

	_, err = s.rdb.ZScore(s.ctx, providerActiveIndexKey, member).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "index member should be removed after wait drops to zero")
}

// TestUserActiveIndex_AcquireAndRelease acquires and releases one ordinary
// user member. It checks that user discovery is present while loaded and
// absent after its request member is removed.
func (s *ConcurrencyCacheSuite) TestUserActiveIndex_AcquireAndRelease() {
	userID := int64(612)
	member := strconv.FormatInt(userID, 10)
	reqID := "user-active-index-req"

	ok, err := s.cache.AcquireUserSlot(s.ctx, userID, 2, reqID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	_, err = s.rdb.ZScore(s.ctx, userActiveIndexKey, member).Result()
	require.NoError(s.T(), err, "acquire should register user index member")

	require.NoError(s.T(), s.cache.ReleaseUserSlot(s.ctx, userID, reqID))

	_, err = s.rdb.ZScore(s.ctx, userActiveIndexKey, member).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "user index member should be removed after release")
}

// TestProviderSlot_TTL acquires an ordinary member using the suite's selected
// provider lifetime. It checks that the set has a positive Redis TTL bounded
// by that configured duration.
func (s *ConcurrencyCacheSuite) TestProviderSlot_TTL() {
	providerID := int64(11)
	reqID := "req_ttl_test"
	slotKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 5, reqID)
	require.NoError(s.T(), err, "AcquireProviderSlot")
	require.True(s.T(), ok)

	ttl, err := s.rdb.TTL(s.ctx, slotKey).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, 1*time.Second, testSlotTTL)
}

// TestProviderSlot_DuplicateReqID acquires the same ordinary provider request
// identity twice. It checks both admissions succeed while the counted
// provider membership remains one rather than two.
func (s *ConcurrencyCacheSuite) TestProviderSlot_DuplicateReqID() {
	providerID := int64(12)
	reqID := "dup-req"

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 2, reqID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	cur, err := s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, cur, "expected concurrency=1 (idempotent)")
}

// TestProviderSlot_ReleaseIdempotent releases an acquired request, repeats
// that removal and removes an unrelated absent ID. It checks successful
// cleanup calls and a final zero concurrency count.
func (s *ConcurrencyCacheSuite) TestProviderSlot_ReleaseIdempotent() {
	providerID := int64(13)
	reqID := "release-test"

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 1, reqID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	require.NoError(s.T(), s.cache.ReleaseProviderSlot(s.ctx, providerID, reqID), "ReleaseProviderSlot")
	require.NoError(s.T(), s.cache.ReleaseProviderSlot(s.ctx, providerID, reqID), "ReleaseProviderSlot again")
	require.NoError(s.T(), s.cache.ReleaseProviderSlot(s.ctx, providerID, "non-existent"), "ReleaseProviderSlot non-existent")

	cur, err := s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, cur)
}

// TestProviderSlot_MaxZero attempts a new ordinary provider reservation with
// a zero capacity. It checks that the request is rejected successfully
// through the Redis adapter.
func (s *ConcurrencyCacheSuite) TestProviderSlot_MaxZero() {
	providerID := int64(14)
	reqID := "max-zero-test"

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 0, reqID)
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "expected acquire to fail with max=0")
}

// TestUserSlot_AcquireAndRelease fills one user's ordinary capacity and
// attempts another request. It checks rejection, observed occupancy and
// successful cleanup of present and absent identities before a zero count.
func (s *ConcurrencyCacheSuite) TestUserSlot_AcquireAndRelease() {
	userID := int64(42)
	reqID1, reqID2 := "req1", "req2"

	ok, err := s.cache.AcquireUserSlot(s.ctx, userID, 1, reqID1)
	require.NoError(s.T(), err, "AcquireUserSlot")
	require.True(s.T(), ok)

	ok, err = s.cache.AcquireUserSlot(s.ctx, userID, 1, reqID2)
	require.NoError(s.T(), err, "AcquireUserSlot 2")
	require.False(s.T(), ok, "expected second acquire to fail at max=1")

	cur, err := s.cache.GetUserConcurrency(s.ctx, userID)
	require.NoError(s.T(), err, "GetUserConcurrency")
	require.Equal(s.T(), 1, cur, "expected concurrency=1")

	require.NoError(s.T(), s.cache.ReleaseUserSlot(s.ctx, userID, reqID1), "ReleaseUserSlot")
	require.NoError(s.T(), s.cache.ReleaseUserSlot(s.ctx, userID, "non-existent"), "ReleaseUserSlot non-existent")

	cur, err = s.cache.GetUserConcurrency(s.ctx, userID)
	require.NoError(s.T(), err, "GetUserConcurrency after release")
	require.Equal(s.T(), 0, cur, "expected concurrency=0 after release")
}

// TestUserSlot_TTL acquires an ordinary user member with the suite's selected
// lifetime. It checks a positive Redis set TTL bounded by the configured slot
// duration.
func (s *ConcurrencyCacheSuite) TestUserSlot_TTL() {
	userID := int64(200)
	reqID := "req_ttl_test"
	slotKey := fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)

	ok, err := s.cache.AcquireUserSlot(s.ctx, userID, 5, reqID)
	require.NoError(s.T(), err, "AcquireUserSlot")
	require.True(s.T(), ok)

	ttl, err := s.rdb.TTL(s.ctx, slotKey).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, 1*time.Second, testSlotTTL)
}

// TestAPIKeySlot_TrackReleaseAndBatchCount tracks two request identities
// beside an empty API-key set. It checks TTL and release counts, then combines
// ordinary and Live membership while pruning their distinct expiration
// windows.
func (s *ConcurrencyCacheSuite) TestAPIKeySlot_TrackReleaseAndBatchCount() {
	cache := s.apiKeyConcurrencyCache()
	apiKeyID := int64(300)
	emptyAPIKeyID := int64(301)
	slotKey := fmt.Sprintf("%s%d", apiKeySlotKeyPrefix, apiKeyID)

	require.NoError(s.T(), cache.TrackAPIKeySlot(s.ctx, apiKeyID, "req1"))
	require.NoError(s.T(), cache.TrackAPIKeySlot(s.ctx, apiKeyID, "req2"))

	counts, err := cache.GetAPIKeyConcurrencyBatch(s.ctx, []int64{apiKeyID, emptyAPIKeyID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[int64]int{apiKeyID: 2, emptyAPIKeyID: 0}, counts)

	ttl, err := s.rdb.TTL(s.ctx, slotKey).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, 1*time.Second, testSlotTTL)

	require.NoError(s.T(), cache.ReleaseAPIKeySlot(s.ctx, apiKeyID, "req1"))
	counts, err = cache.GetAPIKeyConcurrencyBatch(s.ctx, []int64{apiKeyID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, counts[apiKeyID])

	require.NoError(s.T(), cache.ReleaseAPIKeySlot(s.ctx, apiKeyID, "req2"))
	counts, err = cache.GetAPIKeyConcurrencyBatch(s.ctx, []int64{apiKeyID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, counts[apiKeyID])

	now, err := s.rdb.Time(s.ctx).Result()
	require.NoError(s.T(), err)
	liveKey := liveAPIKeySlotKey(apiKeyID)
	// Leave ten seconds beyond each cutoff so subsequent Redis clock samples
	// classify both stale fixture members using the ordinary and Live windows.
	expiredOrdinary := float64(now.Unix() - int64(s.rawCache.slotTTLSeconds) - 10)
	expiredLive := float64(now.Unix() - liveLeaseTTLSeconds - 10)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, slotKey,
		redis.Z{Score: expiredOrdinary, Member: "expired-ordinary"},
		redis.Z{Score: float64(now.Unix()), Member: "current-ordinary"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, liveKey,
		redis.Z{Score: expiredLive, Member: "expired-live"},
		redis.Z{Score: float64(now.Unix()), Member: "current-live"},
	).Err())
	require.NoError(s.T(), s.rdb.Expire(s.ctx, slotKey, testSlotTTL).Err())
	// Give the seeded Live set its production lifetime in Redis seconds.
	require.NoError(s.T(), s.rdb.Expire(s.ctx, liveKey, time.Duration(liveLeaseTTLSeconds)*time.Second).Err())

	counts, err = cache.GetAPIKeyConcurrencyBatch(s.ctx, []int64{apiKeyID, emptyAPIKeyID, apiKeyID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[int64]int{apiKeyID: 2, emptyAPIKeyID: 0}, counts)
	ordinaryMembers, err := s.rdb.ZRange(s.ctx, slotKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"current-ordinary"}, ordinaryMembers)
	liveMembers, err := s.rdb.ZRange(s.ctx, liveKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"current-live"}, liveMembers)

	// Age the surviving memberships by score so both expiration paths execute
	// before storage TTL expiry could remove either fixture key instead.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, slotKey,
		redis.Z{Score: expiredOrdinary, Member: "current-ordinary"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, liveKey,
		redis.Z{Score: expiredLive, Member: "current-live"},
	).Err())
	counts, err = cache.GetAPIKeyConcurrencyBatch(s.ctx, []int64{apiKeyID})
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[int64]int{apiKeyID: 0}, counts)
}

// TestWaitQueue_IncrementAndDecrement fills a two-waiter user queue and
// attempts one more increment. It checks capacity rejection, bounded expiry
// and a count of one after cleanup.
func (s *ConcurrencyCacheSuite) TestWaitQueue_IncrementAndDecrement() {
	userID := int64(20)
	waitKey := fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)

	ok, err := s.cache.IncrementWaitCount(s.ctx, userID, 2)
	require.NoError(s.T(), err, "IncrementWaitCount 1")
	require.True(s.T(), ok)

	ok, err = s.cache.IncrementWaitCount(s.ctx, userID, 2)
	require.NoError(s.T(), err, "IncrementWaitCount 2")
	require.True(s.T(), ok)

	ok, err = s.cache.IncrementWaitCount(s.ctx, userID, 2)
	require.NoError(s.T(), err, "IncrementWaitCount 3")
	require.False(s.T(), ok, "expected wait increment over max to fail")

	ttl, err := s.rdb.TTL(s.ctx, waitKey).Result()
	require.NoError(s.T(), err, "TTL waitKey")
	s.AssertTTLWithin(ttl, 1*time.Second, testSlotTTL)

	require.NoError(s.T(), s.cache.DecrementWaitCount(s.ctx, userID), "DecrementWaitCount")

	val, err := s.rdb.Get(s.ctx, waitKey).Int()
	if !errors.Is(err, redis.Nil) {
		require.NoError(s.T(), err, "Get waitKey")
	}
	require.Equal(s.T(), 1, val, "expected wait count 1")
}

// TestWaitQueue_DecrementNoNegative decrements a missing user counter, then
// decrements a confirmed one twice. It checks nonnegative observations for
// the absent and zero-counter cleanup cases.
func (s *ConcurrencyCacheSuite) TestWaitQueue_DecrementNoNegative() {
	userID := int64(300)
	waitKey := fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)

	// Exercise missing and zero-counter cleanup separately from request
	// ownership, checking the adapter's nonnegative floor before and after a
	// confirmed increment.
	require.NoError(s.T(), s.cache.DecrementWaitCount(s.ctx, userID), "DecrementWaitCount on non-existent key")

	val, err := s.rdb.Get(s.ctx, waitKey).Int()
	if !errors.Is(err, redis.Nil) {
		require.NoError(s.T(), err, "Get waitKey")
	}
	require.GreaterOrEqual(s.T(), val, 0, "expected non-negative wait count after decrement on empty")

	ok, err := s.cache.IncrementWaitCount(s.ctx, userID, 5)
	require.NoError(s.T(), err, "IncrementWaitCount")
	require.True(s.T(), ok)

	require.NoError(s.T(), s.cache.DecrementWaitCount(s.ctx, userID), "DecrementWaitCount")

	require.NoError(s.T(), s.cache.DecrementWaitCount(s.ctx, userID), "DecrementWaitCount on zero")

	val, err = s.rdb.Get(s.ctx, waitKey).Int()
	if !errors.Is(err, redis.Nil) {
		require.NoError(s.T(), err, "Get waitKey after double decrement")
	}
	require.GreaterOrEqual(s.T(), val, 0, "expected non-negative wait count")
}

// TestProviderWaitQueue_IncrementAndDecrement fills a two-waiter provider
// queue before a third increment. It checks rejection, bounded expiry and a
// remaining count of one after decrement.
func (s *ConcurrencyCacheSuite) TestProviderWaitQueue_IncrementAndDecrement() {
	providerID := int64(30)
	waitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, providerID)

	ok, err := s.cache.IncrementProviderWaitCount(s.ctx, providerID, 2)
	require.NoError(s.T(), err, "IncrementProviderWaitCount 1")
	require.True(s.T(), ok)

	ok, err = s.cache.IncrementProviderWaitCount(s.ctx, providerID, 2)
	require.NoError(s.T(), err, "IncrementProviderWaitCount 2")
	require.True(s.T(), ok)

	ok, err = s.cache.IncrementProviderWaitCount(s.ctx, providerID, 2)
	require.NoError(s.T(), err, "IncrementProviderWaitCount 3")
	require.False(s.T(), ok, "expected provider wait increment over max to fail")

	ttl, err := s.rdb.TTL(s.ctx, waitKey).Result()
	require.NoError(s.T(), err, "TTL provider waitKey")
	s.AssertTTLWithin(ttl, 1*time.Second, testSlotTTL)

	require.NoError(s.T(), s.cache.DecrementProviderWaitCount(s.ctx, providerID), "DecrementProviderWaitCount")

	val, err := s.rdb.Get(s.ctx, waitKey).Int()
	if !errors.Is(err, redis.Nil) {
		require.NoError(s.T(), err, "Get waitKey")
	}
	require.Equal(s.T(), 1, val, "expected provider wait count 1")
}

// TestCleanupStaleProcessSlots seeds indexed provider/user members with old
// and retained prefixes while the sweep marker is present. It checks prefix
// cleanup and wait reset while API-key statistics and unindexed provider data
// remain.
func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots() {
	// Seed the completed-sweep marker so this scenario exercises indexed prefix
	// cleanup while unindexed wait counters remain available for the scope
	// assertions.
	require.NoError(s.T(), s.rdb.Set(s.ctx, legacyWaitSweepMarkerKey, "1", 0).Err())
	providerID := int64(901)
	userID := int64(902)
	apiKeyID := int64(903)
	unindexedProviderID := int64(1901)
	providerKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)
	userKey := fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)
	apiKeyKey := fmt.Sprintf("%s%d", apiKeySlotKeyPrefix, apiKeyID)
	unindexedProviderKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, unindexedProviderID)
	userWaitKey := fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)
	providerWaitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, providerID)
	unindexedProviderWaitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, unindexedProviderID)

	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerKey,
		redis.Z{Score: float64(now), Member: "oldproc-1"},
		redis.Z{Score: float64(now), Member: "keep-1"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userKey,
		redis.Z{Score: float64(now), Member: "oldproc-2"},
		redis.Z{Score: float64(now), Member: "keep-2"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, unindexedProviderKey,
		redis.Z{Score: float64(now), Member: "oldproc-unindexed"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, apiKeyKey,
		redis.Z{Score: float64(now), Member: "oldproc-3"},
		redis.Z{Score: float64(now), Member: "keep-3"},
	).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, userWaitKey, 3, time.Minute).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, providerWaitKey, 2, time.Minute).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, unindexedProviderWaitKey, 2, time.Minute).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerActiveIndexKey, redis.Z{
		Score:  float64(now + 60),
		Member: strconv.FormatInt(providerID, 10),
	}).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userActiveIndexKey, redis.Z{
		Score:  float64(now + 60),
		Member: strconv.FormatInt(userID, 10),
	}).Err())

	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "keep-"))

	providerMembers, err := s.rdb.ZRange(s.ctx, providerKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"keep-1"}, providerMembers)

	userMembers, err := s.rdb.ZRange(s.ctx, userKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"keep-2"}, userMembers)

	// Startup's indexed ordinary cleanup leaves API-key statistics alone, so
	// both old and retained fixture prefixes remain present for the assertion.
	apiKeyMembers, err := s.rdb.ZRange(s.ctx, apiKeyKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), []string{"keep-3", "oldproc-3"}, apiKeyMembers)

	_, err = s.rdb.Get(s.ctx, userWaitKey).Result()
	require.True(s.T(), errors.Is(err, redis.Nil))

	_, err = s.rdb.Get(s.ctx, providerWaitKey).Result()
	require.True(s.T(), errors.Is(err, redis.Nil))

	unindexedMembers, err := s.rdb.ZRange(s.ctx, unindexedProviderKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"oldproc-unindexed"}, unindexedMembers)
	_, err = s.rdb.Get(s.ctx, unindexedProviderWaitKey).Result()
	require.NoError(s.T(), err)
}

// TestGetProviderConcurrency_Missing queries a provider with no stored slot
// members in its fixture namespace. It checks a successful zero observation
// through the Redis concurrency adapter.
func (s *ConcurrencyCacheSuite) TestGetProviderConcurrency_Missing() {
	cur, err := s.cache.GetProviderConcurrency(s.ctx, 999)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, cur)
}

// TestGetUserConcurrency_Missing queries a user with no stored slot members
// in its fixture namespace. It checks a successful zero observation through
// the Redis concurrency adapter.
func (s *ConcurrencyCacheSuite) TestGetUserConcurrency_Missing() {
	cur, err := s.cache.GetUserConcurrency(s.ctx, 999)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, cur)
}

// TestGetProvidersLoadBatch seeds full queue-adjusted, partial and idle
// provider load at different capacities. It checks identities, slot/wait
// counts and integer percentages of one hundred, fifty and zero.
func (s *ConcurrencyCacheSuite) TestGetProvidersLoadBatch() {
	provider1 := int64(100)
	provider2 := int64(101)
	provider3 := int64(102)

	ok, err := s.cache.AcquireProviderSlot(s.ctx, provider1, 3, "req1")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = s.cache.AcquireProviderSlot(s.ctx, provider1, 3, "req2")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = s.cache.IncrementProviderWaitCount(s.ctx, provider1, 5)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	ok, err = s.cache.AcquireProviderSlot(s.ctx, provider2, 2, "req3")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	providers := []scheduler.ProviderWithConcurrency{
		{ID: provider1, MaxConcurrency: 3},
		{ID: provider2, MaxConcurrency: 2},
		{ID: provider3, MaxConcurrency: 1},
	}

	loadMap, err := s.cache.GetProvidersLoadBatch(s.ctx, providers)
	require.NoError(s.T(), err)
	require.Len(s.T(), loadMap, 3)

	// Two occupied slots plus one waiter reach three units of pressure against
	// the first provider's capacity of three.
	load1 := loadMap[provider1]
	require.NotNil(s.T(), load1)
	require.Equal(s.T(), provider1, load1.ProviderID)
	require.Equal(s.T(), 2, load1.CurrentConcurrency)
	require.Equal(s.T(), 1, load1.WaitingCount)
	require.Equal(s.T(), 100, load1.LoadRate)

	// One occupied slot and no queue represent half of the second provider's
	// capacity, so its load remains fifty percent.
	load2 := loadMap[provider2]
	require.NotNil(s.T(), load2)
	require.Equal(s.T(), provider2, load2.ProviderID)
	require.Equal(s.T(), 1, load2.CurrentConcurrency)
	require.Equal(s.T(), 0, load2.WaitingCount)
	require.Equal(s.T(), 50, load2.LoadRate)

	// The untouched provider still receives a zero-valued load entry, keeping
	// the returned observations complete for every requested provider identity.
	load3 := loadMap[provider3]
	require.NotNil(s.T(), load3)
	require.Equal(s.T(), provider3, load3.ProviderID)
	require.Equal(s.T(), 0, load3.CurrentConcurrency)
	require.Equal(s.T(), 0, load3.WaitingCount)
	require.Equal(s.T(), 0, load3.LoadRate)
}

// TestGetProvidersLoadBatch_Empty supplies an empty provider input list to
// the load reader. It checks successful empty output so callers can omit a
// batch when no candidates exist.
func (s *ConcurrencyCacheSuite) TestGetProvidersLoadBatch_Empty() {
	loadMap, err := s.cache.GetProvidersLoadBatch(s.ctx, []scheduler.ProviderWithConcurrency{})
	require.NoError(s.T(), err)
	require.Empty(s.T(), loadMap)
}

// TestCleanupExpiredProviderSlots moves two of three request scores past the
// ordinary lifetime. It checks cleanup retains the fresh third member and
// reports only its remaining concurrency.
func (s *ConcurrencyCacheSuite) TestCleanupExpiredProviderSlots() {
	providerID := int64(200)
	slotKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 5, "req1")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 5, "req2")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 5, "req3")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	cur, err := s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, cur)

	now := time.Now().Unix()
	// Move two scores ten seconds beyond the selected ordinary lifetime while
	// leaving the third request fresh, exposing score-based pruning rather than
	// whole-key expiry.
	expiredTime := now - int64(testSlotTTL.Seconds()) - 10
	err = s.rdb.ZAdd(s.ctx, slotKey, redis.Z{Score: float64(expiredTime), Member: "req1"}).Err()
	require.NoError(s.T(), err)
	err = s.rdb.ZAdd(s.ctx, slotKey, redis.Z{Score: float64(expiredTime), Member: "req2"}).Err()
	require.NoError(s.T(), err)

	err = s.cache.CleanupExpiredProviderSlots(s.ctx, providerID)
	require.NoError(s.T(), err)

	cur, err = s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, cur)

	members, err := s.rdb.ZRange(s.ctx, slotKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Len(s.T(), members, 1)
	require.Equal(s.T(), "req3", members[0])
}

// TestCleanupExpiredProviderSlots_NoExpired acquires two fresh ordinary
// request members before cleanup. It checks both remain counted when neither
// member's score is past the configured lifetime.
func (s *ConcurrencyCacheSuite) TestCleanupExpiredProviderSlots_NoExpired() {
	providerID := int64(201)

	ok, err := s.cache.AcquireProviderSlot(s.ctx, providerID, 5, "req1")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = s.cache.AcquireProviderSlot(s.ctx, providerID, 5, "req2")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	err = s.cache.CleanupExpiredProviderSlots(s.ctx, providerID)
	require.NoError(s.T(), err)

	cur, err := s.cache.GetProviderConcurrency(s.ctx, providerID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 2, cur)
}

// TestCleanupExpiredProviderSlotKeys seeds due indexed providers beside
// unindexed expired provider/user sets. It checks indexed pruning,
// empty-member removal and loaded-score refresh while both unindexed sets
// stay untouched.
func (s *ConcurrencyCacheSuite) TestCleanupExpiredProviderSlotKeys() {
	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	// Seed scores beyond the ordinary lifetime for indexed and unindexed sets so
	// the assertions can distinguish score pruning from discovery scope.
	expiredTime := now - int64(testSlotTTL.Seconds()) - 10
	providerKeyWithFresh := fmt.Sprintf("%s%d", providerSlotKeyPrefix, 301)
	providerKeyExpiredOnly := fmt.Sprintf("%s%d", providerSlotKeyPrefix, 302)
	userKey := fmt.Sprintf("%s%d", userSlotKeyPrefix, 303)
	unindexedProviderKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, 304)

	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerKeyWithFresh,
		redis.Z{Score: float64(expiredTime), Member: "expired"},
		redis.Z{Score: float64(now), Member: "fresh"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerKeyExpiredOnly,
		redis.Z{Score: float64(expiredTime), Member: "expired-only"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userKey,
		redis.Z{Score: float64(expiredTime), Member: "user-expired"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, unindexedProviderKey,
		redis.Z{Score: float64(expiredTime), Member: "unindexed-expired"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerActiveIndexKey,
		redis.Z{Score: float64(now), Member: "301"},
		redis.Z{Score: float64(now), Member: "302"},
	).Err())

	require.NoError(s.T(), s.cache.CleanupExpiredProviderSlotKeys(s.ctx))

	providerMembers, err := s.rdb.ZRange(s.ctx, providerKeyWithFresh, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"fresh"}, providerMembers)

	exists, err := s.rdb.Exists(s.ctx, providerKeyExpiredOnly).Result()
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 0, exists)

	userMembers, err := s.rdb.ZRange(s.ctx, userKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"user-expired"}, userMembers)

	unindexedMembers, err := s.rdb.ZRange(s.ctx, unindexedProviderKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"unindexed-expired"}, unindexedMembers)

	score, err := s.rdb.ZScore(s.ctx, providerActiveIndexKey, "301").Result()
	require.NoError(s.T(), err)
	require.Greater(s.T(), int64(score), now)
	_, err = s.rdb.ZScore(s.ctx, providerActiveIndexKey, "302").Result()
	require.ErrorIs(s.T(), err, redis.Nil)
}

// TestCleanupExpiredProviderSlotKeys_ReapsUserIndex seeds a loaded user, an
// idle user and a malformed member with due scores. It checks loaded-score
// renewal and removal of idle/malformed discovery entries.
func (s *ConcurrencyCacheSuite) TestCleanupExpiredProviderSlotKeys_ReapsUserIndex() {
	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	// Make discovery scores already due while the retained user's member score
	// is fresh, so reconciliation must recheck primary load before removing an
	// entry.
	expiredScore := float64(now - 10)
	userKeyWithFresh := fmt.Sprintf("%s%d", userSlotKeyPrefix, 401)

	// User 401 has an expired index score but still has load, so cleanup must refresh its score.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userKeyWithFresh,
		redis.Z{Score: float64(now), Member: "fresh"},
	).Err())
	// User 402 has no load, so cleanup removes its expired index entry.
	// Malformed members are removed with expired candidates.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userActiveIndexKey,
		redis.Z{Score: expiredScore, Member: "401"},
		redis.Z{Score: expiredScore, Member: "402"},
		redis.Z{Score: expiredScore, Member: "not-a-user-id"},
	).Err())

	require.NoError(s.T(), s.cache.CleanupExpiredProviderSlotKeys(s.ctx))

	score, err := s.rdb.ZScore(s.ctx, userActiveIndexKey, "401").Result()
	require.NoError(s.T(), err)
	require.Greater(s.T(), int64(score), now, "loaded user should be re-scheduled, not dropped")

	_, err = s.rdb.ZScore(s.ctx, userActiveIndexKey, "402").Result()
	require.ErrorIs(s.T(), err, redis.Nil, "idle expired user member should be reaped")

	_, err = s.rdb.ZScore(s.ctx, userActiveIndexKey, "not-a-user-id").Result()
	require.ErrorIs(s.T(), err, redis.Nil, "invalid member should be reaped")
}

// TestCleanupStaleProcessSlots_LegacyWaitSweepRunsOnce seeds unindexed wait
// counters before the completion marker exists. It checks first-startup
// deletion, a stored marker and retention of a newly seeded unindexed counter
// on the second call.
func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots_LegacyWaitSweepRunsOnce() {
	unindexedProviderWaitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, 2901)
	unindexedUserWaitKey := fmt.Sprintf("%s%d", waitQueueKeyPrefix, 2902)
	require.NoError(s.T(), s.rdb.Set(s.ctx, unindexedProviderWaitKey, 5, time.Minute).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, unindexedUserWaitKey, 3, time.Minute).Err())

	// Run startup with no completion marker so the wait-prefix scan must
	// discover and remove both unindexed counters seeded by this fixture.
	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "keep-"))

	_, err := s.rdb.Get(s.ctx, unindexedProviderWaitKey).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "legacy provider wait key should be swept on first startup")
	_, err = s.rdb.Get(s.ctx, unindexedUserWaitKey).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "legacy user wait key should be swept on first startup")

	exists, err := s.rdb.Exists(s.ctx, legacyWaitSweepMarkerKey).Result()
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 1, exists, "sweep marker should be set after first run")

	// Seed new unindexed activity after the successful sweep marker is written;
	// the second startup call must leave that newly recorded counter intact.
	require.NoError(s.T(), s.rdb.Set(s.ctx, unindexedProviderWaitKey, 5, time.Minute).Err())
	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "keep-"))
	val, err := s.rdb.Get(s.ctx, unindexedProviderWaitKey).Int()
	require.NoError(s.T(), err, "sweep must not run twice")
	require.Equal(s.T(), 5, val)
}

// TestCleanupStaleProcessSlots_ProcessesExpiredIndexMembers seeds old-prefix
// members behind already due provider/user discovery scores. It checks
// startup still reaches those entries, removes their slot keys and clears the
// associated indexes and provider wait.
func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots_ProcessesExpiredIndexMembers() {
	// Seed the completed-sweep marker before due index entries so startup must
	// find old-prefix ordinary members through its full indexed cleanup path.
	require.NoError(s.T(), s.rdb.Set(s.ctx, legacyWaitSweepMarkerKey, "1", 0).Err())
	providerID := int64(3901)
	userID := int64(3902)
	providerKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)
	userKey := fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)
	providerWaitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, providerID)

	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerKey,
		redis.Z{Score: float64(now), Member: "oldproc-1"},
	).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userKey,
		redis.Z{Score: float64(now), Member: "oldproc-2"},
	).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, providerWaitKey, 4, time.Minute).Err())
	// Give the indexed old-prefix sets past discovery scores, requiring
	// startup's full index read to reach members a future-only query would skip.
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerActiveIndexKey, redis.Z{
		Score:  float64(now - 100),
		Member: strconv.FormatInt(providerID, 10),
	}).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userActiveIndexKey, redis.Z{
		Score:  float64(now - 100),
		Member: strconv.FormatInt(userID, 10),
	}).Err())

	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "keep-"))

	exists, err := s.rdb.Exists(s.ctx, providerKey).Result()
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 0, exists, "stale slot key of expired index member should be purged")

	exists, err = s.rdb.Exists(s.ctx, userKey).Result()
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 0, exists)

	_, err = s.rdb.Get(s.ctx, providerWaitKey).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "wait counter of expired index member should be deleted")

	_, err = s.rdb.ZScore(s.ctx, providerActiveIndexKey, strconv.FormatInt(providerID, 10)).Result()
	require.ErrorIs(s.T(), err, redis.Nil, "emptied member should be removed from index")
	_, err = s.rdb.ZScore(s.ctx, userActiveIndexKey, strconv.FormatInt(userID, 10)).Result()
	require.ErrorIs(s.T(), err, redis.Nil)
}

// TestCleanupStaleProcessSlots_RemovesOldPrefixesAndWaitCounters seeds
// indexed old and active process members with the sweep marker present. It
// checks only active-prefix members remain and both indexed wait counters are
// removed.
func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots_RemovesOldPrefixesAndWaitCounters() {
	// Seed the completed-sweep marker to select indexed cleanup, then retain
	// active-prefix ordinary members while checking that their wait counters are
	// reset.
	require.NoError(s.T(), s.rdb.Set(s.ctx, legacyWaitSweepMarkerKey, "1", 0).Err())
	providerID := int64(901)
	userID := int64(902)
	providerSlotKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)
	userSlotKey := fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)
	userWaitKey := fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)
	providerWaitKey := fmt.Sprintf("%s%d", providerWaitKeyPrefix, providerID)

	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerSlotKey,
		redis.Z{Score: float64(now), Member: "oldproc-1"},
		redis.Z{Score: float64(now), Member: "activeproc-1"},
	).Err())
	require.NoError(s.T(), s.rdb.Expire(s.ctx, providerSlotKey, testSlotTTL).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userSlotKey,
		redis.Z{Score: float64(now), Member: "oldproc-2"},
		redis.Z{Score: float64(now), Member: "activeproc-2"},
	).Err())
	require.NoError(s.T(), s.rdb.Expire(s.ctx, userSlotKey, testSlotTTL).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, userWaitKey, 3, testSlotTTL).Err())
	require.NoError(s.T(), s.rdb.Set(s.ctx, providerWaitKey, 2, testSlotTTL).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerActiveIndexKey, redis.Z{
		Score:  float64(now + 60),
		Member: strconv.FormatInt(providerID, 10),
	}).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, userActiveIndexKey, redis.Z{
		Score:  float64(now + 60),
		Member: strconv.FormatInt(userID, 10),
	}).Err())

	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "activeproc-"))

	providerMembers, err := s.rdb.ZRange(s.ctx, providerSlotKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"activeproc-1"}, providerMembers)

	userMembers, err := s.rdb.ZRange(s.ctx, userSlotKey, 0, -1).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"activeproc-2"}, userMembers)

	_, err = s.rdb.Get(s.ctx, userWaitKey).Result()
	require.ErrorIs(s.T(), err, redis.Nil)
	_, err = s.rdb.Get(s.ctx, providerWaitKey).Result()
	require.ErrorIs(s.T(), err, redis.Nil)
}

// TestCleanupStaleProcessSlots_DeletesEmptySlotKeys seeds one indexed
// ordinary provider member outside the active prefix. It checks that startup
// deletes its primary set after removing the last member.
func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots_DeletesEmptySlotKeys() {
	providerID := int64(903)
	providerSlotKey := fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)
	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerSlotKey, redis.Z{Score: float64(now), Member: "oldproc-1"}).Err())
	require.NoError(s.T(), s.rdb.Expire(s.ctx, providerSlotKey, testSlotTTL).Err())
	require.NoError(s.T(), s.rdb.ZAdd(s.ctx, providerActiveIndexKey, redis.Z{
		Score:  float64(now + 60),
		Member: strconv.FormatInt(providerID, 10),
	}).Err())

	require.NoError(s.T(), s.cache.CleanupStaleProcessSlots(s.ctx, "activeproc-"))

	exists, err := s.rdb.Exists(s.ctx, providerSlotKey).Result()
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 0, exists)
}

// IncrementWaitCount returns an admission error without incrementing the
// embedded Redis counter. The service treats that new admission as
// unconfirmed, leaving existing real wait counts owned by their earlier
// requests.
func (c waitIncrementFault) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return false, errors.New("增加等待计数未确认")
}

// IncrementProviderWaitCount returns an admission error without incrementing
// the embedded provider counter. The service treats that admission as
// unconfirmed, leaving existing Redis wait counts available for ownership
// assertions.
func (c waitIncrementFault) IncrementProviderWaitCount(context.Context, int64, int) (bool, error) {
	return false, errors.New("增加提供商等待计数未确认")
}

// TestWaitFailOpenDoesNotReleaseOtherRequest seeds real user/provider wait
// counts before injecting unconfirmed increment failures. It checks allowed
// service results and retention of the existing counts after repeated release
// calls.
func TestWaitFailOpenDoesNotReleaseOtherRequest(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900)
	userAllowed, err := cache.IncrementWaitCount(ctx, 91001, 20)
	require.NoError(t, err)
	require.True(t, userAllowed)
	providerAllowed, err := cache.IncrementProviderWaitCount(ctx, 91002, 20)
	require.NoError(t, err)
	require.True(t, providerAllowed)
	// Inject errors only into new admission while retaining real Redis reads and
	// releases, making another request's counters visible if uncertain ownership
	// mistakenly decrements them.
	concurrency := scheduler.NewConcurrencyService(waitIncrementFault{cache}, scheduler.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
	user, err := concurrency.EnterUserWait(ctx, 91001, 20)
	require.NoError(t, err)
	require.True(t, user.Allowed)
	provider, err := concurrency.EnterProviderWait(ctx, 91002, 20)
	require.NoError(t, err)
	require.True(t, provider.Allowed)
	user.Release()
	provider.Release()
	user.Release()
	provider.Release()
	count, err := rdb.Get(ctx, "concurrency:wait:91001").Int()
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = cache.GetProviderWaitingCount(ctx, 91002)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
