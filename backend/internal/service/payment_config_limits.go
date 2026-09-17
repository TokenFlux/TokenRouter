// 旧配置入口只投影与委托；唯一规则在 payment，S15/S16 清理。
package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentConfigService) GetAvailableMethodLimits(ctx context.Context) (*MethodLimitsResponse, error) {
	return s.paymentCoreConfig().GetAvailableMethodLimits(ctx)
}

func (s *PaymentConfigService) GetMethodLimits(ctx context.Context, types []string) ([]MethodLimits, error) {
	return s.paymentCoreConfig().GetMethodLimits(ctx, types)
}

func (s *PaymentConfigService) ValidateMethodCurrencyConsistency(ctx context.Context, paymentType string) (string, error) {
	return s.paymentCoreConfig().ValidateMethodCurrencyConsistency(ctx, paymentType)
}

func (s *PaymentConfigService) pcAggregateMethodCurrency(instances []*dbent.PaymentProviderInstance) (string, bool) {
	return s.paymentCoreConfig().ConfigPcAggregateMethodCurrency(paymentInstances(instances))
}

func pcGroupByPaymentType(instances []*dbent.PaymentProviderInstance) map[string][]*dbent.PaymentProviderInstance {
	return paymentInstanceGroupsToEnt(payment.ConfigPcGroupByPaymentType(paymentInstances(instances)))
}

func pcInstanceTypeLimits(inst *dbent.PaymentProviderInstance, pt string) (payment.ChannelLimits, bool) {
	return payment.ConfigPcInstanceTypeLimits(paymentInstance(inst), pt)
}

func unionFloat(agg float64, limited bool, val float64, wantMin bool) (float64, bool) {
	return payment.ConfigUnionFloat(agg, limited, val, wantMin)
}

func pcAggregateMethodLimits(pt string, instances []*dbent.PaymentProviderInstance) MethodLimits {
	return payment.ConfigPcAggregateMethodLimits(pt, paymentInstances(instances))
}

func pcComputeGlobalRange(methods map[string]MethodLimits) (globalMin, globalMax float64) {
	return payment.ConfigPcComputeGlobalRange(methods)
}
