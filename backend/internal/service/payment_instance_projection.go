// 兼容投影不附加 Ent 客户端或缓存，S15/S16 清理。
package service

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func paymentInstance(v *dbent.PaymentProviderInstance) *payment.ProviderInstance {
	if v == nil {
		return nil
	}
	return &payment.ProviderInstance{
		ID:              v.ID,
		ProviderKey:     v.ProviderKey,
		Name:            v.Name,
		Config:          v.Config,
		SupportedTypes:  v.SupportedTypes,
		Enabled:         v.Enabled,
		PaymentMode:     v.PaymentMode,
		SortOrder:       v.SortOrder,
		Limits:          v.Limits,
		RefundEnabled:   v.RefundEnabled,
		AllowUserRefund: v.AllowUserRefund,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}
func paymentInstanceToEnt(v *payment.ProviderInstance) *dbent.PaymentProviderInstance {
	if v == nil {
		return nil
	}
	return &dbent.PaymentProviderInstance{
		ID:              v.ID,
		ProviderKey:     v.ProviderKey,
		Name:            v.Name,
		Config:          v.Config,
		SupportedTypes:  v.SupportedTypes,
		Enabled:         v.Enabled,
		PaymentMode:     v.PaymentMode,
		SortOrder:       v.SortOrder,
		Limits:          v.Limits,
		RefundEnabled:   v.RefundEnabled,
		AllowUserRefund: v.AllowUserRefund,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}
func paymentInstances(v []*dbent.PaymentProviderInstance) []*payment.ProviderInstance {
	if v == nil {
		return nil
	}
	out := make([]*payment.ProviderInstance, len(v))
	for i, x := range v {
		out[i] = paymentInstance(x)
	}
	return out
}
func paymentInstancesToEnt(v []*payment.ProviderInstance) []*dbent.PaymentProviderInstance {
	if v == nil {
		return nil
	}
	out := make([]*dbent.PaymentProviderInstance, len(v))
	for i, x := range v {
		out[i] = paymentInstanceToEnt(x)
	}
	return out
}

func paymentInstanceGroupsToEnt(v map[string][]*payment.ProviderInstance) map[string][]*dbent.PaymentProviderInstance {
	if v == nil {
		return nil
	}
	out := make(map[string][]*dbent.PaymentProviderInstance, len(v))
	for k, x := range v {
		out[k] = paymentInstancesToEnt(x)
	}
	return out
}
