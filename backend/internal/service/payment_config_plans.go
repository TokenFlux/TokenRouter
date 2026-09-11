// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

func (s *PaymentConfigService) ListPlans(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	return s.billingPlans().ListPlans(ctx)
}

func (s *PaymentConfigService) ListPlansForSale(ctx context.Context) ([]*billing.SubscriptionPlan, error) {
	return s.billingPlans().ListPlansForSale(ctx)
}

func (s *PaymentConfigService) CreatePlan(ctx context.Context, req CreatePlanRequest) (*billing.SubscriptionPlan, error) {
	return s.billingPlans().CreatePlan(ctx, req)
}

func (s *PaymentConfigService) UpdatePlan(ctx context.Context, id int64, req UpdatePlanRequest) (*billing.SubscriptionPlan, error) {
	return s.billingPlans().UpdatePlan(ctx, id, req)
}

func (s *PaymentConfigService) DeletePlan(ctx context.Context, id int64) error {
	return s.billingPlans().DeletePlan(ctx, id)
}

func (s *PaymentConfigService) GetPlan(ctx context.Context, id int64) (*billing.SubscriptionPlan, error) {
	return s.billingPlans().GetPlan(ctx, id)
}
