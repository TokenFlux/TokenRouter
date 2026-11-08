package provider

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// factory.go constructs the payment adapter selected by a persisted provider
// key.
// Persisted payment instances identify a supported provider adapter by an
// exact key. Construction delegates credential validation and configuration
// ownership to that adapter, so callers receive its concrete provider or the
// error from its initialization checks.

// CreateProvider dispatches an exact provider key to its payment adapter
// constructor. It passes the instance ID and configuration through;
// configuration validation and copying depend on that constructor, and an
// unknown key returns an error.
func CreateProvider(providerKey string, instanceID string, config map[string]string) (payment.Provider, error) {
	switch providerKey {
	case payment.TypeEPUSDT:
		return NewEPUSDT(instanceID, config)
	case payment.TypeEasyPay:
		return NewEasyPay(instanceID, config)
	case payment.TypeAlipay:
		return NewAlipay(instanceID, config)
	case payment.TypeWxpay:
		return NewWxpay(instanceID, config)
	case payment.TypeStripe:
		return NewStripe(instanceID, config)
	case payment.TypeAirwallex:
		return NewAirwallex(instanceID, config)
	default:
		return nil, fmt.Errorf("unknown provider key: %s", providerKey)
	}
}
