package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	gocache "github.com/patrickmn/go-cache"
	"golang.org/x/sync/singleflight"
	"time"
)

// userGroupRateResolver 兼容旧网关类型，各入口复用原缓存与 singleflight。
type userGroupRateResolver = billing.GroupRateResolver

func newUserGroupRateResolver(repo UserGroupRateRepository, cache *gocache.Cache, ttl time.Duration, sf *singleflight.Group, component string) *userGroupRateResolver {
	return billing.NewGroupRateResolver(repo, cache, ttl, sf, component, logger.LegacyPrintf)
}
