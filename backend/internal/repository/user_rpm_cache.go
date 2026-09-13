// RPM Redis 实现由 scheduler 拥有；旧构造只委托。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewUserRPMCache(rdb *redis.Client) scheduler.UserRPMCache {
	return schedulerredis.NewUserRPMCache(rdb)
}
