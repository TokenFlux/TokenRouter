// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package legacybridge

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// BillingGroups 只读取套餐展示所需名称，S06 退出。
type BillingGroups struct{ Repository service.GroupRepository }

func (b BillingGroups) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if b.Repository == nil {
		return nil, nil
	}
	g, err := b.Repository.GetByIDLite(ctx, id)
	if err != nil || g == nil {
		return nil, err
	}
	return &billing.SubscriptionPlanGroup{ID: g.ID, Name: g.Name}, nil
}

// BillingAccountQuotaOutbox 同事务调用现有账号 outbox，不持有业务规则；S07 改绑。
func BillingAccountQuotaOutbox(ctx context.Context, tx *sql.Tx, id int64) error {
	return repository.EnqueueAccountQuotaChangedInTx(ctx, tx, id)
}

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
