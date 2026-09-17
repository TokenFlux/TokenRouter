//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func calculateGatewayRefundAmount(orderAmount, payAmount, refundAmount float64, currency string) float64 {
	return payment.CalculateGatewayRefundAmount(orderAmount, payAmount, refundAmount, currency)
}
