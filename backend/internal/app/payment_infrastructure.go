package app

import (
	"log/slog"
	"os"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/service"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/payment"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

// payment.EncryptionKey is a named type for the payment encryption key (AES-256, 32 bytes).
// Using a named type avoids Wire ambiguity with other []byte parameters.

// providePaymentEncryptionKey derives the payment encryption key from the TOTP encryption key in config.
// When the key is empty, nil is returned (payment features that need encryption will be disabled).
// When the key is non-empty but invalid (bad hex or wrong length), an error is returned
// to prevent startup with a misconfigured encryption key.
func providePaymentEncryptionKey(cfg *config.Config) (payment.EncryptionKey, error) {
	if cfg == nil {
		slog.Warn("payment encryption key not configured — encrypted payment config and resume signing will be unavailable")
		return nil, nil
	}
	key, warning, err := payment.ConfiguredEncryptionKey(cfg.Totp.EncryptionKey, cfg.Totp.EncryptionKeyConfigured)
	if warning != "" {
		slog.Warn(warning)
	}
	return key, err
}

// providePaymentRegistry creates an empty payment provider registry.
// Providers are registered at runtime after application startup.
func providePaymentRegistry() *payment.Registry {
	return payment.NewRegistry()
}

// providePaymentLoadBalancer creates a DefaultLoadBalancer backed by the ent client.
func providePaymentLoadBalancer(store *paymentpostgres.InstanceStore, key payment.EncryptionKey) *payment.DefaultLoadBalancer {
	return payment.NewDefaultLoadBalancer(store, []byte(key), payment.SelectionRuntime{Observe: paymentSelectionLog})
}

func paymentSelectionLog(level, message string, attrs ...any) {
	if level == "warn" {
		slog.Warn(message, attrs...)
	} else {
		slog.Info(message, attrs...)
	}
}

func providePaymentConfigCore(store *paymentpostgres.InstanceStore, settings service.SettingRepository, key payment.EncryptionKey, plans *billing.Plans) *payment.ConfigService {
	return payment.NewConfigService(store, settings, []byte(key), plans, payment.ConfigurationRuntime{CreateProvider: provider.CreateProvider, LookupEnv: os.LookupEnv, Warn: slog.Warn})
}
func providePaymentConfiguration(core *payment.ConfigService, client *dbent.Client, settings service.SettingRepository, key payment.EncryptionKey, plans *billing.Plans) *service.PaymentConfigService {
	return service.WrapPaymentConfigService(core, client, settings, []byte(key), plans)
}
