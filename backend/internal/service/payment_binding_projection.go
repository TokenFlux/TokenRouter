// 旧渠道入口保留签名及测试构造参数；注册表状态由 payment 唯一拥有。
package service

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
)

func (s *PaymentService) paymentBindings() *payment.ProviderBindings {
	s.bindingOnce.Do(func() {
		if s.bindings != nil {
			return
		}
		var store payment.BindingStore
		if s.entClient != nil {
			store = paymentpostgres.NewInstanceStore(s.entClient)
		}
		s.bindings = payment.NewProviderBindings(store, s.registry, s.loadBalancer, payment.BindingRuntime{Factory: func(key, id string, cfg map[string]string) (payment.Provider, error) {
			return createPaymentProviderFromInstance(key, id, cfg)
		}, RegistryFactory: provider.CreateProvider, Warn: slog.Warn}, s.providersLoaded)
	})
	return s.bindings
}

// BindProviderBindings 只在应用图构造时注入唯一渠道绑定实例。
func (s *PaymentService) BindProviderBindings(bindings *payment.ProviderBindings) {
	s.bindings = bindings
}
