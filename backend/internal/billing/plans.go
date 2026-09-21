// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	context "context"
	fmt "fmt"

	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type PlanRepository interface {
	ListPlans(context.Context) ([]*SubscriptionPlan, error)
	ListPlansForSale(context.Context) ([]*SubscriptionPlan, error)
	CreatePlan(context.Context, CreatePlanRequest) (*SubscriptionPlan, error)
	UpdatePlan(context.Context, int64, UpdatePlanRequest) (*SubscriptionPlan, error)
	DeletePlan(context.Context, int64) error
	GetPlan(context.Context, int64) (*SubscriptionPlan, error)
}

// PlanOrders 只读取阻止删除的订单数量，支付状态归属仍在 S12。
type PlanOrders interface {
	CountInProgressByPlan(context.Context, int64) (int, error)
}
type Plans struct {
	store  PlanRepository
	orders PlanOrders
}

func NewPlans(store PlanRepository, orders PlanOrders) *Plans {
	return &Plans{store: store, orders: orders}
}
func (s *Plans) ListPlans(ctx context.Context) ([]*SubscriptionPlan, error) {
	return s.store.ListPlans(ctx)
}
func (s *Plans) ListPlansForSale(ctx context.Context) ([]*SubscriptionPlan, error) {
	return s.store.ListPlansForSale(ctx)
}
func (s *Plans) GetPlan(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	return s.store.GetPlan(ctx, id)
}
func (s *Plans) CreatePlan(ctx context.Context, req CreatePlanRequest) (*SubscriptionPlan, error) {
	groupIDs := NormalizePlanGroupIDs(req.GroupID, req.GroupIDs)
	groupRates, err := NormalizePlanGroupRateMultipliers(groupIDs, req.GroupRateMultipliers)
	if err != nil {
		return nil, err
	}
	if err := ValidatePlanRequired(req.Name, req.Price, req.ValidityDays, req.ValidityUnit, req.OriginalPrice); err != nil {
		return nil, err
	}
	currency, err := NormalizePlanCurrency(req.Currency)
	if err != nil {
		return nil, err
	}
	if err := ValidatePlanQuotas(req.DailyLimitUSD, req.WeeklyLimitUSD, req.MonthlyLimitUSD); err != nil {
		return nil, err
	}

	req.GroupID = 0
	req.GroupIDs = groupIDs
	req.GroupRateMultipliers = groupRates
	req.Currency = currency
	return s.store.CreatePlan(ctx, req)
}
func (s *Plans) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*SubscriptionPlan, error) {
	if err := ValidatePlanPatch(req); err != nil {
		return nil, err
	}
	return s.store.UpdatePlan(ctx, id, req)
}
func (s *Plans) DeletePlan(ctx context.Context, id int64) error {
	count, err := s.orders.CountInProgressByPlan(ctx, id)
	if err != nil {
		return fmt.Errorf("check pending orders: %w", err)
	}
	if count > 0 {
		return apperror.Conflict("PENDING_ORDERS", fmt.Sprintf("this plan has %d in-progress orders and cannot be deleted — wait for orders to complete first", count))
	}
	return s.store.DeletePlan(ctx, id)
}
