// Order 是订单快照值，不携带 Ent 客户端、关系实体或事务。
package payment

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

type Order struct {
	ID                   int64                            `json:"id,omitempty"`
	UserID               int64                            `json:"user_id,omitempty"`
	UserEmail            string                           `json:"user_email,omitempty"`
	UserName             string                           `json:"user_name,omitempty"`
	UserNotes            *string                          `json:"user_notes,omitempty"`
	Amount               float64                          `json:"amount,omitempty"`
	PayAmount            float64                          `json:"pay_amount,omitempty"`
	FeeRate              float64                          `json:"fee_rate,omitempty"`
	FeeFixed             float64                          `json:"fee_fixed,omitempty"`
	FeeRateAmount        float64                          `json:"fee_rate_amount,omitempty"`
	FeeAmount            float64                          `json:"fee_amount,omitempty"`
	RechargeCode         string                           `json:"recharge_code,omitempty"`
	OutTradeNo           string                           `json:"out_trade_no,omitempty"`
	PaymentType          string                           `json:"payment_type,omitempty"`
	PaymentTradeNo       string                           `json:"payment_trade_no,omitempty"`
	PayURL               *string                          `json:"pay_url,omitempty"`
	QrCode               *string                          `json:"qr_code,omitempty"`
	QrCodeImg            *string                          `json:"qr_code_img,omitempty"`
	PaymentCustomerID    *string                          `json:"payment_customer_id,omitempty"`
	PaymentInvoiceID     *string                          `json:"payment_invoice_id,omitempty"`
	PaymentInvoiceURL    *string                          `json:"payment_invoice_url,omitempty"`
	PaymentInvoicePdfURL *string                          `json:"payment_invoice_pdf_url,omitempty"`
	PaymentInvoiceStatus *string                          `json:"payment_invoice_status,omitempty"`
	BillingSnapshot      map[string]any                   `json:"billing_snapshot,omitempty"`
	OrderType            string                           `json:"order_type,omitempty"`
	PlanID               *int64                           `json:"plan_id,omitempty"`
	PlanSnapshot         billing.SubscriptionPlanSnapshot `json:"plan_snapshot,omitempty"`
	ProviderInstanceID   *string                          `json:"provider_instance_id,omitempty"`
	ProviderKey          *string                          `json:"provider_key,omitempty"`
	ProviderSnapshot     map[string]any                   `json:"provider_snapshot,omitempty"`
	Status               string                           `json:"status,omitempty"`
	RefundAmount         float64                          `json:"refund_amount,omitempty"`
	RefundReason         *string                          `json:"refund_reason,omitempty"`
	RefundAt             *time.Time                       `json:"refund_at,omitempty"`
	ForceRefund          bool                             `json:"force_refund,omitempty"`
	RefundRequestedAt    *time.Time                       `json:"refund_requested_at,omitempty"`
	RefundRequestReason  *string                          `json:"refund_request_reason,omitempty"`
	RefundRequestedBy    *string                          `json:"refund_requested_by,omitempty"`
	ExpiresAt            time.Time                        `json:"expires_at,omitempty"`
	PaidAt               *time.Time                       `json:"paid_at,omitempty"`
	CompletedAt          *time.Time                       `json:"completed_at,omitempty"`
	FailedAt             *time.Time                       `json:"failed_at,omitempty"`
	FailedReason         *string                          `json:"failed_reason,omitempty"`
	ClientIP             string                           `json:"client_ip,omitempty"`
	SrcHost              string                           `json:"src_host,omitempty"`
	SrcURL               *string                          `json:"src_url,omitempty"`
	CreatedAt            time.Time                        `json:"created_at,omitempty"`
	UpdatedAt            time.Time                        `json:"updated_at,omitempty"`
	// 原订单查询不加载用户关系，HTTP 保留空 edges 字段。
	Edges struct{} `json:"edges"`
}

// Clone 为请求和旧形状转换提供独立副本，保持 JSON 数字与空集合类型。
func (o *Order) Clone() *Order {
	if o == nil {
		return nil
	}
	copy := *o
	if o.UserNotes != nil {
		value := *o.UserNotes
		copy.UserNotes = &value
	}
	if o.PayURL != nil {
		value := *o.PayURL
		copy.PayURL = &value
	}
	if o.QrCode != nil {
		value := *o.QrCode
		copy.QrCode = &value
	}
	if o.QrCodeImg != nil {
		value := *o.QrCodeImg
		copy.QrCodeImg = &value
	}
	if o.PaymentCustomerID != nil {
		value := *o.PaymentCustomerID
		copy.PaymentCustomerID = &value
	}
	if o.PaymentInvoiceID != nil {
		value := *o.PaymentInvoiceID
		copy.PaymentInvoiceID = &value
	}
	if o.PaymentInvoiceURL != nil {
		value := *o.PaymentInvoiceURL
		copy.PaymentInvoiceURL = &value
	}
	if o.PaymentInvoicePdfURL != nil {
		value := *o.PaymentInvoicePdfURL
		copy.PaymentInvoicePdfURL = &value
	}
	if o.PaymentInvoiceStatus != nil {
		value := *o.PaymentInvoiceStatus
		copy.PaymentInvoiceStatus = &value
	}
	if o.PlanID != nil {
		value := *o.PlanID
		copy.PlanID = &value
	}
	if o.ProviderInstanceID != nil {
		value := *o.ProviderInstanceID
		copy.ProviderInstanceID = &value
	}
	if o.ProviderKey != nil {
		value := *o.ProviderKey
		copy.ProviderKey = &value
	}
	if o.RefundReason != nil {
		value := *o.RefundReason
		copy.RefundReason = &value
	}
	if o.RefundAt != nil {
		value := *o.RefundAt
		copy.RefundAt = &value
	}
	if o.RefundRequestedAt != nil {
		value := *o.RefundRequestedAt
		copy.RefundRequestedAt = &value
	}
	if o.RefundRequestReason != nil {
		value := *o.RefundRequestReason
		copy.RefundRequestReason = &value
	}
	if o.RefundRequestedBy != nil {
		value := *o.RefundRequestedBy
		copy.RefundRequestedBy = &value
	}
	if o.PaidAt != nil {
		value := *o.PaidAt
		copy.PaidAt = &value
	}
	if o.CompletedAt != nil {
		value := *o.CompletedAt
		copy.CompletedAt = &value
	}
	if o.FailedAt != nil {
		value := *o.FailedAt
		copy.FailedAt = &value
	}
	if o.FailedReason != nil {
		value := *o.FailedReason
		copy.FailedReason = &value
	}
	if o.SrcURL != nil {
		value := *o.SrcURL
		copy.SrcURL = &value
	}
	copy.BillingSnapshot = cloneOrderMap(o.BillingSnapshot)
	copy.ProviderSnapshot = cloneOrderMap(o.ProviderSnapshot)
	if o.PlanSnapshot.DailyLimitUSD != nil {
		value := *o.PlanSnapshot.DailyLimitUSD
		copy.PlanSnapshot.DailyLimitUSD = &value
	}
	if o.PlanSnapshot.WeeklyLimitUSD != nil {
		value := *o.PlanSnapshot.WeeklyLimitUSD
		copy.PlanSnapshot.WeeklyLimitUSD = &value
	}
	if o.PlanSnapshot.MonthlyLimitUSD != nil {
		value := *o.PlanSnapshot.MonthlyLimitUSD
		copy.PlanSnapshot.MonthlyLimitUSD = &value
	}
	return &copy
}
func cloneOrderMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = cloneOrderValue(value)
	}
	return out
}
func cloneOrderValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneOrderMap(typed)
	case []any:
		if typed == nil {
			return []any(nil)
		}
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = cloneOrderValue(v)
		}
		return out
	case []string:
		if typed == nil {
			return []string(nil)
		}
		return append([]string{}, typed...)
	case map[string]string:
		if typed == nil {
			return map[string]string(nil)
		}
		out := make(map[string]string, len(typed))
		for k, v := range typed {
			out[k] = v
		}
		return out
	default:
		return value
	}
}
