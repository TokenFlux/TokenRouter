package composite

import "github.com/TokenFlux/TokenRouter/internal/payment"

// ApplyPaymentAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyPaymentAdminReadSettings(value *payment.AdminReadSettings) {
	s.PaymentVisibleMethodAlipayEnabled = value.PaymentVisibleMethodAlipayEnabled
	s.PaymentVisibleMethodAlipaySource = value.PaymentVisibleMethodAlipaySource
	s.PaymentVisibleMethodWxpayEnabled = value.PaymentVisibleMethodWxpayEnabled
	s.PaymentVisibleMethodWxpaySource = value.PaymentVisibleMethodWxpaySource
}
