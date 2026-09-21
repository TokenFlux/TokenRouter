//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
	"github.com/google/wire"
)

// cacheProviders 直接绑定唯一缓存实现，旧调用方继续共享同一个周期任务锁实例。
var cacheProviders = wire.NewSet(
	rediscache.NewInternal500CounterCache,
	redisinfra.NewLeaderLockCache,
	wire.Bind(new(account.CNMonitorLeader), new(*redisinfra.LeaderLock)),
)
