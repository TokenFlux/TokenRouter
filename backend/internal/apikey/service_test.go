package apikey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// authRepoStub delegates configured direct authentication and invalidation
// enumeration while rejecting unsupported calls.
type authRepoStub struct {
	// getByKeyForAuth, listKeysByUserID and listKeysByGroupID are installed before
	// service calls. Their wrappers panic when an operation has no callback, and
	// returned views retain callback ownership.
	getByKeyForAuth   func(ctx context.Context, key string) (*APIKey, error)     // configured direct credential lookup callback
	listKeysByUserID  func(ctx context.Context, userID int64) ([]string, error)  // configured creator enumeration callback
	listKeysByGroupID func(ctx context.Context, groupID int64) ([]string, error) // configured binding enumeration callback
}

// authCacheStub controls optional shared reads and records publication or
// deletion identities.
type authCacheStub struct {
	// getAuthCache selects shared lookup behavior before service use; nil produces
	// redis.Nil. setAuthKeys and deleteAuthKeys capture intent in unsynchronized
	// slices inspected after callers finish.
	getAuthCache   func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) // optional shared-entry lookup callback
	setAuthKeys    []string                                                             // captured published cache identities
	deleteAuthKeys []string                                                             // captured deleted cache identities
}

// apiKeyRepoStub supplies credential or page results and captures
// configuration, deletion and touch attempts.
type apiKeyRepoStub struct {
	// apiKey supplies shallow lookup copies; nested references remain shared.
	// getByIDErr takes precedence over the retained view, and an absent successful
	// GetByID record panics.
	apiKey     *APIKey // configured credential lookup view
	getByIDErr error   // configured credential lookup failure
	deleteErr  error   // configured credential deletion failure
	updateErr  error   // configured configuration write failure
	deletedIDs []int64 // captured credential deletion identifiers
	// updatedKeys records shallow values even when updateErr rejects the write.
	// Scenarios inspect attempts sequentially because capture slices have no
	// synchronization.
	updatedKeys []APIKey // captured configuration write attempts
	// allowListByUserID enables list calls before use; disabled calls panic.
	// Enabled calls capture input before listByUserIDErr is returned or configured
	// page values are copied.
	allowListByUserID   bool                          // creator page fixture enablement
	listByUserIDKeys    []APIKey                      // configured creator page records
	listByUserIDErr     error                         // configured creator page failure
	listByUserIDCalls   []int64                       // captured creator page identifiers
	listByUserIDParams  []pagination.PaginationParams // captured creator page parameters
	listByUserIDFilters []APIKeyListFilters           // captured creator page filters
	// updateLastUsed is installed before a touch scenario and supplies its result
	// after ID and time capture. touchedIDs and touchedUsedAts are unsynchronized;
	// the concurrent singleflight test uses a separate locked recorder.
	updateLastUsed func(ctx context.Context, id int64, usedAt time.Time) error // optional touch result callback
	touchedIDs     []int64                                                     // captured touched credential identifiers
	touchedUsedAts []time.Time                                                 // captured touch write timestamps
}

// apiKeyCacheStub records creator-counter and authentication deletion attempts
// while acknowledging other cache operations.
type apiKeyCacheStub struct {
	// invalidated and deleteAuthKeys record optional cleanup intent for sequential
	// service scenarios. Captures remain in local slices and do not change shared
	// storage.
	invalidated    []int64  // captured creator counter cleanup identifiers
	deleteAuthKeys []string // captured hashed credential deletion identities
}

// touchSingleflightRepo counts touch attempts under a lock and holds their
// completion barrier.
type touchSingleflightRepo struct {
	*apiKeyRepoStub // embedded narrow credential fixture
	// mu protects calls across overlapping touches. blockCh holds their completion
	// until the test observes entry and releases it; the embedded fixture supplies
	// unrelated service operations.
	mu      sync.Mutex    // touch counter serialization lock
	calls   int           // captured touch write attempts
	blockCh chan struct{} // test-owned touch completion barrier
}

// dailyUsageCalendarCache retains the date-based counter identity and requested
// expiry observation.
type dailyUsageCalendarCache struct {
	APIKeyCache               // unused cache methods require setup
	key         string        // captured date-based counter identity
	ttl         time.Duration // captured daily-counter expiry duration
}

// authPreparationGroupRepo records target reads for authentication preparation
// and supplies the configured group or read failure.
type authPreparationGroupRepo struct {
	GroupRepository                // unused methods require setup
	group           *routing.Group // configured runtime target group
	err             error          // configured target lookup failure
	reads           []int64        // observed target lookup identifiers
}

// authPreparationCache retains published snapshots so authentication tests can
// inspect their ownership after request preparation.
type authPreparationCache struct {
	authCacheStub // ordinary shared cache fixture
	// obsoletePublication changes the version during publication to exercise the
	// defensive direct reload after a loaded entry fails snapshot acceptance.
	obsoletePublication bool // publication version replacement switch
	// published retains the pointers supplied to shared publication so tests can
	// check for request preparation mutations after GetByKey finishes.
	published []*APIKeyAuthCacheEntry // captured shared cache entries
}

// TestAPIKeyService_GetByKey_PreparationAcrossLoadPaths exercises ordinary and
// composite credentials through cached, loaded and rejected-snapshot paths.
// It protects fallback precedence, permission errors, IP preparation, snapshot
// ownership, timestamp values and exact repository read counts.
func TestAPIKeyService_GetByKey_PreparationAcrossLoadPaths(t *testing.T) {
	for _, path := range []struct {
		name          string // authentication load path name
		l1            bool   // positive local cache enablement
		l2            bool   // shared cache tier enablement
		seed          bool   // initial positive snapshot presence
		oldSnapshot   bool   // obsolete initial snapshot version
		singleflight  bool   // shared credential load enablement
		directReload  bool   // published snapshot rejection injection
		wantRepoReads int    // expected credential repository reads
		wantL2Reads   int    // expected shared cache reads
	}{
		{name: "l1", l1: true, seed: true},
		{name: "l2", l2: true, seed: true, wantL2Reads: 1},
		{name: "load", wantRepoReads: 1},
		{name: "singleflight", singleflight: true, wantRepoReads: 1},
		{name: "old_l1", l1: true, seed: true, oldSnapshot: true, wantRepoReads: 1},
		{name: "old_l2", l2: true, seed: true, oldSnapshot: true, wantRepoReads: 1, wantL2Reads: 1},
		{name: "direct_reload", l2: true, directReload: true, wantRepoReads: 2, wantL2Reads: 1},
		{name: "singleflight_direct_reload", l2: true, singleflight: true, directReload: true, wantRepoReads: 2, wantL2Reads: 1},
	} {
		t.Run(path.name, func(t *testing.T) {
			for _, scenario := range []string{
				"fallback_before_source_permission", "denied_public_group", "failed_fallback_then_permission",
				"composite", "nil_ip_rules", "empty_ip_rules", "missing_group",
			} {
				t.Run(scenario, func(t *testing.T) {
					createdAt := time.Date(2026, time.October, 1, 2, 3, 4, 0, time.UTC)
					expiresAt := createdAt.Add(time.Hour)
					lastUsedAt := createdAt.Add(time.Minute)
					source := &APIKey{
						ID:                           21,
						UserID:                       3,
						Key:                          "preparation-key",
						Status:                       StatusActive,
						GroupID:                      new(int64(1)),
						FallbackWhenGroupUnavailable: true,
						CreatedAt:                    createdAt,
						ExpiresAt:                    &expiresAt,
						LastUsedAt:                   &lastUsedAt,
						UpdatedAt:                    createdAt,
						Group: &routing.Group{
							ID:                         1,
							Status:                     routing.StatusActive,
							UnavailableFallbackGroupID: new(int64(2)),
						},
						User: &User{
							ID:                      3,
							Status:                  StatusActive,
							GroupRestrictionsLoaded: true,
						},
						IPWhitelist: []string{"192.0.2.0/24", "invalid"},
						IPBlacklist: []string{"192.0.2.8"},
					}
					groups := &authPreparationGroupRepo{group: &routing.Group{ID: 2, Status: routing.StatusActive}}
					wantGroupID := int64(1)
					var wantGroupReads []int64
					var wantErr error
					switch scenario {
					case "fallback_before_source_permission":
						source.Group.Status = routing.StatusDisabled
						source.User.DisabledPublicGroups = []int64{1}
						wantGroupID = 2
						wantGroupReads = []int64{2}
					case "denied_public_group":
						source.User.DisabledPublicGroups = []int64{1}
						wantErr = ErrGroupDisabledForUser
					case "failed_fallback_then_permission":
						source.Group.Status = routing.StatusDisabled
						source.User.DisabledPublicGroups = []int64{1}
						groups.err = errors.New("target lookup failed")
						wantGroupReads = []int64{2}
						wantErr = ErrGroupDisabledForUser
					case "composite":
						source.IsComposite = true
						source.Group.Status = routing.StatusDisabled
						source.User.DisabledPublicGroups = []int64{1}
						source.CompositeGroups = []APIKeyCompositeGroup{{GroupID: 1, Prefix: "one", Group: source.Group}}
					case "nil_ip_rules":
						source.IPWhitelist = nil
						source.IPBlacklist = nil
					case "empty_ip_rules":
						source.IPWhitelist = []string{}
						source.IPBlacklist = []string{}
					case "missing_group":
						source.GroupID = nil
						source.Group = nil
					}
					repoReads := 0
					repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
						repoReads++
						return source, nil
					}}
					cache := &authPreparationCache{obsoletePublication: path.directReload}
					cfg := &Options{APIKeyAuth: APIKeyAuthCacheConfig{Singleflight: path.singleflight}}
					if path.l1 {
						cfg.APIKeyAuth.L1Size = 100
						cfg.APIKeyAuth.L1TTLSeconds = 60
					}
					if path.l2 {
						cfg.APIKeyAuth.L2TTLSeconds = 60
					}
					svc := NewAPIKeyService(repo, nil, groups, nil, nil, cache, cfg)
					svc.Start()
					t.Cleanup(svc.Stop)
					entry := &APIKeyAuthCacheEntry{Snapshot: svc.KeySnapshotFromAPIKey(context.Background(), source)}
					expectedSnapshot := svc.KeySnapshotFromAPIKey(context.Background(), source)
					if path.oldSnapshot {
						// Seed a prior schema so cache presence still requires repository
						// loading before this request can obtain an accepted authentication view.
						entry.Snapshot.Version--
						expectedSnapshot.Version--
					}
					l2Reads := 0
					cache.getAuthCache = func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
						l2Reads++
						if path.seed {
							return entry, nil
						}
						return nil, redis.Nil
					}
					if path.l1 {
						svc.KeySetAuthCacheL1(svc.KeyAuthCacheKey(source.Key), entry)
						// Ristretto publishes asynchronously; flush and verify the seeded
						// entry so this scenario exercises a local hit before preparation.
						svc.authCacheL1.Load().Wait()
						_, found := svc.authCacheL1.Load().Get(svc.KeyAuthCacheKey(source.Key))
						require.True(t, found)
					}

					loaded, err := svc.GetByKey(context.Background(), source.Key)
					require.Equal(t, path.wantRepoReads, repoReads)
					require.Equal(t, path.wantL2Reads, l2Reads)
					require.Equal(t, wantGroupReads, groups.reads)
					require.Equal(t, expectedSnapshot, entry.Snapshot)
					wantPublications := 0
					if path.l2 && path.wantRepoReads > 0 {
						wantPublications = 1
					}
					require.Len(t, cache.published, wantPublications)
					for _, published := range cache.published {
						expectedPublished := *expectedSnapshot
						expectedPublished.Version = KeyApiKeyAuthSnapshotVersion
						if path.directReload {
							expectedPublished.Version--
						}
						require.Equal(t, &expectedPublished, published.Snapshot)
					}
					if wantErr != nil {
						require.ErrorIs(t, err, wantErr)
						require.EqualError(t, err, "get api key: "+wantErr.Error())
						require.Nil(t, loaded)
						if path.directReload {
							require.Nil(t, source.CompiledIPWhitelist)
							require.Nil(t, source.CompiledIPBlacklist)
						}
						return
					}
					require.NoError(t, err)
					require.Equal(t, source.Key, loaded.Key)
					require.Equal(t, source.IsComposite, loaded.IsComposite)
					if scenario == "missing_group" {
						require.Nil(t, loaded.GroupID)
						require.Nil(t, loaded.Group)
					} else {
						require.Equal(t, wantGroupID, *loaded.GroupID)
						require.Equal(t, wantGroupID, loaded.Group.ID)
						require.Equal(t, int64(1), *source.GroupID)
					}
					require.Equal(t, source.IPWhitelist, loaded.IPWhitelist)
					require.Equal(t, source.IPBlacklist, loaded.IPBlacklist)
					require.Equal(t, len(source.IPWhitelist), loaded.CompiledIPWhitelist.PatternCount)
					require.Equal(t, len(source.IPBlacklist), loaded.CompiledIPBlacklist.PatternCount)
					allowed, _ := CheckIPRestrictionWithCompiledRules("192.0.2.9", loaded.CompiledIPWhitelist, loaded.CompiledIPBlacklist)
					require.True(t, allowed)
					denied, _ := CheckIPRestrictionWithCompiledRules("192.0.2.8", loaded.CompiledIPWhitelist, loaded.CompiledIPBlacklist)
					require.Equal(t, len(source.IPBlacklist) == 0, denied)
					require.Equal(t, createdAt, loaded.CreatedAt)
					require.Equal(t, expiresAt, *loaded.ExpiresAt)
					if path.directReload {
						require.Equal(t, source.LastUsedAt, loaded.LastUsedAt)
						require.Equal(t, source.UpdatedAt, loaded.UpdatedAt)
					} else {
						require.Nil(t, loaded.LastUsedAt)
						require.Zero(t, loaded.UpdatedAt)
					}
				})
			}
		})
	}
}

// GetByIDLite captures the requested runtime group identity and returns the
// configured result. The fixture retains its group pointer, and tests inspect
// the sequential read history after authentication finishes.
func (r *authPreparationGroupRepo) GetByIDLite(_ context.Context, id int64) (*routing.Group, error) {
	r.reads = append(r.reads, id)
	return r.group, r.err
}

// SetAuthCache records the entry published by credential loading. When
// obsoletePublication is set, the returned entry becomes unacceptable so
// GetByKey exercises its defensive second repository lookup.
func (c *authPreparationCache) SetAuthCache(_ context.Context, _ string, entry *APIKeyAuthCacheEntry, _ time.Duration) error {
	if c.obsoletePublication {
		// Invalidate this loaded entry before acceptance to expose the defensive
		// reload branch independently of the initial cache lookup's version check.
		entry.Snapshot.Version--
	}
	c.published = append(c.published, entry)
	return nil
}

// TestAPIKeyService_GetByKey_UsesL1Cache loads one credential into an enabled
// local positive cache. It flushes Ristretto publication before checking
// presence and verifies the next read avoids another repository lookup.
func TestAPIKeyService_GetByKey_UsesL1Cache(t *testing.T) {
	var calls int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			atomic.AddInt32(&calls, 1)
			return &APIKey{
				ID:     21,
				UserID: 3,
				Status: StatusActive,
				User: &User{
					ID:          3,
					Status:      StatusActive,
					Role:        "user",
					Balance:     5,
					Concurrency: 2,
				},
			}, nil
		},
	}
	cfg := &Options{
		APIKeyAuth: APIKeyAuthCacheConfig{
			L1Size:       1000,
			L1TTLSeconds: 60,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()
	require.NotNil(t, svc.authCacheL1.Load())

	_, err := svc.GetByKey(context.Background(), "k-l1")
	require.NoError(t, err)
	// Ristretto admission is asynchronous and can reject publication. Flush
	// before observing this known positive entry so the following lookup tests an actual hit.
	svc.authCacheL1.Load().Wait()
	cacheKey := svc.KeyAuthCacheKey("k-l1")
	_, ok := svc.authCacheL1.Load().Get(cacheKey)
	require.True(t, ok)
	_, err = svc.GetByKey(context.Background(), "k-l1")
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// TestAPIKeyService_GetByKey_CachesNegativeOnRepoMiss loads an absent
// credential with both positive and local negative cache settings. It checks no
// shared negative write occurs and a flushed local miss prevents a second
// repository read.
func TestAPIKeyService_GetByKey_CachesNegativeOnRepoMiss(t *testing.T) {
	var repoCalls atomic.Int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			repoCalls.Add(1)
			return nil, ErrAPIKeyNotFound
		},
	}
	cfg := &Options{
		APIKeyAuth: APIKeyAuthCacheConfig{
			L1Size:             100,
			L1TTLSeconds:       60,
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return nil, redis.Nil
	}

	_, err := svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, cache.setAuthKeys, "attacker-controlled misses must not be written to Redis")
	// Flush the retained negative entry before the second lookup so its
	// avoided storage read demonstrates a local hit rather than overlap or timing.
	svc.authNegativeCacheL1.Load().Wait()
	_, err = svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Equal(t, int32(1), repoCalls.Load())
}

// TestApiKeyService_Delete_Success deletes a creator-owned fixture credential
// with a saved local touch deadline. It checks lifecycle deletion,
// creator-counter and authentication notifications, and removal of that touch
// observation.
func TestApiKeyService_Delete_Success(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &APIKey{ID: 42, UserID: 7, Key: "k"},
	}
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{apiKeyRepo: repo, cache: cache}
	svc.lastUsedTouchL1.Store(int64(42), time.Now())

	err := svc.Delete(context.Background(), 42, 7) // Owner 7 deletes key 42.
	require.NoError(t, err)
	require.Equal(t, []int64{42}, repo.deletedIDs)  // Requested key deletion
	require.Equal(t, []int64{7}, cache.invalidated) // Owner's creation-counter cleanup requested
	require.Equal(t, []string{svc.KeyAuthCacheKey("k")}, cache.deleteAuthKeys)
	_, exists := svc.lastUsedTouchL1.Load(int64(42))
	require.False(t, exists, "delete should clear touch debounce cache")
}

// TestAPIKeyService_TouchLastUsed_InvalidKeyID supplies zero and negative
// identifiers to a configured touch callback. It checks successful no-op return
// and absence of captured repository writes.
func TestAPIKeyService_TouchLastUsed_InvalidKeyID(t *testing.T) {
	repo := &apiKeyRepoStub{
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			return errors.New("should not be called")
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo}

	require.NoError(t, svc.TouchLastUsed(context.Background(), 0))
	require.NoError(t, svc.TouchLastUsed(context.Background(), -1))
	require.Empty(t, repo.touchedIDs)
}

// TestAPIKeyService_TouchLastUsed_FirstTouchSucceeds touches one uncached
// positive Key identifier. It checks one nonzero write timestamp and a retained
// deadline observation after the successful storage call.
func TestAPIKeyService_TouchLastUsed_FirstTouchSucceeds(t *testing.T) {
	repo := &apiKeyRepoStub{}
	svc := &APIKeyService{apiKeyRepo: repo}

	err := svc.TouchLastUsed(context.Background(), 123)
	require.NoError(t, err)
	require.Equal(t, []int64{123}, repo.touchedIDs)
	require.Len(t, repo.touchedUsedAts, 1)
	require.False(t, repo.touchedUsedAts[0].IsZero())

	cached, ok := svc.lastUsedTouchL1.Load(int64(123))
	require.True(t, ok, "successful touch should update debounce cache")
	_, isTime := cached.(time.Time)
	require.True(t, isTime)
}

// TestAPIKeyService_TouchLastUsed_DebouncedWithinWindow touches the same Key
// twice before its saved debounce deadline. It checks the second successful
// call skips storage and leaves one captured write.
func TestAPIKeyService_TouchLastUsed_DebouncedWithinWindow(t *testing.T) {
	repo := &apiKeyRepoStub{}
	svc := &APIKeyService{apiKeyRepo: repo}

	require.NoError(t, svc.TouchLastUsed(context.Background(), 123))
	require.NoError(t, svc.TouchLastUsed(context.Background(), 123))

	require.Equal(t, []int64{123}, repo.touchedIDs, "second touch within debounce window should not hit repository")
}

// TestAPIKeyService_TouchLastUsed_ExpiredDebounceTouchesAgain moves a saved
// touch deadline into the past after one successful write. It checks a
// subsequent call writes the same identifier again instead of treating the
// elapsed observation as current.
func TestAPIKeyService_TouchLastUsed_ExpiredDebounceTouchesAgain(t *testing.T) {
	repo := &apiKeyRepoStub{}
	svc := &APIKeyService{apiKeyRepo: repo}

	require.NoError(t, svc.TouchLastUsed(context.Background(), 123))

	// Replace the deadline directly to exercise a permitted second write
	// without sleeping through the production debounce interval or changing its duration.
	svc.lastUsedTouchL1.Store(int64(123), time.Now().Add(-KeyApiKeyLastUsedMinTouch-time.Second))

	require.NoError(t, svc.TouchLastUsed(context.Background(), 123))
	require.Len(t, repo.touchedIDs, 2)
	require.Equal(t, int64(123), repo.touchedIDs[0])
	require.Equal(t, int64(123), repo.touchedIDs[1])
}

// TestAPIKeyService_TouchLastUsed_RepoError fails one synchronous touch write
// through the fixture callback. It checks the returned operation error,
// captured attempt and retained retry deadline despite persistence failure.
func TestAPIKeyService_TouchLastUsed_RepoError(t *testing.T) {
	repo := &apiKeyRepoStub{
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			return errors.New("db write failed")
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo}

	err := svc.TouchLastUsed(context.Background(), 123)
	require.Error(t, err)
	require.ErrorContains(t, err, "touch api key last used")
	require.Equal(t, []int64{123}, repo.touchedIDs)

	cached, ok := svc.lastUsedTouchL1.Load(int64(123))
	require.True(t, ok, "failed touch should still update retry debounce cache")
	_, isTime := cached.(time.Time)
	require.True(t, isTime)
}

// TestAPIKeyService_TouchLastUsed_RepoErrorDebounced fails the first touch and
// immediately retries the same identifier. It checks the retry returns nil
// through debounce and the repository captures only the original failed
// attempt.
func TestAPIKeyService_TouchLastUsed_RepoErrorDebounced(t *testing.T) {
	repo := &apiKeyRepoStub{
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			return errors.New("db write failed")
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo}

	firstErr := svc.TouchLastUsed(context.Background(), 456)
	require.Error(t, firstErr)
	require.ErrorContains(t, firstErr, "touch api key last used")

	secondErr := svc.TouchLastUsed(context.Background(), 456)
	require.NoError(t, secondErr, "failed touch should be debounced and skip immediate retry")
	require.Equal(t, []int64{456}, repo.touchedIDs, "debounced retry should not hit repository again")
}

// TestAPIKeyService_TouchLastUsed_ConcurrentFirstTouchDeduplicated holds a
// counted touch while many same-Key callers start together. It checks joined
// callers succeed and the locked repository fixture observes one shared write
// attempt.
func TestAPIKeyService_TouchLastUsed_ConcurrentFirstTouchDeduplicated(t *testing.T) {
	repo := &touchSingleflightRepo{
		apiKeyRepoStub: &apiKeyRepoStub{},
		blockCh:        make(chan struct{}),
	}
	svc := &APIKeyService{apiKeyRepo: repo}

	// Launch several same-Key callers behind one barrier and hold storage
	// completion to allow overlap while checking coalescing and the retained debounce deadline.
	const workers = 20
	startCh := make(chan struct{})
	errCh := make(chan error, workers)
	var wg sync.WaitGroup

	for range workers {
		// Start callers behind one barrier so their first touch attempts overlap
		// the held write; joining all returns makes the final captured count observable.
		wg.Go(func() {
			<-startCh
			errCh <- svc.TouchLastUsed(context.Background(), 321)
		})
	}

	close(startCh)

	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return repo.calls >= 1
	}, time.Second, 10*time.Millisecond)

	// Release the held writer after entry is observed so singleflight followers
	// can receive its result before the test joins and inspects the attempt count.
	close(repo.blockCh)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, 1, repo.calls, "并发首次 touch 只应写库一次")
}

// TestDailyUsageCacheUsesInjectedCalendar uses east and west fixed-zone
// calendars to increment one daily Key observation. It checks the recorded date
// name matches a before-or-after local sample and the expiry request remains
// twenty-four hours.
func TestDailyUsageCacheUsesInjectedCalendar(t *testing.T) {
	// Fixed-zone offsets use seconds; distant dates distinguish captured service
	// calendar naming from the machine's local date.
	for _, location := range []*time.Location{time.FixedZone("east", 14*3600), time.FixedZone("west", -12*3600)} {
		t.Run(location.String(), func(t *testing.T) {
			calendar := timezone.NewCalendar(location)
			cache := &dailyUsageCalendarCache{}
			service := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &Options{Calendar: calendar})
			before := calendar.Now().Format("2006-01-02")
			require.NoError(t, service.IncrementUsage(context.Background(), 17))
			after := calendar.Now().Format("2006-01-02")
			require.Contains(t, []string{"apikey:usage:17:" + before, "apikey:usage:17:" + after}, cache.key)
			require.Equal(t, 24*time.Hour, cache.ttl)
		})
	}
}

// Create panics when the service repository fixture receives an unsupported
// Create call.
func (s *authRepoStub) Create(ctx context.Context, key *APIKey) error {
	panic("unexpected Create call")
}

// GetByID panics when the service repository fixture receives an unsupported
// GetByID call.
func (s *authRepoStub) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	panic("unexpected GetByID call")
}

// GetKeyAndOwnerID panics when the service repository fixture receives an
// unsupported GetKeyAndOwnerID call.
func (s *authRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

// GetByKey panics when the service repository fixture receives an unsupported
// GetByKey call.
func (s *authRepoStub) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth delegates the configured credential callback and retains its
// returned error identity. Missing callback panics; the fixture performs no
// extra filtering or copying beyond that supplied implementation.
func (s *authRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s.getByKeyForAuth == nil {
		panic("unexpected GetByKeyForAuth call")
	}
	return s.getByKeyForAuth(ctx, key)
}

// RotateCredential panics when the service repository fixture receives an
// unsupported RotateCredential call.
func (s *authRepoStub) RotateCredential(context.Context, *APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update panics when the service repository fixture receives an unsupported
// Update call.
func (s *authRepoStub) Update(ctx context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	panic("unexpected Update call")
}

// Delete panics when the service repository fixture receives an unsupported
// Delete call.
func (s *authRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

// DeleteWithAudit panics when the service repository fixture receives an
// unsupported DeleteWithAudit call.
func (s *authRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

// ListByUserID panics when the service repository fixture receives an
// unsupported ListByUserID call.
func (s *authRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

// VerifyOwnership panics when the service repository fixture receives an
// unsupported VerifyOwnership call.
func (s *authRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the service repository fixture receives an
// unsupported CountByUserID call.
func (s *authRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey panics when the service repository fixture receives an
// unsupported ExistsByKey call.
func (s *authRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

// ListByGroupID panics when the service repository fixture receives an
// unsupported ListByGroupID call.
func (s *authRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the service repository fixture receives an
// unsupported SearchAPIKeys call.
func (s *authRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the service repository fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *authRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the service repository fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *authRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the service repository fixture receives an
// unsupported CountByGroupID call.
func (s *authRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID delegates configured creator enumeration and returns
// callback-owned results. Missing callback panics; the service uses supplied
// secrets to observe invalidation independently of the fixture's unsupported
// persistence methods.
func (s *authRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	if s.listKeysByUserID == nil {
		panic("unexpected ListKeysByUserID call")
	}
	return s.listKeysByUserID(ctx, userID)
}

// ListKeysByGroupID delegates configured group enumeration and returns
// callback-owned results. Missing callback panics, and the fixture imposes no
// independent ordinary or composite binding filter on supplied secrets.
func (s *authRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	if s.listKeysByGroupID == nil {
		panic("unexpected ListKeysByGroupID call")
	}
	return s.listKeysByGroupID(ctx, groupID)
}

// UpdateLastUsed panics when the service repository fixture receives an
// unsupported UpdateLastUsed call.
func (s *authRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

// GetCreateAttemptCount returns zero so authentication fixture reads impose no
// creation threshold.
func (s *authCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount accepts failure-count requests while retaining no
// local counter value.
func (s *authCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount acknowledges creator-counter cleanup in the
// authentication fixture.
func (s *authCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// IncrementDailyUsage acknowledges unrelated daily-counter updates in this
// authentication cache fixture.
func (s *authCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry acknowledges unrelated daily-counter lifetime requests in
// this fixture.
func (s *authCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// GetAuthCache delegates to getAuthCache when configured, otherwise returns
// redis.Nil for a miss. It performs no TTL or schema validation, and the
// callback controls returned entry ownership or failure.
func (s *authCacheStub) GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
	if s.getAuthCache == nil {
		return nil, redis.Nil
	}
	return s.getAuthCache(ctx, key)
}

// SetAuthCache captures the hashed identity before returning success, retaining
// no entry or expiry. Scenarios sequence calls or join worker use before
// inspecting its unsynchronized setAuthKeys slice.
func (s *authCacheStub) SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error {
	s.setAuthKeys = append(s.setAuthKeys, key)
	return nil
}

// DeleteAuthCache captures one hashed deletion identity without changing real
// storage. Scenarios coordinate any worker calls before reading its
// unsynchronized capture slice; this fixture returns success for every attempt.
func (s *authCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

// PublishAuthCacheInvalidation accepts fixture notification intent while
// delivering no real message.
func (s *authCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

// SubscribeAuthCacheInvalidation returns immediately with nil, leaving the
// service wrapper to handle closure. It runs no callback and supplies no
// lifetime barrier; lifecycle tests use their dedicated subscriber fixture.
func (s *authCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// Create panics when the service repository fixture receives an unsupported
// Create call.
func (s *apiKeyRepoStub) Create(ctx context.Context, key *APIKey) error {
	panic("unexpected Create call")
}

// GetByID returns a configured error or shallow credential copy, ignoring the
// requested ID. Missing configured success data panics; nested fields remain
// shared with the retained record for these service scenarios.
func (s *apiKeyRepoStub) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	if s.apiKey != nil {
		clone := *s.apiKey
		return &clone, nil
	}
	panic("unexpected GetByID call")
}

// GetKeyAndOwnerID returns the configured secret and creator, or the selected
// lookup error. An absent record reports Key-not-found; this fixture ignores
// the requested ID and uses its retained record for every lookup.
func (s *apiKeyRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	if s.getByIDErr != nil {
		return "", 0, s.getByIDErr
	}
	if s.apiKey != nil {
		return s.apiKey.Key, s.apiKey.UserID, nil
	}
	return "", 0, ErrAPIKeyNotFound
}

// GetByKey panics when the service repository fixture receives an unsupported
// GetByKey call.
func (s *apiKeyRepoStub) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth panics when the service repository fixture receives an
// unsupported GetByKeyForAuth call.
func (s *apiKeyRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

// RotateCredential panics when the service repository fixture receives an
// unsupported RotateCredential call.
func (s *apiKeyRepoStub) RotateCredential(context.Context, *APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update records a shallow nonnil Key value before returning its configured
// write error. The mask is ignored, so updatedKeys counts attempts including
// failures; input collections and pointers remain shared with captured records.
func (s *apiKeyRepoStub) Update(ctx context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	if key != nil {
		s.updatedKeys = append(s.updatedKeys, *key)
	}
	return s.updateErr
}

// Delete records the requested identifier before returning the selected fixture
// deletion error. A failed attempt remains captured; scenarios sequence calls
// before inspecting this unsynchronized observation slice.
func (s *apiKeyRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// DeleteWithAudit records lifecycle deletion intent before returning the
// configured persistence result. Capture includes failed attempts, and the
// fixture imposes no transaction or tombstone behavior on its observation list.
func (s *apiKeyRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// ListByUserID records creator, page and filter input when explicitly enabled.
// Disabled use panics; configured failure returns after capture. Successful
// reads copy the record slice shallowly and report its length with one fixture
// page, ignoring actual pagination or filtering.
func (s *apiKeyRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	if !s.allowListByUserID {
		panic("unexpected ListByUserID call")
	}
	s.listByUserIDCalls = append(s.listByUserIDCalls, userID)
	s.listByUserIDParams = append(s.listByUserIDParams, params)
	s.listByUserIDFilters = append(s.listByUserIDFilters, filters)
	if s.listByUserIDErr != nil {
		return nil, nil, s.listByUserIDErr
	}
	keys := append([]APIKey(nil), s.listByUserIDKeys...)
	return keys, &pagination.PaginationResult{
		Total:    int64(len(keys)),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    1,
	}, nil
}

// VerifyOwnership panics when the service repository fixture receives an
// unsupported VerifyOwnership call.
func (s *apiKeyRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the service repository fixture receives an
// unsupported CountByUserID call.
func (s *apiKeyRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey panics when the service repository fixture receives an
// unsupported ExistsByKey call.
func (s *apiKeyRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

// ListByGroupID panics when the service repository fixture receives an
// unsupported ListByGroupID call.
func (s *apiKeyRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the service repository fixture receives an
// unsupported SearchAPIKeys call.
func (s *apiKeyRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the service repository fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *apiKeyRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the service repository fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *apiKeyRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the service repository fixture receives an
// unsupported CountByGroupID call.
func (s *apiKeyRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID panics when the service repository fixture receives an
// unsupported ListKeysByUserID call.
func (s *apiKeyRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

// ListKeysByGroupID panics when the service repository fixture receives an
// unsupported ListKeysByGroupID call.
func (s *apiKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

// UpdateLastUsed records the supplied Key ID and timestamp before invoking its
// optional result callback. Missing callback succeeds; captures are
// unsynchronized, and concurrent touch scenarios use their separately locked
// fixture instead.
func (s *apiKeyRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	s.touchedIDs = append(s.touchedIDs, id)
	s.touchedUsedAts = append(s.touchedUsedAts, usedAt)
	if s.updateLastUsed != nil {
		return s.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}

// GetCreateAttemptCount returns zero for creator-counter reads in this service
// fixture.
func (s *apiKeyCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount acknowledges fixture failure-count requests
// without retaining a counter value.
func (s *apiKeyCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount records the creator identifier requested for counter
// cleanup.
func (s *apiKeyCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	s.invalidated = append(s.invalidated, userID)
	return nil
}

// IncrementDailyUsage acknowledges daily-counter requests while retaining no
// usage observation.
func (s *apiKeyCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry accepts lifetime requests while retaining no
// daily-counter expiration.
func (s *apiKeyCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// GetAuthCache returns nil data with no error for every cache identity. The
// service must still treat the unusable entry as requiring load; this fixture
// stores no positive or negative state.
func (s *apiKeyCacheStub) GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
	return nil, nil
}

// SetAuthCache acknowledges shared publication intent while retaining no entry
// or lifetime.
func (s *apiKeyCacheStub) SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error {
	return nil
}

// DeleteAuthCache records the requested hashed identity for service
// invalidation assertions.
func (s *apiKeyCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

// PublishAuthCacheInvalidation acknowledges service notification intent while
// sending no real message.
func (s *apiKeyCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

// SubscribeAuthCacheInvalidation returns immediately with nil and invokes no
// invalidation callback. The service wrapper can interpret it as a closed
// subscription; this fixture supplies no held subscriber lifetime.
func (s *apiKeyCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// UpdateLastUsed increments a locked attempt count, then waits for blockCh
// before succeeding. The caller owns releasing that barrier; ignored timestamp,
// identity and cancellation let the test hold overlapping touches until
// observation.
func (r *touchSingleflightRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	// Hold completion outside the counter mutex so other same-Key callers
	// can overlap while the test reads calls and later releases this writer.
	<-r.blockCh
	return nil
}

// IncrementDailyUsage records the exact service-generated counter name for
// calendar assertions.
func (c *dailyUsageCalendarCache) IncrementDailyUsage(_ context.Context, key string) error {
	c.key = key
	return nil
}

// SetDailyUsageExpiry captures ttl when key matches the prior increment
// identity. A different name returns an error; the fixture retains only this
// observation and implements no actual expiry.
func (c *dailyUsageCalendarCache) SetDailyUsageExpiry(_ context.Context, key string, ttl time.Duration) error {
	if c.key != key {
		return fmt.Errorf("expiry key differs from counter key")
	}
	c.ttl = ttl
	return nil
}
