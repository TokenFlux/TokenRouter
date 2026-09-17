// 旧配置入口只投影与委托；唯一规则在 payment，S15/S16 清理。
package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentConfigService) UsesOfficialAlipayVisibleMethod(ctx context.Context) (bool, error) {
	return s.paymentCoreConfig().UsesOfficialAlipayVisibleMethod(ctx)
}

func isOfficialAlipayProviderInstance(instance *dbent.PaymentProviderInstance) bool {
	return payment.ConfigIsOfficialAlipayProviderInstance(paymentInstance(instance))
}
