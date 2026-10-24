package testkit

import (
	"log/slog"
	"os"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
)

// configuration.go assembles payment configuration readers and provider
// validation for tests using production storage adapters.
// Payment configuration scenarios reuse the production instance and billing
// plan readers with their test settings. This helper also reads process
// environment overrides and validates provider drafts through the production
// factory, so tests exercise each constructor's actual validation depth.

// Configuration binds Ent instance/plan readers, supplied settings, and legacy
// key bytes to a payment configuration service. It uses process environment
// lookup and the production provider factory; nil client supports pure parsing
// paths, while persisted reads still require a usable client.
func Configuration(client *dbent.Client, settings payment.ConfigurationSettings, key []byte) *payment.ConfigService {
	var store payment.ConfigurationStore
	if client != nil {
		store = paymentpostgres.NewInstanceStore(client)
	}
	plans := billing.NewPlans(billingpostgres.NewPlanStore(client), paymentpostgres.NewInstanceStore(client), nil)
	return payment.NewConfigService(store, settings, key, plans, payment.ConfigurationRuntime{CreateProvider: paymentadapter.CreateProvider, LookupEnv: os.LookupEnv, Warn: slog.Warn})
}
