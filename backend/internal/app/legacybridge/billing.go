// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package legacybridge

import (
	context "context"
	sql "database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
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

func BillingMaintenanceLease(ctx context.Context, cache service.LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	return service.AcquireLegacySingletonLease(ctx, cache, db, key, owner, ttl)
}
