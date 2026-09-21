package testkit

import (
	"log/slog"
	"os"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
)

// Configuration 仅组装原生配置用例，保持测试的环境读取及渠道验证行为。
func Configuration(client *dbent.Client, settings payment.ConfigurationSettings, key []byte) *payment.ConfigService {
	var store payment.ConfigurationStore
	if client != nil {
		store = paymentpostgres.NewInstanceStore(client)
	}
	plans := billing.NewPlans(billingpostgres.NewPlanStore(client), paymentpostgres.NewInstanceStore(client))
	return payment.NewConfigService(store, settings, key, plans, payment.ConfigurationRuntime{CreateProvider: provider.CreateProvider, LookupEnv: os.LookupEnv, Warn: slog.Warn})
}
