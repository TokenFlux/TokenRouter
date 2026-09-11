//go:build integration

// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
)

type redeemCache = billingredis.RedeemCache

type redeemCodeRepository = billingpostgres.RedeemStore

type userSubscriptionRepository = billingpostgres.SubscriptionStore
