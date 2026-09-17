//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func expectedNotificationProviderKeyForOrder(registry *payment.Registry, order *dbent.PaymentOrder, instanceProviderKey string) string {
	return payment.ExpectedNotificationProviderKeyForOrder(registry, paymentOrderValue(order), instanceProviderKey)
}
