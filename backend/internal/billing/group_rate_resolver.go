package billing

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// group_rate_resolver.go resolves user-group billing multipliers with caching
// and shared concurrent loads.
// A user override or the supplied group default determines the multiplier for
// a completed request. Successful reads cache that selected number under the
// user/group pair, including the default when no override exists. The key
// contains no default or repository identity, so cached results can outlive a
// change to either input until the entry expires.
//
// Concurrent misses share the winning loader's context and selected number.
// When the repository returns an error, each caller uses its own default and
// that error stays uncached. Shared caches and singleflight groups require
// compatible sources and default rules for each key; callers waiting on a load
// receive its outcome before returning.

// Group rate TTL values determine how long the resolver retains successful
// override reads and caller defaults selected for missing overrides.
const (
	defaultUserGroupRateCacheTTL = 30 * time.Second
	DefaultGroupRateCacheTTL     = defaultUserGroupRateCacheTTL
)

// Shared resolver counters aggregate cache hits, misses and load outcomes
// across instances for gateway diagnostics; individual resolver caches remain
// separate by default.
var groupRateMetrics GroupRateMetrics

// GroupRateResolver reads and caches the effective multiplier for a user/group
// pair.
type GroupRateResolver struct {
	// repo may be nil. Cached float values remain readable, while a miss
	// immediately selects the supplied group default without a repository lookup.
	repo UserGroupRateRepository // user group rate reader
	// cache stores both overrides and selected defaults by user/group key.
	// Supplied caches can be shared, so their entries need matching source and
	// default rules.
	cache    *querycache.TTLMap[float64] // effective user group multipliers
	cacheTTL time.Duration               // cached multiplier entry lifetime
	// sf shares misses by user/group key and uses the load winner's context.
	// Waiting callers receive that outcome before applying their own error
	// fallback.
	sf           *singleflight.Group          // coalesced user group loads
	logComponent string                       // failed read log component
	observe      func(string, string, ...any) // failed read observation callback
}

// GroupRateMetrics holds concurrent counters for all resolvers. Hit counts
// usable cache reads, Miss counts repository-backed misses, Load counts begun
// repository reads, Shared counts shared results, and Fallback counts failed
// or unusable load results.
type GroupRateMetrics struct {
	// Hit, Miss, Load, Shared and Fallback accumulate across all resolver
	// instances. Atomic updates permit concurrent resolutions; snapshots load
	// each counter separately and may span simultaneous activity.
	// Hit: usable cache reads; Miss: repository-backed cache misses.
	// Load: repository read attempts; Shared: shared caller results.
	// Fallback: unusable load outcomes.
	Hit, Miss, Load, Shared, Fallback atomic.Int64
}

// NewGroupRateResolver constructs a resolver with a thirty-second TTL when
// cacheTTL is nonpositive. Nil cache and singleflight inputs allocate local
// instances; supplied instances are shared and must use matching
// repository/default rules for their user/group keys. Nil observation disables
// logging.
func NewGroupRateResolver(repo UserGroupRateRepository, cache *querycache.TTLMap[float64], cacheTTL time.Duration, sf *singleflight.Group, logComponent string, observe func(string, string, ...any)) *GroupRateResolver {
	if observe == nil {
		observe = func(string, string, ...any) {}
	}
	if cacheTTL <= 0 {
		cacheTTL = defaultUserGroupRateCacheTTL
	}
	if cache == nil {
		cache = querycache.NewTTLMap[float64](cacheTTL)
	}
	if logComponent == "" {
		logComponent = "service.gateway"
	}
	if sf == nil {
		sf = &singleflight.Group{}
	}

	return &GroupRateResolver{
		repo:         repo,
		cache:        cache,
		cacheTTL:     cacheTTL,
		sf:           sf,
		logComponent: logComponent,
		observe:      observe,
	}
}

// Resolve returns the cached or stored user-group multiplier. Invalid IDs, a
// nil resolver or an uncached pair without a repository return the supplied
// groupDefaultMultiplier. A successful read without an override caches its
// selected default until expiry, so callers using the same key need compatible
// default rules.
//
// Concurrent misses share one read with the winning caller's context and
// default. Waiting callers cannot cancel their own wait through ctx; they
// receive that shared outcome. A returned read error is observed and remains
// uncached, and each caller then returns its own supplied default.
func (r *GroupRateResolver) Resolve(ctx context.Context, userID, groupID int64, groupDefaultMultiplier float64) float64 {
	if r == nil || userID <= 0 || groupID <= 0 {
		return groupDefaultMultiplier
	}

	key := fmt.Sprintf("%d:%d", userID, groupID)
	if r.cache != nil {
		if multiplier, ok := r.cache.Get(key); ok {
			groupRateMetrics.Hit.Add(1)
			return multiplier
		}
	}
	if r.repo == nil {
		return groupDefaultMultiplier
	}
	groupRateMetrics.Miss.Add(1)

	// Recheck the cache inside the shared loader because another load may
	// populate this pair after the initial miss but before this loader runs.
	value, err, shared := r.sf.Do(key, func() (any, error) {
		if r.cache != nil {
			if multiplier, ok := r.cache.Get(key); ok {
				groupRateMetrics.Hit.Add(1)
				return multiplier, nil
			}
		}

		groupRateMetrics.Load.Add(1)
		userRate, repoErr := r.repo.GetByUserAndGroup(ctx, userID, groupID)
		if repoErr != nil {
			return nil, repoErr
		}

		multiplier := groupDefaultMultiplier
		if userRate != nil {
			multiplier = *userRate
		}
		// Cache the selected default as well as a stored override, so repeated
		// requests avoid another repository read until this entry expires.
		if r.cache != nil {
			r.cache.Set(key, multiplier, r.cacheTTL)
		}
		return multiplier, nil
	})
	if shared {
		groupRateMetrics.Shared.Add(1)
	}
	// A failed shared load cannot establish a reusable multiplier; retain each
	// caller's own supplied group default and report the repository failure.
	if err != nil {
		groupRateMetrics.Fallback.Add(1)
		r.observe(r.logComponent, "get user group rate failed, fallback to group default: user=%d group=%d err=%v", userID, groupID, err)
		return groupDefaultMultiplier
	}

	multiplier, ok := value.(float64)
	// An unusable singleflight result has no valid multiplier to return; choose
	// this caller's default without publishing the unexpected value into its
	// cache.
	if !ok {
		groupRateMetrics.Fallback.Add(1)
		return groupDefaultMultiplier
	}
	return multiplier
}

// SharedGroupRateMetrics returns the counters accumulated across all
// group-rate resolvers.
func SharedGroupRateMetrics() *GroupRateMetrics { return &groupRateMetrics }

// DeleteExpired removes expired entries from this resolver's cache when
// available. Nil receivers and absent caches produce no operation; expiry
// affects cached defaults and overrides by the same TTL rule.
func (r *GroupRateResolver) DeleteExpired() {
	if r != nil && r.cache != nil {
		r.cache.DeleteExpired()
	}
}
