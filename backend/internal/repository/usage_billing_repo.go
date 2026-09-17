// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	context "context"
	sql "database/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchpostgres "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativepostgres "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type usageBillingRepository struct {
	inner *billingpostgres.SettlementStore
	funds *billing.Funds
}

// NewUsageBillingRepository 保留旧 Wire 入口，S04 装配更新后由 app 提供新实例。
func NewUsageBillingRepository(_ *dbent.Client, db *sql.DB) service.UsageBillingRepository {
	store := billingpostgres.NewSettlementStore(db, EnqueueAccountQuotaChangedInTx, billingpostgres.TaskProjectionFactories{
		creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return creativepostgres.NewFundingParticipant(tx, ref.ID)
		},
		batchimage.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return batchpostgres.NewFundingParticipant(tx, ref.ID)
		},
	})
	return NewUsageBillingAdapter(store, billing.NewFunds(store))
}
func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	return r.funds.Settle(ctx, cmd)
}
func (r *usageBillingRepository) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	return r.inner.ResolveUsableSubscriptionForGroup(ctx, userID, groupID)
}
func (r *usageBillingRepository) ResolvePreferredSubscriptionForGroup(ctx context.Context, userID, subscriptionID, groupID int64) (*service.UserSubscription, error) {
	return r.inner.ResolvePreferredSubscriptionForGroup(ctx, userID, subscriptionID, groupID)
}

func (r *usageBillingRepository) ReserveBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	cmd.Normalize()
	current := cmd.BillingCommand()
	result, err := r.funds.Reserve(ctx, current)
	if err == nil && cmd != nil {
		cmd.UpdateBillingCommand(current)
	}
	return result, err
}

func (r *usageBillingRepository) CaptureBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	cmd.Normalize()
	current := cmd.BillingCommand()
	result, err := r.funds.Capture(ctx, current)
	if err == nil && cmd != nil {
		cmd.UpdateBillingCommand(current)
	}
	return result, err
}

func (r *usageBillingRepository) ReleaseBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	cmd.Normalize()
	current := cmd.BillingCommand()
	result, err := r.funds.Release(ctx, current)
	if err == nil && cmd != nil {
		cmd.UpdateBillingCommand(current)
	}
	return result, err
}

// NewUsageBillingAdapter 仅保留旧命令投影，复用 app 持有的唯一资金入口。
func NewUsageBillingAdapter(store *billingpostgres.SettlementStore, funds *billing.Funds) service.UsageBillingRepository {
	return &usageBillingRepository{inner: store, funds: funds}
}

// BillingFunds 向已迁任务交还 app 的唯一资金入口，避免再经过旧任务类型转换。
func (r *usageBillingRepository) BillingFunds() *billing.Funds { return r.funds }
