package service

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func PaymentOrderCurrency(order *dbent.PaymentOrder) string {
	return payment.PaymentOrderCurrency(paymentOrderValue(order))
}
