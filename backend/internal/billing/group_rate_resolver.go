package billing

import (
	"context"
	"fmt"
	"time"

	"sync/atomic"

	gocache "github.com/patrickmn/go-cache"
	"golang.org/x/sync/singleflight"
)

type GroupRateResolver struct {
	repo         UserGroupRateRepository
	cache        *gocache.Cache
	cacheTTL     time.Duration
	sf           *singleflight.Group
	logComponent string
	observe      func(string, string, ...any)
}

func NewGroupRateResolver(repo UserGroupRateRepository, cache *gocache.Cache, cacheTTL time.Duration, sf *singleflight.Group, logComponent string, observe func(string, string, ...any)) *GroupRateResolver {
	if observe == nil {
		observe = func(string, string, ...any) {}
	}
	if cacheTTL <= 0 {
		cacheTTL = defaultUserGroupRateCacheTTL
	}
	if cache == nil {
		cache = gocache.New(cacheTTL, 0)
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

func (r *GroupRateResolver) Resolve(ctx context.Context, userID, groupID int64, groupDefaultMultiplier float64) float64 {
	if r == nil || userID <= 0 || groupID <= 0 {
		return groupDefaultMultiplier
	}

	key := fmt.Sprintf("%d:%d", userID, groupID)
	if r.cache != nil {
		if cached, ok := r.cache.Get(key); ok {
			if multiplier, castOK := cached.(float64); castOK {
				groupRateMetrics.Hit.Add(1)
				return multiplier
			}
		}
	}
	if r.repo == nil {
		return groupDefaultMultiplier
	}
	groupRateMetrics.Miss.Add(1)

	value, err, shared := r.sf.Do(key, func() (any, error) {
		if r.cache != nil {
			if cached, ok := r.cache.Get(key); ok {
				if multiplier, castOK := cached.(float64); castOK {
					groupRateMetrics.Hit.Add(1)
					return multiplier, nil
				}
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
		if r.cache != nil {
			r.cache.Set(key, multiplier, r.cacheTTL)
		}
		return multiplier, nil
	})
	if shared {
		groupRateMetrics.Shared.Add(1)
	}
	if err != nil {
		groupRateMetrics.Fallback.Add(1)
		r.observe(r.logComponent, "get user group rate failed, fallback to group default: user=%d group=%d err=%v", userID, groupID, err)
		return groupDefaultMultiplier
	}

	multiplier, ok := value.(float64)
	if !ok {
		groupRateMetrics.Fallback.Add(1)
		return groupDefaultMultiplier
	}
	return multiplier
}

// DefaultGroupRateCacheTTL 保留网关隔离缓存的原有效期。
const defaultUserGroupRateCacheTTL = 30 * time.Second
const DefaultGroupRateCacheTTL = defaultUserGroupRateCacheTTL

// GroupRateMetrics 共用一份统计状态；各网关的缓存和 singleflight 保持隔离。
type GroupRateMetrics struct{ Hit, Miss, Load, Shared, Fallback atomic.Int64 }

var groupRateMetrics GroupRateMetrics

func SharedGroupRateMetrics() *GroupRateMetrics { return &groupRateMetrics }
func (r *GroupRateResolver) DeleteExpired() {
	if r != nil && r.cache != nil {
		r.cache.DeleteExpired()
	}
}
func GroupRateCacheStats() (int64, int64, int64, int64, int64) {
	return groupRateMetrics.Hit.Load(), groupRateMetrics.Miss.Load(), groupRateMetrics.Load.Load(), groupRateMetrics.Shared.Load(), groupRateMetrics.Fallback.Load()
}
