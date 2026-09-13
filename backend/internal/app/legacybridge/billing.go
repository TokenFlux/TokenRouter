// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package legacybridge

import (
	context "context"
	sql "database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// RedeemAffiliate 保留旧返利能力的调用与结果投影，S12 退出。
type RedeemAffiliate struct{ Service *service.AffiliateService }

func (b RedeemAffiliate) IsEnabled(ctx context.Context) bool {
	return b.Service != nil && b.Service.IsEnabled(ctx)
}
func (b RedeemAffiliate) AccrueInviteRebate(ctx context.Context, id int64, amount float64) (float64, error) {
	return b.Service.AccrueInviteRebate(ctx, id, amount)
}

// PlanOrders 只转接未迁支付订单的阻塞数量，S12 退出。
type PlanOrders struct{ Client *dbent.Client }

func (p PlanOrders) CountInProgressByPlan(ctx context.Context, id int64) (int, error) {
	return service.CountPendingPlanOrders(ctx, p.Client, id)
}

// ExpiryNotifications 仅投影已确定的提醒事实，通知模板及传输在 S10 迁移。
type ExpiryNotifications struct {
	Service *service.NotificationEmailService
}

func (n ExpiryNotifications) Ready(ctx context.Context) error {
	return (service.LegacyExpiryNotifier{Service: n.Service}).Ready(ctx)
}
func (n ExpiryNotifications) Send(ctx context.Context, r billing.ExpiryReminder) error {
	return (service.LegacyExpiryNotifier{Service: n.Service}).Send(ctx, r)
}
func BillingMaintenanceLease(ctx context.Context, cache service.LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	return service.AcquireLegacySingletonLease(ctx, cache, db, key, owner, ttl)
}
