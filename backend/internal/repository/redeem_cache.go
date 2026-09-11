// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	redis "github.com/redis/go-redis/v9"
)

// NewRedeemCache 委托唯一 Redis 实现，保留旧装配入口。
func NewRedeemCache(rdb *redis.Client) service.RedeemCache { return billingredis.NewRedeemCache(rdb) }
