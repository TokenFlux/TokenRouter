// 旧支付调用只恢复原标量形状；完整 ORM 操作由 payment/postgres 拥有。
package service

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func paymentOrderValue(v *dbent.PaymentOrder) *payment.Order {
	if v == nil {
		return nil
	}
	value := &payment.Order{
		ID:                   v.ID,
		UserID:               v.UserID,
		UserEmail:            v.UserEmail,
		UserName:             v.UserName,
		UserNotes:            v.UserNotes,
		Amount:               v.Amount,
		PayAmount:            v.PayAmount,
		FeeRate:              v.FeeRate,
		FeeFixed:             v.FeeFixed,
		FeeRateAmount:        v.FeeRateAmount,
		FeeAmount:            v.FeeAmount,
		RechargeCode:         v.RechargeCode,
		OutTradeNo:           v.OutTradeNo,
		PaymentType:          v.PaymentType,
		PaymentTradeNo:       v.PaymentTradeNo,
		PayURL:               v.PayURL,
		QrCode:               v.QrCode,
		QrCodeImg:            v.QrCodeImg,
		PaymentCustomerID:    v.PaymentCustomerID,
		PaymentInvoiceID:     v.PaymentInvoiceID,
		PaymentInvoiceURL:    v.PaymentInvoiceURL,
		PaymentInvoicePdfURL: v.PaymentInvoicePdfURL,
		PaymentInvoiceStatus: v.PaymentInvoiceStatus,
		BillingSnapshot:      v.BillingSnapshot,
		OrderType:            v.OrderType,
		PlanID:               v.PlanID,
		PlanSnapshot:         v.PlanSnapshot,
		ProviderInstanceID:   v.ProviderInstanceID,
		ProviderKey:          v.ProviderKey,
		ProviderSnapshot:     v.ProviderSnapshot,
		Status:               v.Status,
		RefundAmount:         v.RefundAmount,
		RefundReason:         v.RefundReason,
		RefundAt:             v.RefundAt,
		ForceRefund:          v.ForceRefund,
		RefundRequestedAt:    v.RefundRequestedAt,
		RefundRequestReason:  v.RefundRequestReason,
		RefundRequestedBy:    v.RefundRequestedBy,
		ExpiresAt:            v.ExpiresAt,
		PaidAt:               v.PaidAt,
		CompletedAt:          v.CompletedAt,
		FailedAt:             v.FailedAt,
		FailedReason:         v.FailedReason,
		ClientIP:             v.ClientIP,
		SrcHost:              v.SrcHost,
		SrcURL:               v.SrcURL,
		CreatedAt:            v.CreatedAt,
		UpdatedAt:            v.UpdatedAt,
	}
	return value.Clone()
}
func paymentOrderEntity(v *payment.Order) *dbent.PaymentOrder {
	v = v.Clone()
	if v == nil {
		return nil
	}
	return &dbent.PaymentOrder{
		ID:                   v.ID,
		UserID:               v.UserID,
		UserEmail:            v.UserEmail,
		UserName:             v.UserName,
		UserNotes:            v.UserNotes,
		Amount:               v.Amount,
		PayAmount:            v.PayAmount,
		FeeRate:              v.FeeRate,
		FeeFixed:             v.FeeFixed,
		FeeRateAmount:        v.FeeRateAmount,
		FeeAmount:            v.FeeAmount,
		RechargeCode:         v.RechargeCode,
		OutTradeNo:           v.OutTradeNo,
		PaymentType:          v.PaymentType,
		PaymentTradeNo:       v.PaymentTradeNo,
		PayURL:               v.PayURL,
		QrCode:               v.QrCode,
		QrCodeImg:            v.QrCodeImg,
		PaymentCustomerID:    v.PaymentCustomerID,
		PaymentInvoiceID:     v.PaymentInvoiceID,
		PaymentInvoiceURL:    v.PaymentInvoiceURL,
		PaymentInvoicePdfURL: v.PaymentInvoicePdfURL,
		PaymentInvoiceStatus: v.PaymentInvoiceStatus,
		BillingSnapshot:      v.BillingSnapshot,
		OrderType:            v.OrderType,
		PlanID:               v.PlanID,
		PlanSnapshot:         v.PlanSnapshot,
		ProviderInstanceID:   v.ProviderInstanceID,
		ProviderKey:          v.ProviderKey,
		ProviderSnapshot:     v.ProviderSnapshot,
		Status:               v.Status,
		RefundAmount:         v.RefundAmount,
		RefundReason:         v.RefundReason,
		RefundAt:             v.RefundAt,
		ForceRefund:          v.ForceRefund,
		RefundRequestedAt:    v.RefundRequestedAt,
		RefundRequestReason:  v.RefundRequestReason,
		RefundRequestedBy:    v.RefundRequestedBy,
		ExpiresAt:            v.ExpiresAt,
		PaidAt:               v.PaidAt,
		CompletedAt:          v.CompletedAt,
		FailedAt:             v.FailedAt,
		FailedReason:         v.FailedReason,
		ClientIP:             v.ClientIP,
		SrcHost:              v.SrcHost,
		SrcURL:               v.SrcURL,
		CreatedAt:            v.CreatedAt,
		UpdatedAt:            v.UpdatedAt,
	}
}
func paymentOrderEntities(rows []*payment.Order) []*dbent.PaymentOrder {
	if rows == nil {
		return nil
	}
	out := make([]*dbent.PaymentOrder, len(rows))
	for i, v := range rows {
		out[i] = paymentOrderEntity(v)
	}
	return out
}

// PaymentOrderSnapshot 仅供旧 HTTP DTO 转接，新生产 HTTP 直接接收 payment.Order。
func PaymentOrderSnapshot(v *dbent.PaymentOrder) *payment.Order      { return paymentOrderValue(v) }
func PaymentOrderSnapshots(v []*dbent.PaymentOrder) []*payment.Order { return paymentOrderValues(v) }
