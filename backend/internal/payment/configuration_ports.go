package payment

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// configuration_ports.go binds payment configuration storage, billing plan
// lookup, and provider construction callbacks.
// Payment settings and provider instances use different persistence readers,
// while subscription checkout obtains its sale plan from billing. Construction
// binds these dependencies and supplies limited runtime defaults; legacy
// encrypted configurations still require the caller's stable key bytes when
// they are read.

type (
	// SubscriptionPlan is the billing plan value used by checkout configuration
	// readers.
	SubscriptionPlan = billing.SubscriptionPlan
	// ConfigurationSettings reads and persists the string values interpreted by
	// payment configuration.
	ConfigurationSettings interface {
		// GetValue reads one named payment setting for visible-source selection. The
		// reader uses settings.ErrSettingNotFound as an absent value while
		// propagating other errors to its payment caller.
		GetValue(context.Context, string) (string, error)
		// GetMultiple returns the requested setting values in a batch.
		// GetPaymentConfig requires that batch to succeed before parsing defaults,
		// localization, or fee settings for the response.
		GetMultiple(context.Context, []string) (map[string]string, error)
		// SetMultiple persists the prepared supplied setting keys as one update
		// batch. Omitted keys remain outside that batch; standalone payment updates
		// delegate commit and notification policy to the settings implementation.
		SetMultiple(context.Context, map[string]string) error
	}
)

// ConfigurationPlans supplies billing plan reads for subscription checkout
// validation.
type ConfigurationPlans interface {
	// GetPlan returns the billing plan selected by its local ID. Checkout
	// requires a nonnil successful plan to inspect ForSale, while
	// ConfigService.GetByID applies the requested display locale.
	GetPlan(context.Context, int64) (*billing.SubscriptionPlan, error)
}

// ConfigurationRuntime supplies provider validation, environment lookup, probe
// identity, and logging.
type ConfigurationRuntime struct {
	// CreateProvider validates enabled instance configuration and constructs
	// EasyPay draft-query providers. NewConfigService supplies an error-returning
	// function when callers omit this adapter factory.
	CreateProvider func(string, string, map[string]string) (Provider, error)
	NewTradeNo     func() string        // draft probe merchant number
	Warn           func(string, ...any) // configuration warning callback
}

// ConfigService interprets payment settings, manages instances, and reads
// checkout plans.
type ConfigService struct {
	store       ConfigurationStore    // provider instance persistence
	settingRepo ConfigurationSettings // payment settings persistence
	// encryptionKey retains the caller's byte slice for legacy configuration
	// decryption. Keep its contents stable while the service reads ciphertext
	// through parseProviderConfig.
	encryptionKey []byte
	plans         ConfigurationPlans   // billing plan lookup
	runtime       ConfigurationRuntime // injected configuration operations
}

// NewConfigService retains the supplied configuration stores, billing reader,
// key bytes, and runtime callbacks. Missing trade generation uses
// GenerateOutTradeNo, and missing provider
// construction returns an unavailable-factory error when invoked.
func NewConfigService(store ConfigurationStore, settings ConfigurationSettings, key []byte, plans ConfigurationPlans, runtime ConfigurationRuntime) *ConfigService {
	if runtime.NewTradeNo == nil {
		runtime.NewTradeNo = GenerateOutTradeNo
	}
	if runtime.CreateProvider == nil {
		runtime.CreateProvider = func(string, string, map[string]string) (Provider, error) {
			return nil, fmt.Errorf("payment provider factory unavailable")
		}
	}
	return &ConfigService{store: store, settingRepo: settings, encryptionKey: key, plans: plans, runtime: runtime}
}

// warn forwards configuration diagnostics when a warning callback was
// supplied. An absent callback discards the event, allowing parsing and
// instance management callers to retain their own result and error behavior.
func (s *ConfigService) warn(message string, attrs ...any) {
	if s.runtime.Warn != nil {
		s.runtime.Warn(message, attrs...)
	}
}

// configPointer returns a pointer to a copy of the supplied value.
func configPointer[T any](v T) *T { return &v }
