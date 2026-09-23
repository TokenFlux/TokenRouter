//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"

	"github.com/google/wire"
)

// payment 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var paymentAssemblyProviders = wire.NewSet(
	providePaymentExpiry,
	providePaymentHTTP,
	providePaymentAdminHTTP,
	providePaymentWebhookHTTP,
	providePaymentConfigCore,
	providePaymentRuntime,
	paymentProviders,
)

// 支付 Wire 集合只供生成器使用，运行构造函数留在普通 app 文件。
// ProviderSet is the Wire provider set for the payment package.
var paymentProviders = wire.NewSet(
	paymentpostgres.NewInstanceStore,
	providePaymentEncryptionKey,
	providePaymentRegistry,
	providePaymentLoadBalancer,
	wire.Bind(new(payment.LoadBalancer), new(*payment.DefaultLoadBalancer)),
)
