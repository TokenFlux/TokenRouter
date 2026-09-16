package completion

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

type AccountActivity interface{ ScheduleLastUsedUpdate(int64) }
type AuthInvalidator interface{ InvalidateAuthCacheByKey(context.Context, string) }

// CommitEffects 只安排已提交资金的缓存和通知，保留 billing 的唯一副作用顺序。
type CommitEffects struct {
	Funds         billing.SettlementEffects
	Activity      AccountActivity
	Auth          AuthInvalidator
	Notifications *billing.BalanceNotifyService
	Observe       func(string, string)
}

func (e *CommitEffects) AccountUsed(id int64) { e.Activity.ScheduleLastUsedUpdate(id) }
func (e *CommitEffects) InvalidateAuth(ctx context.Context, key string) {
	if e.Auth != nil {
		e.Auth.InvalidateAuthCacheByKey(ctx, key)
	}
}

// @project-doc docs/domains/platform_quotas.md#platform_quota_settlement_and_flush
func (e *CommitEffects) Settled(p SettlementInput, result *billing.UsageBillingApplyResult) {
	effects := e.Funds
	effects.AccountUsed = func() { e.AccountUsed(p.Account.ID) }
	effects.NotifyBalance = func() { e.NotifyBalance(p, result) }
	effects.NotifyAccount = func() { e.NotifyAccount(p, result) }
	in := billing.SettlementEffectInput{Cost: p.Cost, Result: result, Platform: p.Platform, HasUser: p.User != nil}
	if p.User != nil {
		in.UserID = p.User.ID
	}
	if p.APIKey != nil {
		in.KeyID = p.APIKey.ID
		in.HasKeyRateLimits = p.APIKey.HasRateLimits
	}
	effects.Finalize(in)
}
func (e *CommitEffects) recoverNotification(name string) {
	if v := recover(); v != nil && e.Observe != nil {
		e.Observe(name, fmt.Sprint(v))
	}
}

// NotifyBalance 保留余额阈值通知在提交后的原边界。
func (e *CommitEffects) NotifyBalance(p SettlementInput, result *billing.UsageBillingApplyResult) {
	defer e.recoverNotification("notifyBalanceLow")
	if result == nil || result.BalanceAmountUSD <= 0 || p.User == nil || e.Notifications == nil {
		return
	}
	e.Notifications.CheckBalanceAfterDeduction(context.Background(), p.User.Notification, billing.BalanceBeforeSettlement(p.User.Balance, result), result.BalanceAmountUSD)

}

// NotifyAccount 保留账号通知的成本口径，优先使用事务返回额度状态。
func (e *CommitEffects) NotifyAccount(p SettlementInput, result *billing.UsageBillingApplyResult) {
	defer e.recoverNotification("notifyAccountQuota")
	if p.Cost.TotalCost <= 0 || p.Account == nil || !p.Account.QuotaEligible || e.Notifications == nil {
		return
	}
	var state *billing.AccountQuotaState
	if result != nil {
		state = result.QuotaState
	}
	e.Notifications.CheckAccountQuotaAfterIncrement(context.Background(), p.Account.Notification, p.Cost.TotalCost*p.AccountRateMultiplier, state)

}
