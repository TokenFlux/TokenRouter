// 订单存储边界只转换快照值，不导出 ORM 客户端。
package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func OrderFromEntity(v *dbent.PaymentOrder) *payment.Order {
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
