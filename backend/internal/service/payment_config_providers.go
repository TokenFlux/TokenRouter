// 旧配置入口只投影与委托；唯一规则在 payment，S15/S16 清理。
package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

func (s *PaymentConfigService) ListProviderInstances(ctx context.Context) ([]*dbent.PaymentProviderInstance, error) {
	v, e := s.paymentCoreConfig().ListProviderInstances(ctx)
	return paymentInstancesToEnt(v), e
}

type ProviderInstanceResponse = payment.ProviderInstanceResponse

func (s *PaymentConfigService) ListProviderInstancesWithConfig(ctx context.Context) ([]ProviderInstanceResponse, error) {
	return s.paymentCoreConfig().ListProviderInstancesWithConfig(ctx)
}

var providerSensitiveConfigFields = payment.ConfigProviderSensitiveConfigFields

func CountPendingPlanOrders(ctx context.Context, client *dbent.Client, planID int64) (int, error) {
	return paymentpostgres.NewInstanceStore(client).CountInProgressByPlan(ctx, planID)
}

func (s *PaymentConfigService) CreateProviderInstance(ctx context.Context, req CreateProviderInstanceRequest) (*dbent.PaymentProviderInstance, error) {
	v, e := s.paymentCoreConfig().CreateProviderInstance(ctx, req)
	return paymentInstanceToEnt(v), e
}

func (s *PaymentConfigService) TestProviderDraft(ctx context.Context, req TestProviderDraftRequest) (*ProviderDraftTestResult, error) {
	return s.paymentCoreConfig().TestProviderDraft(ctx, req)
}

func (s *PaymentConfigService) UpdateProviderInstance(ctx context.Context, id int64, req UpdateProviderInstanceRequest) (*dbent.PaymentProviderInstance, error) {
	v, e := s.paymentCoreConfig().UpdateProviderInstance(ctx, id, req)
	return paymentInstanceToEnt(v), e
}

func (s *PaymentConfigService) GetUserRefundEligibleInstanceIDs(ctx context.Context) ([]string, error) {
	return s.paymentCoreConfig().GetUserRefundEligibleInstanceIDs(ctx)
}

func (s *PaymentConfigService) DeleteProviderInstance(ctx context.Context, id int64) error {
	return s.paymentCoreConfig().DeleteProviderInstance(ctx, id)
}
