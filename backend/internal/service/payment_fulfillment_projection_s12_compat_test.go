//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func paymentFulfillmentLeaseValue(v *paymentFulfillmentLease) *payment.FulfillmentLease {
	if v == nil {
		return nil
	}
	return &payment.FulfillmentLease{Version: v.version}
}

func paymentFulfillmentLeaseLegacy(v *payment.FulfillmentLease) *paymentFulfillmentLease {
	if v == nil {
		return nil
	}
	return &paymentFulfillmentLease{version: v.Version}
}
