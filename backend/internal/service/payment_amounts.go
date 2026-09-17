package service

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func calculateCreditedBalance(paymentAmount, multiplier float64) float64 {
	return payment.CalculateCreditedBalance(paymentAmount, multiplier)
}
