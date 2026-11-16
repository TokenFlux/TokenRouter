package apikey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// auth_cache_impl.go loads credentials through local and shared caches and
// reconstructs request views from versioned authentication snapshots. Random
// missing credentials occupy a bounded local negative cache. Accepted
// snapshots yield request-owned mutable policy collections, keeping changes to
// those collections separate from cached entries. Team credentials bypass
// positive insertion because their membership allowances are loaded for each
// authentication lookup.
//
// Repository misses can share one in-progress load through service
// singleflight, and a separate lookup channel bounds concurrent storage work.
// Cache failures become misses or skipped writes so storage still decides
// authentication. Local invalidation subscription starts after resources are
// bound, retries failed subscription calls with capped backoff, and stops
// before service cache closure; this wrapper does not clear all cached state
// on reconnection.

// KeyApiKeyAuthSnapshotVersion identifies the serialized fields accepted by
// positive-cache reconstruction, requiring older snapshots to reload
// authentication data.
const KeyApiKeyAuthSnapshotVersion = 48

// KeyApiKeyAuthCacheConfig stores captured local, shared and negative cache
// lifetime choices.
type KeyApiKeyAuthCacheConfig struct {
	// l1Size and l1TTL jointly enable positive local caching when both are
	// positive. l2TTL enables shared reads and writes independently, while
	// negativeTTL enables bounded local misses only when its own duration is positive.
	l1Size      int           // positive local entry capacity
	l1TTL       time.Duration // positive local entry lifetime
	l2TTL       time.Duration // shared positive entry lifetime
	negativeTTL time.Duration // local missing-credential entry lifetime
	// jitterPercent controls expiration variation. KeyJitterTTL leaves nonpositive
	// settings unused and caps positive percentages at one hundred before sampling.
	jitterPercent int  // expiration sampling percentage allowance
	singleflight  bool // shared repository load enablement
}

// AuthCacheInvalidationSubscriberHealth reports subscriber readiness and retry
// failures observed by the service.
type AuthCacheInvalidationSubscriberHealth struct {
	// Connected tracks acknowledgement until the subscription call returns, while
	// Failures counts uncanceled returns. Health reads these separately, so an
	// acknowledged call does not establish uninterrupted message delivery.
	Connected bool   `json:"connected"` // observed subscription acknowledgement state
	Failures  uint64 `json:"failures"`  // cumulative uncanceled subscription returns
}

// KeyNewAPIKeyAuthCacheConfig captures authentication cache settings and
// converts their configured seconds to durations. Nil Options produces disabled
// zero values; lookup concurrency and invalid-auth abuse settings are
// configured separately by service construction.
func KeyNewAPIKeyAuthCacheConfig(cfg *Options) KeyApiKeyAuthCacheConfig {
	if cfg == nil {
		return KeyApiKeyAuthCacheConfig{}
	}
	// Seconds become signed durations so nonpositive configured lifetimes keep
	// their tier disabled when the later enablement predicates inspect them.
	auth := cfg.APIKeyAuth
	return KeyApiKeyAuthCacheConfig{
		l1Size:        auth.L1Size,
		l1TTL:         time.Duration(auth.L1TTLSeconds) * time.Second,
		l2TTL:         time.Duration(auth.L2TTLSeconds) * time.Second,
		negativeTTL:   time.Duration(auth.NegativeTTLSeconds) * time.Second,
		jitterPercent: auth.JitterPercent,
		singleflight:  auth.Singleflight,
	}
}

// KeyL1Enabled reports whether local capacity and lifetime are both positive.
func (c KeyApiKeyAuthCacheConfig) KeyL1Enabled() bool {
	return c.l1Size > 0 && c.l1TTL > 0
}

// KeyL2Enabled reports whether shared positive caching has a positive lifetime.
func (c KeyApiKeyAuthCacheConfig) KeyL2Enabled() bool {
	return c.l2TTL > 0
}

// KeyNegativeEnabled reports whether local missing-credential caching has a
// positive lifetime.
func (c KeyApiKeyAuthCacheConfig) KeyNegativeEnabled() bool {
	return c.negativeTTL > 0
}

// KeyJitterTTL samples a lifetime around ttl using the configured bounded
// percentage. Nonpositive lifetimes or jitter return ttl unchanged; jitter is
// capped at 100 percent, and a nonpositive sampled factor returns the supplied
// lifetime. Package rand functions permit concurrent samples.
func (c KeyApiKeyAuthCacheConfig) KeyJitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	if c.jitterPercent <= 0 {
		return ttl
	}
	// Keep the sampling range within one complete lifetime.
	percent := min(c.jitterPercent, 100)
	// Convert the bounded percentage to the sampling fraction.
	delta := float64(percent) / 100
	randVal := rand.Float64()
	factor := 1 - delta + randVal*(2*delta)
	// A zero lifetime means persistent storage to Ristretto. Retain the
	// configured positive lifetime when this sample reaches a nonpositive factor.
	if factor <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * factor)
}

// initAuthCaches allocates configured positive and negative local caches during
// startup. Negative capacity uses the default bounded size or a smaller
// positive L1 size; failed Ristretto construction leaves that local cache
// absent, and each cache is configured independently.
func (s *APIKeyService) initAuthCaches() {
	if s.authCfg.KeyNegativeEnabled() {
		negativeSize := KeyDefaultNegativeAuthCacheSize
		if s.authCfg.l1Size > 0 && s.authCfg.l1Size < negativeSize {
			negativeSize = s.authCfg.l1Size
		}
		cache, err := ristretto.NewCache(&ristretto.Config[string, any]{
			NumCounters: int64(negativeSize) * 10,
			MaxCost:     int64(negativeSize),
			BufferItems: 64,
		})
		// A failed optional cache allocation leaves this tier disabled; normal
		// lookup can still use configured shared storage or the repository.
		if err == nil {
			s.authNegativeCacheL1.Store(cache)
		}
	}
	if s.authCfg.KeyL1Enabled() {
		cache, err := ristretto.NewCache(&ristretto.Config[string, any]{
			NumCounters: int64(s.authCfg.l1Size) * 10,
			MaxCost:     int64(s.authCfg.l1Size),
			BufferItems: 64,
		})
		// A failed optional cache allocation leaves this tier disabled; normal
		// lookup can still use configured shared storage or the repository.
		if err == nil {
			s.authCacheL1.Store(cache)
		}
	}
}

// StartAuthCacheInvalidationSubscriber starts one retrying local invalidation
// subscription after service resources are bound. Nil services, stopped
// subscribers, absent shared cache or absent local caches skip startup.
//
// Subscriber registration and permanent stopping use subscriberMu so its wait
// group cannot gain work after shutdown begins waiting. The adapter readiness
// callback marks Connected; a returned subscription clears it, counts a failure
// unless canceled, and retries with exponential backoff capped at thirty
// seconds. A previously acknowledged connection resets that backoff. Callers
// supply a context and stop the subscriber before closing the local caches.
func (s *APIKeyService) StartAuthCacheInvalidationSubscriber(ctx context.Context) {
	if s == nil {
		return
	}
	s.subscriberMu.Lock()
	defer s.subscriberMu.Unlock()
	if s.subscriberStopped {
		return
	}

	if s.cache == nil || (s.authCacheL1.Load() == nil && s.authNegativeCacheL1.Load() == nil) {
		return
	}
	s.authInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		subscriberCtx = KeyWithAuthCacheSubscriptionReady(subscriberCtx, func() {
			s.authInvalidationConnected.Store(true)
		})
		s.authInvalidationCancel = cancel
		// Register under the same lifecycle lock used to mark stopping so
		// StopAuthCacheInvalidationSubscriber cannot wait before this worker is counted.
		s.authInvalidationWG.Go(func() {
			backoff := time.Second
			for {
				err := s.cache.SubscribeAuthCacheInvalidation(subscriberCtx, func(cacheKey string) {
					s.KeyInvalidateLocalAuthCache(cacheKey)
				})
				wasConnected := s.authInvalidationConnected.Swap(false)
				if subscriberCtx.Err() != nil {
					return
				}
				// Acknowledged subscription service resets failure delay, allowing the
				// next attempt to recover quickly after a later adapter disconnect.
				if wasConnected {
					backoff = time.Second
				}
				s.authInvalidationFailures.Add(1)
				if err == nil {
					err = errors.New("auth cache invalidation subscription closed")
				}
				slog.Warn("failed to start auth cache invalidation subscriber; retrying", "error", err, "retry_in", backoff)
				// Retry uncanceled subscription returns with capped delay; the context
				// branch stops waiting promptly when the lifecycle owner requests shutdown.
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
			}
		})
	})
}

// KeyInvalidateLocalAuthCache removes cacheKey from both configured local
// caches. Nil services do nothing; this direct callback skips operation-gate
// admission, so its owner coordinates subscriber or outbox work with cache
// lifetime.
func (s *APIKeyService) KeyInvalidateLocalAuthCache(cacheKey string) {
	if s == nil {
		return
	}
	// Remove both positive and negative local results so later lookup can reload
	// changed credential data or discover a newly created formerly missing Key.
	if s.authCacheL1.Load() != nil {
		s.authCacheL1.Load().Del(cacheKey)
	}
	if s.authNegativeCacheL1.Load() != nil {
		s.authNegativeCacheL1.Load().Del(cacheKey)
	}
}

// AuthCacheInvalidationSubscriberHealth returns separately sampled connection
// and failure counters. Nil services return zero health; Connected records the
// acknowledgement callback until a subscription call returns, so it is an
// observation rather than a delivery guarantee.
func (s *APIKeyService) AuthCacheInvalidationSubscriberHealth() AuthCacheInvalidationSubscriberHealth {
	if s == nil {
		return AuthCacheInvalidationSubscriberHealth{}
	}
	return AuthCacheInvalidationSubscriberHealth{
		Connected: s.authInvalidationConnected.Load(),
		Failures:  s.authInvalidationFailures.Load(),
	}
}

// StopAuthCacheInvalidationSubscriber prevents future subscriber startup,
// cancels the active subscription and joins its worker once. Nil services do
// nothing, and repeated stops wait for the same once callback.
//
// The lifecycle flag is set under subscriberMu before waiting, matching startup
// registration under that lock. Cancellation occurs after releasing the mutex
// so adapter teardown can finish independently. This method closes no cache
// itself; its caller drains other service operations and closes caches after
// the subscription has returned.
func (s *APIKeyService) StopAuthCacheInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.authInvalidationStop.Do(func() {
		s.subscriberMu.Lock()
		s.subscriberStopped = true
		cancel := s.authInvalidationCancel
		s.subscriberMu.Unlock()
		// Cancel after releasing subscriberMu so the adapter can finish while
		// startup remains permanently closed; join its callbacks before cache closure.
		if cancel != nil {
			cancel()
		}
		s.authInvalidationWG.Wait()
	})
}

// KeyAuthCacheKey returns the SHA-256 cache identity of the supplied
// credential.
func (s *APIKeyService) KeyAuthCacheKey(key string) string { return AuthCacheKey(key) }

// KeyGetAuthCacheEntry checks positive L1, negative L1, then enabled shared
// storage in that order. Returned pointers remain owned by the cache; callers
// validate them and reconstruct a Key before use. Shared read errors report a
// miss, and a successful shared read attempts local insertion even when its
// entry is unusable.
func (s *APIKeyService) KeyGetAuthCacheEntry(ctx context.Context, cacheKey string) (*APIKeyAuthCacheEntry, bool) {
	if s.authCacheL1.Load() != nil {
		if val, ok := s.authCacheL1.Load().Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok {
				return entry, true
			}
		}
	}
	if s.authNegativeCacheL1.Load() != nil {
		if val, ok := s.authNegativeCacheL1.Load().Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok && entry.NotFound {
				return entry, true
			}
		}
	}
	if s.cache == nil || !s.authCfg.KeyL2Enabled() {
		return nil, false
	}
	entry, err := s.cache.GetAuthCache(ctx, cacheKey)
	// Shared cache failures remain misses so authentication reloads storage
	// instead of treating an unavailable cache as a missing or accepted credential.
	if err != nil {
		return nil, false
	}
	s.KeySetAuthCacheL1(cacheKey, entry)
	return entry, true
}

// KeySetAuthCacheL1 attempts local insertion using the positive or negative
// entry lifetime. Nil entries and missing appropriate caches do nothing;
// inserted entry pointers remain shared, and Ristretto can decline or
// asynchronously reject the supplied value.
func (s *APIKeyService) KeySetAuthCacheL1(cacheKey string, entry *APIKeyAuthCacheEntry) {
	if entry == nil {
		return
	}
	if entry.NotFound {
		if s.authNegativeCacheL1.Load() != nil && s.authCfg.negativeTTL > 0 {
			// Publication may be rejected or delayed. Keep the missing-credential
			// result immutable so concurrent reads can interpret its retained negative flag.
			_ = s.authNegativeCacheL1.Load().SetWithTTL(cacheKey, entry, 1, s.authCfg.KeyJitterTTL(s.authCfg.negativeTTL))
		}
		return
	}
	if s.authCacheL1.Load() == nil {
		return
	}
	ttl := s.authCfg.l1TTL
	ttl = s.authCfg.KeyJitterTTL(ttl)
	// Optional local insertion may be rejected; later misses retain access
	// to shared storage or repository lookup through the normal authentication flow.
	_ = s.authCacheL1.Load().SetWithTTL(cacheKey, entry, 1, ttl)
}

// KeySetAuthCacheEntry attempts local insertion before writing enabled shared
// storage with a jittered ttl. Nil entries do nothing, the shared write error
// is suppressed, and callers treat the published entry data as immutable
// afterward.
func (s *APIKeyService) KeySetAuthCacheEntry(ctx context.Context, cacheKey string, entry *APIKeyAuthCacheEntry, ttl time.Duration) {
	if entry == nil {
		return
	}
	s.KeySetAuthCacheL1(cacheKey, entry)
	if s.cache == nil || !s.authCfg.KeyL2Enabled() {
		return
	}
	// Shared publication failure does not reject the loaded credential;
	// future misses can reload it, while the accepted local entry may remain usable.
	_ = s.cache.SetAuthCache(ctx, cacheKey, entry, s.authCfg.KeyJitterTTL(ttl))
}

// KeyDeleteAuthCache clears both local caches, then attempts shared deletion
// and publication. A nil shared cache ends after local invalidation;
// publication is attempted even after deletion failure, and both remote errors
// are suppressed.
func (s *APIKeyService) KeyDeleteAuthCache(ctx context.Context, cacheKey string) {
	// Remove both positive and negative local results so later lookup can reload
	// changed credential data or discover a newly created formerly missing Key.
	if s.authCacheL1.Load() != nil {
		s.authCacheL1.Load().Del(cacheKey)
	}
	if s.authNegativeCacheL1.Load() != nil {
		s.authNegativeCacheL1.Load().Del(cacheKey)
	}
	if s.cache == nil {
		return
	}
	_ = s.cache.DeleteAuthCache(ctx, cacheKey)
	// Publication attempts remote L1 invalidation even if shared deletion
	// failed. Database outbox delivery and entry expiration handle missed attempts.
	_ = s.cache.PublishAuthCacheInvalidation(ctx, cacheKey)
}

// KeyLoadAuthCacheEntry reads and converts the credential view into a cache
// entry. Repository absence returns a negative entry and optionally inserts it
// locally; other lookup failures are returned to authentication.
//
// Snapshot construction requires a hydrated payer. Team views return the
// constructed entry directly and skip positive insertion so later
// authentication reloads their changing membership allowances. Personal
// snapshots are published before the entry is returned. Service singleflight
// can share this load result concurrently, while normal callers reconstruct
// their own view from the resulting snapshot.
func (s *APIKeyService) KeyLoadAuthCacheEntry(ctx context.Context, key, cacheKey string) (*APIKeyAuthCacheEntry, error) {
	apiKey, err := s.KeyLookupAPIKeyForAuth(ctx, key)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			entry := &APIKeyAuthCacheEntry{NotFound: true}
			if s.authCfg.KeyNegativeEnabled() {
				// Invalid credentials have attacker-controlled cardinality. Keep negative entries
				// in the bounded local cache to avoid Redis writes for random-key scans.
				s.KeySetAuthCacheL1(cacheKey, entry)
			}
			return entry, nil
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	snapshot := s.KeySnapshotFromAPIKey(ctx, apiKey)
	if snapshot == nil {
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	entry := &APIKeyAuthCacheEntry{Snapshot: snapshot}
	if apiKey.TeamID != nil {
		// Membership usage changes between admitted requests. Skip positive cache
		// publication so the next team lookup reloads those current member allowances.
		return entry, nil
	}
	s.KeySetAuthCacheEntry(ctx, cacheKey, entry, s.authCfg.l2TTL)
	return entry, nil
}

// KeyLookupAPIKeyForAuth loads authentication fields and hydrates team identity
// from current repositories. A nil service or missing Key repository reports a
// missing credential.
//
// When lookup slots exist, the method attempts nonblocking admission and counts
// accepted work until the repository and hydration return. A full channel
// reports overload without queuing; a selected cancellation branch returns the
// context error. Direct configurations without slots delegate immediately and
// skip these metrics. Admitted callers retain their slot through team owner and
// actor lookups, bounding that complete storage load.
func (s *APIKeyService) KeyLookupAPIKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil {
		return nil, ErrAPIKeyNotFound
	}
	if s.authLookupSlots == nil {
		apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
		return s.KeyHydrateTeamAPIKey(ctx, apiKey, err)
	}
	s.authLookupTotal.Add(1)
	select {
	// Hold capacity through owner and actor hydration as well as the Key
	// read; releasing early would let those additional storage calls bypass the bound.
	case s.authLookupSlots <- struct{}{}:
		s.authLookupInFlight.Add(1)
		defer func() {
			s.authLookupInFlight.Add(-1)
			<-s.authLookupSlots
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		s.authLookupRejected.Add(1)
		return nil, ErrAPIKeyAuthOverloaded
	}
	apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
	return s.KeyHydrateTeamAPIKey(ctx, apiKey, err)
}

// KeyHydrateTeamAPIKey returns personal or already failed reads directly and
// hydrates usable team credentials. Disabled states other than expiry or quota
// exhaustion return before team lookup, retaining their disabled-key error
// priority.
//
// Team authentication requires enabled team support, a matching current context
// and a member join time no later than Key creation. A missing context error
// becomes membership rejection; other repository failures retain their
// identity. Successful team repository contexts supply an owner, then user
// reads populate the assigned actor and current payer. The supplied Key is
// mutated with these hydrated views.
func (s *APIKeyService) KeyHydrateTeamAPIKey(ctx context.Context, apiKey *APIKey, err error) (*APIKey, error) {
	if err != nil || apiKey == nil || apiKey.TeamID == nil {
		return apiKey, err
	}
	// A disabled credential should keep its state-specific rejection even if
	// its assigned member departed; expiry and quota states need later spending checks.
	if !apiKey.IsActive() && apiKey.Status != StatusAPIKeyExpired && apiKey.Status != StatusAPIKeyQuotaExhausted {
		return apiKey, nil
	}
	if s.cfg != nil && !s.cfg.Team.Enabled {
		return nil, ErrTeamFeatureDisabled
	}
	if s.teamRepo == nil {
		return nil, ErrTeamFeatureDisabled
	}
	teamCtx, err := s.teamRepo.GetContextByUserID(ctx, apiKey.UserID)
	// A creator with no current team context cannot authorize this team Key.
	// Map that absence to membership rejection while retaining other storage failures.
	if err != nil {
		if errors.Is(err, ErrTeamNotFound) {
			return nil, ErrTeamMembershipRequired
		}
		return nil, err
	}
	if teamCtx == nil || teamCtx.Team == nil || teamCtx.Membership == nil || teamCtx.Team.ID != *apiKey.TeamID {
		return nil, ErrTeamMembershipRequired
	}
	if teamCtx.Membership.JoinedAt.After(apiKey.CreatedAt) {
		return nil, ErrTeamMembershipRequired
	}
	actor, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return nil, err
	}
	owner, err := s.userRepo.GetByID(ctx, teamCtx.Owner.UserID)
	if err != nil {
		return nil, err
	}
	apiKey.ActorUser = actor
	apiKey.User = owner
	apiKey.Team = teamCtx.Team
	apiKey.TeamMembership = teamCtx.Membership
	return apiKey, nil
}

// KeyApplyAuthCacheEntry reports whether an entry supplies a usable lookup
// result and reconstructs accepted positive snapshots. Negative entries report
// a used missing-Key result; nil entries, missing snapshots and obsolete
// versions report unused with no error so callers can reload.
func (s *APIKeyService) KeyApplyAuthCacheEntry(key string, entry *APIKeyAuthCacheEntry) (*APIKey, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if entry.NotFound {
		return nil, true, ErrAPIKeyNotFound
	}
	if entry.Snapshot == nil {
		return nil, false, nil
	}
	if entry.Snapshot.Version != KeyApiKeyAuthSnapshotVersion {
		return nil, false, nil
	}
	return s.KeySnapshotToAPIKey(key, entry.Snapshot), true, nil
}

// KeySnapshotFromAPIKey builds a versioned snapshot from a Key with a hydrated
// payer, returning nil when either is absent. It copies the stored fields,
// supported collections and scalar pointers used by authentication.
//
// Payer-group RPM overrides are prefetched for active ordinary and composite
// groups; lookup failure leaves nil for later scheduler lookup. Ordinary group
// Fast policy uses the service resolver, while composite binding groups retain
// their supplied policy value. Team actor, team and membership data retain
// their separate identities. Callers publishing this result keep its data
// immutable while cached.
func (s *APIKeyService) KeySnapshotFromAPIKey(ctx context.Context, apiKey *APIKey) *APIKeyAuthSnapshot {
	if apiKey == nil || apiKey.User == nil {
		return nil
	}
	snapshot := &APIKeyAuthSnapshot{
		Version:                      KeyApiKeyAuthSnapshotVersion,
		APIKeyID:                     apiKey.ID,
		UserID:                       apiKey.UserID,
		TeamID:                       clonePointer(apiKey.TeamID),
		TeamOwnerDisabled:            apiKey.TeamOwnerDisabled,
		CreatedAt:                    apiKey.CreatedAt,
		GroupID:                      clonePointer(apiKey.GroupID),
		IsComposite:                  apiKey.IsComposite,
		Name:                         apiKey.Name,
		Status:                       apiKey.Status,
		FastModePolicy:               apiKey.FastModePolicy,
		BillingMode:                  apiKey.BillingMode,
		PreferredSubscriptionID:      clonePointer(apiKey.PreferredSubscriptionID),
		ModelMapping:                 CloneModelMapping(apiKey.ModelMapping),
		IPWhitelist:                  slices.Clone(apiKey.IPWhitelist),
		IPBlacklist:                  slices.Clone(apiKey.IPBlacklist),
		Quota:                        apiKey.Quota,
		QuotaUsed:                    apiKey.QuotaUsed,
		ExpiresAt:                    clonePointer(apiKey.ExpiresAt),
		MaxTokenRateMultiplier:       apiKey.MaxTokenRateMultiplier,
		ConcurrencyLimit:             apiKey.ConcurrencyLimit,
		RPMLimit:                     apiKey.RPMLimit,
		RateLimit5h:                  apiKey.RateLimit5h,
		RateLimit1d:                  apiKey.RateLimit1d,
		RateLimit7d:                  apiKey.RateLimit7d,
		FallbackWhenGroupUnavailable: apiKey.FallbackWhenGroupUnavailable,
		User: APIKeyAuthUserSnapshot{
			ID:                         apiKey.User.ID,
			Status:                     apiKey.User.Status,
			Role:                       apiKey.User.Role,
			Balance:                    apiKey.User.Balance,
			Concurrency:                apiKey.User.Concurrency,
			AllowedGroups:              slices.Clone(apiKey.User.AllowedGroups),
			Email:                      apiKey.User.Email,
			Username:                   apiKey.User.Username,
			BalanceNotifyEnabled:       apiKey.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: apiKey.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     clonePointer(apiKey.User.BalanceNotifyThreshold),
			BalanceNotifyExtraEmails:   slices.Clone(apiKey.User.BalanceNotifyExtraEmails),
			TotalRecharged:             apiKey.User.TotalRecharged,
			RPMLimit:                   apiKey.User.RPMLimit,
			DisabledPublicGroups:       append([]int64(nil), apiKey.User.DisabledPublicGroups...),
		},
	}
	if apiKey.ActorUser != nil {
		snapshot.ActorUser = &APIKeyAuthActorSnapshot{ID: apiKey.ActorUser.ID, Status: apiKey.ActorUser.Status, Email: apiKey.ActorUser.Email, Username: apiKey.ActorUser.Username}
	}
	if apiKey.Team != nil {
		snapshot.Team = &APIKeyAuthTeamSnapshot{ID: apiKey.Team.ID, Name: apiKey.Team.Name, Status: apiKey.Team.Status}
		snapshot.TeamMembership = cloneMembership(apiKey.TeamMembership)
	}

	// Carry an available payer-group RPM override into the snapshot so
	// scheduler admission can use it before a separate total-user RPM check.
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 && apiKey.Group != nil && apiKey.Group.IsActive() && s.userGroupRateRepo != nil {
		override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, *apiKey.GroupID)
		if err == nil && override != nil {
			snapshot.User.UserGroupRPMOverride = clonePointer(override)
		}
		// A failed or absent override keeps nil, allowing scheduler admission
		// to use its repository lookup and then the configured group allowance.
	}
	snapshot.Group = KeyAuthGroupSnapshotFromGroup(apiKey.Group)
	if snapshot.Group != nil {
		snapshot.Group.OpenAIFastPolicy = s.groupPolicy(apiKey.Group)
	}
	if apiKey.IsComposite {
		snapshot.CompositeGroups = make([]APIKeyAuthCompositeGroupSnapshot, 0, len(apiKey.CompositeGroups))
		for _, binding := range apiKey.CompositeGroups {
			bindingSnapshot := APIKeyAuthCompositeGroupSnapshot{
				ID:               binding.ID,
				GroupID:          binding.GroupID,
				Prefix:           binding.Prefix,
				NormalizedPrefix: binding.NormalizedPrefix,
				SortOrder:        binding.SortOrder,
				Group:            KeyAuthGroupSnapshotFromGroup(binding.Group),
			}
			// The override belongs to this payer and selected binding. A failed
			// prefetch keeps nil so later scheduler admission can query that pair again.
			if binding.Group != nil && binding.Group.IsActive() && s.userGroupRateRepo != nil {
				override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, binding.GroupID)
				if err == nil {
					bindingSnapshot.UserGroupRPMOverride = clonePointer(override)
				}
			}
			snapshot.CompositeGroups = append(snapshot.CompositeGroups, bindingSnapshot)
		}
	}
	return snapshot
}

// KeySnapshotToAPIKey reconstructs a request Key from snapshot and the
// caller-supplied credential, returning nil for a nil snapshot. Direct callers
// check schema eligibility themselves or use KeyApplyAuthCacheEntry.
//
// The conversion copies supported maps, slices and scalar pointers, including
// group policy and membership windows, so ordinary request changes do not
// mutate the cached input. It marks payer restrictions loaded and groups
// hydrated. An absent actor snapshot uses the reconstructed payer pointer;
// composite bindings receive the snapshot Key ID and independent group data. IP
// rules are compiled from the copied patterns before return.
func (s *APIKeyService) KeySnapshotToAPIKey(key string, snapshot *APIKeyAuthSnapshot) *APIKey {
	if snapshot == nil {
		return nil
	}
	apiKey := &APIKey{
		ID:                           snapshot.APIKeyID,
		UserID:                       snapshot.UserID,
		TeamID:                       clonePointer(snapshot.TeamID),
		TeamOwnerDisabled:            snapshot.TeamOwnerDisabled,
		CreatedAt:                    snapshot.CreatedAt,
		GroupID:                      clonePointer(snapshot.GroupID),
		IsComposite:                  snapshot.IsComposite,
		Key:                          key,
		Name:                         snapshot.Name,
		Status:                       snapshot.Status,
		FastModePolicy:               snapshot.FastModePolicy,
		BillingMode:                  snapshot.BillingMode,
		PreferredSubscriptionID:      clonePointer(snapshot.PreferredSubscriptionID),
		ModelMapping:                 CloneModelMapping(snapshot.ModelMapping),
		IPWhitelist:                  slices.Clone(snapshot.IPWhitelist),
		IPBlacklist:                  slices.Clone(snapshot.IPBlacklist),
		Quota:                        snapshot.Quota,
		QuotaUsed:                    snapshot.QuotaUsed,
		ExpiresAt:                    clonePointer(snapshot.ExpiresAt),
		MaxTokenRateMultiplier:       snapshot.MaxTokenRateMultiplier,
		ConcurrencyLimit:             snapshot.ConcurrencyLimit,
		RPMLimit:                     snapshot.RPMLimit,
		RateLimit5h:                  snapshot.RateLimit5h,
		RateLimit1d:                  snapshot.RateLimit1d,
		RateLimit7d:                  snapshot.RateLimit7d,
		FallbackWhenGroupUnavailable: snapshot.FallbackWhenGroupUnavailable,
		User: &User{
			ID:                         snapshot.User.ID,
			Status:                     snapshot.User.Status,
			Role:                       snapshot.User.Role,
			Balance:                    snapshot.User.Balance,
			Concurrency:                snapshot.User.Concurrency,
			AllowedGroups:              slices.Clone(snapshot.User.AllowedGroups),
			Email:                      snapshot.User.Email,
			Username:                   snapshot.User.Username,
			BalanceNotifyEnabled:       snapshot.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: snapshot.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     clonePointer(snapshot.User.BalanceNotifyThreshold),
			BalanceNotifyExtraEmails:   slices.Clone(snapshot.User.BalanceNotifyExtraEmails),
			TotalRecharged:             snapshot.User.TotalRecharged,
			RPMLimit:                   snapshot.User.RPMLimit,
			UserGroupRPMOverride:       clonePointer(snapshot.User.UserGroupRPMOverride),
			DisabledPublicGroups:       append([]int64(nil), snapshot.User.DisabledPublicGroups...),
			GroupRestrictionsLoaded:    true,
		},
	}
	if snapshot.ActorUser != nil {
		apiKey.ActorUser = &User{ID: snapshot.ActorUser.ID, Status: snapshot.ActorUser.Status, Email: snapshot.ActorUser.Email, Username: snapshot.ActorUser.Username}
	} else {
		apiKey.ActorUser = apiKey.User
	}
	if snapshot.Team != nil {
		apiKey.Team = &Team{ID: snapshot.Team.ID, Name: snapshot.Team.Name, Status: snapshot.Team.Status}
		apiKey.TeamMembership = cloneMembership(snapshot.TeamMembership)
	}
	apiKey.Group = KeyGroupFromAuthSnapshot(snapshot.Group)
	if snapshot.IsComposite {
		apiKey.CompositeGroups = make([]APIKeyCompositeGroup, 0, len(snapshot.CompositeGroups))
		for _, binding := range snapshot.CompositeGroups {
			apiKey.CompositeGroups = append(apiKey.CompositeGroups, APIKeyCompositeGroup{
				ID:                   binding.ID,
				APIKeyID:             snapshot.APIKeyID,
				GroupID:              binding.GroupID,
				Prefix:               binding.Prefix,
				NormalizedPrefix:     binding.NormalizedPrefix,
				SortOrder:            binding.SortOrder,
				UserGroupRPMOverride: clonePointer(binding.UserGroupRPMOverride),
				Group:                KeyGroupFromAuthSnapshot(binding.Group),
			})
		}
	}
	s.KeyCompileAPIKeyIPRules(apiKey)
	return apiKey
}

// KeyAuthGroupSnapshotFromGroup copies the group fields used by authentication
// snapshot serialization. Nil input remains nil; supported policy maps,
// candidate slices, configuration collections and scalar pointers receive
// independent storage through their respective copy helpers.
func KeyAuthGroupSnapshotFromGroup(group *routing.Group) *APIKeyAuthGroupSnapshot {
	if group == nil {
		return nil
	}
	return &APIKeyAuthGroupSnapshot{
		ID:                              group.ID,
		Name:                            group.Name,
		SchedulerType:                   group.SchedulerType,
		AdvancedSchedulerOverrides:      accessview.CloneGroupAdvancedSchedulerOverrides(group.AdvancedSchedulerOverrides),
		IsExclusive:                     group.IsExclusive,
		Status:                          group.Status,
		RateMultiplier:                  group.RateMultiplier,
		SessionIsolationEnabled:         group.SessionIsolationEnabled,
		AllowImageGeneration:            group.AllowImageGeneration,
		AllowBatchImageGeneration:       group.AllowBatchImageGeneration,
		RoutingPolicy:                   group.RoutingPolicy.Clone(),
		ClaudeCodeOnly:                  group.ClaudeCodeOnly,
		FallbackGroupID:                 clonePointer(group.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: clonePointer(group.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID:      clonePointer(group.UnavailableFallbackGroupID),
		ModelRouting:                    cloneModelRouting(group.ModelRouting),
		ModelRoutingEnabled:             group.ModelRoutingEnabled,
		MCPXMLInject:                    group.MCPXMLInject,
		ProtocolFallbacks:               protocol.CloneFallbacks(group.ProtocolFallbacks),
		ResponsesImagePolicy:            group.ResponsesImagePolicy,
		SupportedModelScopes:            slices.Clone(group.SupportedModelScopes),
		AllowedProtocols:                cloneGroupClientProtocols(group.AllowedProtocols),
		AllowLive:                       group.AllowLive,
		ForceOpenAIFast:                 group.ForceOpenAIFast,
		OpenAIFastPolicy:                group.OpenAIFastPolicy,
		DefaultMappedModel:              group.DefaultMappedModel,
		ModelsListConfig:                cloneModelsList(group.ModelsListConfig),
		RPMLimit:                        group.RPMLimit,
		MaxReasoningEffort:              group.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     group.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         slices.Clone(group.ReasoningEffortMappings),
	}
}

// KeyGroupFromAuthSnapshot reconstructs a hydrated group view from cached
// authentication configuration. Nil input remains nil; supported mutable policy
// collections and scalar pointers are copied for request processing, while
// configuration fields absent from the snapshot retain their zero values.
func KeyGroupFromAuthSnapshot(snapshot *APIKeyAuthGroupSnapshot) *routing.Group {
	if snapshot == nil {
		return nil
	}
	return &routing.Group{
		ID:                              snapshot.ID,
		Name:                            snapshot.Name,
		SchedulerType:                   snapshot.SchedulerType,
		AdvancedSchedulerOverrides:      accessview.CloneGroupAdvancedSchedulerOverrides(snapshot.AdvancedSchedulerOverrides),
		IsExclusive:                     snapshot.IsExclusive,
		Status:                          snapshot.Status,
		Hydrated:                        true,
		RateMultiplier:                  snapshot.RateMultiplier,
		SessionIsolationEnabled:         snapshot.SessionIsolationEnabled,
		AllowImageGeneration:            snapshot.AllowImageGeneration,
		AllowBatchImageGeneration:       snapshot.AllowBatchImageGeneration,
		RoutingPolicy:                   snapshot.RoutingPolicy.Clone(),
		ClaudeCodeOnly:                  snapshot.ClaudeCodeOnly,
		FallbackGroupID:                 clonePointer(snapshot.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: clonePointer(snapshot.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID:      clonePointer(snapshot.UnavailableFallbackGroupID),
		ModelRouting:                    cloneModelRouting(snapshot.ModelRouting),
		ModelRoutingEnabled:             snapshot.ModelRoutingEnabled,
		MCPXMLInject:                    snapshot.MCPXMLInject,
		ProtocolFallbacks:               protocol.CloneFallbacks(snapshot.ProtocolFallbacks),
		ResponsesImagePolicy:            snapshot.ResponsesImagePolicy,
		SupportedModelScopes:            slices.Clone(snapshot.SupportedModelScopes),
		AllowedProtocols:                cloneGroupClientProtocols(snapshot.AllowedProtocols),
		AllowLive:                       snapshot.AllowLive,
		ForceOpenAIFast:                 snapshot.ForceOpenAIFast,
		OpenAIFastPolicy:                snapshot.OpenAIFastPolicy,
		DefaultMappedModel:              snapshot.DefaultMappedModel,
		ModelsListConfig:                cloneModelsList(snapshot.ModelsListConfig),
		RPMLimit:                        snapshot.RPMLimit,
		MaxReasoningEffort:              snapshot.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     snapshot.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         slices.Clone(snapshot.ReasoningEffortMappings),
	}
}

// AuthCacheKey returns the lowercase hexadecimal SHA-256 digest of credential
// text.
func AuthCacheKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
