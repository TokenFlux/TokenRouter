package billing

import (
	"context"
	"sync/atomic"
	"time"
)

// SettlementEffectInput 只包含已提交资金事实与缓存标识，不接收请求或账号实体。
type SettlementEffectInput struct {
	UserID, KeyID             int64
	HasUser, HasKeyRateLimits bool
	Platform                  string
	Cost                      *CostBreakdown
	Result                    *UsageBillingApplyResult
}

// SettlementEffects 是提交后调用的无状态端口集合，队列与用户锁仍由唯一 Eligibility 持有。
type SettlementEffects struct {
	Cache                        *Eligibility
	Quotas                       UserPlatformQuotaRepository
	FlusherEnabled               bool
	Background                   func(string, func()) bool
	Observe                      Observe
	BalanceWarning               func(int64, float64, error)
	AccountUsed                  func()
	NotifyBalance, NotifyAccount func()
}

var platformQuotaDBIncrErrors atomic.Int64

func PlatformQuotaDBIncrErrors() *atomic.Int64 { return &platformQuotaDBIncrErrors }

// Finalize 保留缓存、Key 窗口、账号完成、平台额度和通知的原顺序。
func (e SettlementEffects) Finalize(input SettlementEffectInput) {
	if input.Cost == nil {
		return
	}
	if input.Result != nil && input.Result.BalanceAmountUSD > 0 && input.HasUser {
		e.SyncBalance(context.Background(), input.UserID, input.Result)
	}
	rateCost := input.Cost.ActualCost
	if input.Result != nil {
		rateCost = input.Result.SubscriptionAmountUSD + input.Result.BalanceAmountUSD
	}
	if rateCost > 0 && input.HasKeyRateLimits {
		e.Cache.QueueUpdateAPIKeyRateLimitUsage(input.KeyID, rateCost)
	}
	if e.AccountUsed != nil {
		e.AccountUsed()
	}
	balanceCost := input.Cost.ActualCost
	if input.Result != nil {
		balanceCost = input.Result.BalanceAmountUSD
	}
	if IsAllowedQuotaPlatform(input.Platform) && balanceCost > 0 && input.HasUser && e.Quotas != nil && e.Cache != nil {
		e.recordPlatformUsage(input.UserID, input.Platform, balanceCost)
	}
	if e.NotifyBalance != nil {
		e.launch("billing/settlement:notifyBalance", e.NotifyBalance)
	}
	if e.NotifyAccount != nil {
		e.launch("billing/settlement:notifyAccount", e.NotifyAccount)
	}
}
func (e SettlementEffects) launch(name string, fn func()) bool {
	if e.Background != nil {
		return e.Background(name, fn)
	}
	fn()
	return true
}

// SyncBalance 低于准入阈值时失效，其余情况保持原异步扣减。
func (e SettlementEffects) SyncBalance(ctx context.Context, id int64, result *UsageBillingApplyResult) {
	if e.Cache == nil || result == nil || result.BalanceAmountUSD <= 0 {
		return
	}
	if result.NewBalance != nil && e.Cache.BalanceBelowEligibilityThreshold(*result.NewBalance) {
		if err := e.Cache.InvalidateUserBalance(ctx, id); err != nil && e.BalanceWarning != nil {
			e.BalanceWarning(id, *result.NewBalance, err)
		}
		return
	}
	e.Cache.QueueDeductBalance(id, result.BalanceAmountUSD)
}

// recordPlatformUsage 在锁内重新读取守卫；异步镜像完成前不让重置越过该次累计。
// Redis 与数据库仍非原子提交，故障时保留原告警及缓存滞后语义。
func (e SettlementEffects) recordPlatformUsage(userID int64, platform string, cost float64) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	unlock, err := e.Cache.coordinator.Acquire(ctx, userID)
	if err != nil {
		e.Observe.Printf("service.billing_cache", "ALERT: incr user platform quota lock failed user=%d platform=%s cost=%f: %v", userID, platform, cost, err)
		return
	}
	handedOff := false
	defer func() {
		if !handedOff {
			unlock()
		}
	}()
	if e.Cache.options().RunMode == RunModeSimple {
		return
	}
	if !e.Cache.prepareQuotaIncrement(ctx, e.Quotas, userID, platform) {
		return
	}
	incrementCtx, incrementCancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer incrementCancel()
	if e.Cache.cache != nil {
		ttl := time.Duration(e.Cache.options().Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
		if err := e.Cache.cache.IncrUserPlatformQuotaUsageCache(incrementCtx, userID, platform, cost, ttl, e.FlusherEnabled); err != nil {
			e.Observe.Printf("service.billing_cache", "ALERT: incr user platform quota cache failed user=%d platform=%s cost=%f: %v", userID, platform, cost, err)
		}
	}
	if e.FlusherEnabled {
		return
	}
	handedOff = e.launch("billing/settlement:quotaMirror", func() {
		defer unlock()
		defer func() {
			if r := recover(); r != nil {
				e.Observe.Printf("service.gateway", "ALERT: panic in user platform quota incr goroutine user=%d platform=%s: %v", userID, platform, r)
			}
		}()
		if err := e.Quotas.IncrementUsageWithReset(context.Background(), userID, platform, cost, time.Now().UTC()); err != nil {
			platformQuotaDBIncrErrors.Add(1)
			e.Observe.Printf("service.gateway", "ALERT: incr user platform quota DB failed user=%d platform=%s cost=%f: %v", userID, platform, cost, err)
		}
	})
	if !handedOff {
		e.Observe.Printf("service.gateway", "ALERT: user platform quota DB mirror rejected after shutdown user=%d platform=%s cost=%f", userID, platform, cost)
	}
}

// BalanceBeforeSettlement 优先使用事务返回值还原扣费前余额。
func BalanceBeforeSettlement(snapshot float64, result *UsageBillingApplyResult) float64 {
	if result != nil && result.NewBalance != nil {
		return *result.NewBalance + result.BalanceAmountUSD
	}
	return snapshot
}
