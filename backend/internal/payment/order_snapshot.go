package payment

import (
	"fmt"
	"strconv"
	"strings"
)

// order_snapshot.go interprets saved provider snapshots and validates channel
// metadata against recorded merchant and currency identity.
// Payment confirmation can arrive after a channel configuration changes,
// so an order retains merchant and currency facts from its checkout.
// A typed snapshot reads those persisted attributes for notification
// comparisons, while orders with missing legacy attributes can still
// follow their compatibility paths. Snapshot presence means some recorded
// data exists, not that every merchant field or schema version is valid.
//
// String attributes retain trimmed string values; numeric version markers
// accept the ordinary integer, float, and decimal-string forms handled
// by the reader. Notification validation compares the supported provider's
// recorded expected fields with reported metadata, leaving signature,
// provider binding, and paid-amount checks to their owning callers.
// Currency lookup normalizes the recorded currency when possible and
// uses the compatibility default when the snapshot cannot supply one.
// Subscription face amounts retain their plan currency separately from the
// channel currency used for collected money and payment fees.

// OrderProviderSnapshot retains typed checkout merchant, channel, and currency
// attributes.
type OrderProviderSnapshot struct {
	SchemaVersion      int    // recorded snapshot version
	ProviderInstanceID string // selected channel instance
	ProviderKey        string // selected provider implementation
	PaymentMode        string // recorded checkout mode
	MerchantAppID      string // recorded merchant application
	MerchantID         string // recorded merchant identity
	Currency           string // recorded currency text
}

// PsOrderProviderSnapshot returns a new typed view of the order's
// recorded provider attributes, or nil for missing input and an all-empty
// view. A nonzero version alone makes the view present; this reader
// does not validate supported schema versions or merchant completeness.
//
// The source map can contain values from JSON decoding or older writers,
// so the version reader accepts several numeric representations while
// identity readers retain strings. Unsupported identity values become
// empty, allowing provider-specific validation to distinguish recorded
// expected fields from legacy missing attributes.
func PsOrderProviderSnapshot(order *Order) *OrderProviderSnapshot {
	if order == nil || len(order.ProviderSnapshot) == 0 {
		return nil
	}

	snapshot := &OrderProviderSnapshot{
		SchemaVersion:      PsSnapshotIntValue(order.ProviderSnapshot["schema_version"]),
		ProviderInstanceID: PsSnapshotStringValue(order.ProviderSnapshot["provider_instance_id"]),
		ProviderKey:        PsSnapshotStringValue(order.ProviderSnapshot["provider_key"]),
		PaymentMode:        PsSnapshotStringValue(order.ProviderSnapshot["payment_mode"]),
		MerchantAppID:      PsSnapshotStringValue(order.ProviderSnapshot["merchant_app_id"]),
		MerchantID:         PsSnapshotStringValue(order.ProviderSnapshot["merchant_id"]),
		Currency:           PsSnapshotStringValue(order.ProviderSnapshot["currency"]),
	}
	if snapshot.SchemaVersion == 0 &&
		snapshot.ProviderInstanceID == "" &&
		snapshot.ProviderKey == "" &&
		snapshot.PaymentMode == "" &&
		snapshot.MerchantAppID == "" &&
		snapshot.MerchantID == "" &&
		snapshot.Currency == "" {
		return nil
	}
	return snapshot
}

// PsSnapshotStringValue returns a trimmed snapshot string or an empty
// value for any other representation. Merchant and currency readers
// interpret the empty result as an absent attribute; this helper does
// not stringify numeric or structured input.
func PsSnapshotStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

// PsSnapshotIntValue reads accepted integer, float, or decimal-string
// version markers using ordinary Go conversions. Unsupported types and
// failed string parsing return zero; numeric casts do not validate
// schema versions or guard their integer range.
func PsSnapshotIntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return n
		}
	}
	// An unreadable version contributes no marker to snapshot presence.
	// Other recorded identity strings can still make the typed view
	// nonempty, so version parsing failure does not discard them.
	return 0
}

// ValidateProviderSnapshotMetadata compares reported merchant and
// currency metadata with the recorded attributes for supported provider
// keys. Nil order, empty metadata, or an absent typed snapshot skips
// these comparisons for legacy input. An unrecognized provider key also
// selects no comparison branch.
//
// Recorded attributes generally compare trimmed text case-insensitively.
// EPUSDT requires an exact nonempty PID and compares fiat currency
// case-insensitively. The checked fields depend on the provider, and supplied
// WeChat or Airwallex success-status metadata is checked when present.
// Callers separately establish notification
// signatures, provider binding, and paid-amount validity before applying
// any payment transition.
func ValidateProviderSnapshotMetadata(order *Order, providerKey string, metadata map[string]string) error {
	if order == nil || len(metadata) == 0 {
		return nil
	}

	snapshot := PsOrderProviderSnapshot(order)
	if snapshot == nil {
		return nil
	}

	switch strings.TrimSpace(providerKey) {
	case TypeWxpay:
		if expected := strings.TrimSpace(snapshot.MerchantAppID); expected != "" {
			actual := strings.TrimSpace(metadata["appid"])
			if actual == "" {
				return fmt.Errorf("wxpay notification missing appid")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("wxpay appid mismatch: expected %s, got %s", expected, actual)
			}
		}
		if expected := strings.TrimSpace(snapshot.MerchantID); expected != "" {
			actual := strings.TrimSpace(metadata["mchid"])
			if actual == "" {
				return fmt.Errorf("wxpay notification missing mchid")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("wxpay mchid mismatch: expected %s, got %s", expected, actual)
			}
		}
		if expected := strings.TrimSpace(snapshot.Currency); expected != "" {
			actual := strings.ToUpper(strings.TrimSpace(metadata["currency"]))
			if actual == "" {
				return fmt.Errorf("wxpay notification missing currency")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("wxpay currency mismatch: expected %s, got %s", expected, actual)
			}
		}
		if actual := strings.TrimSpace(metadata["trade_state"]); actual != "" && !strings.EqualFold(actual, "SUCCESS") {
			return fmt.Errorf("wxpay trade_state mismatch: expected SUCCESS, got %s", actual)
		}
	case TypeAlipay:
		if expected := strings.TrimSpace(snapshot.MerchantAppID); expected != "" {
			actual := strings.TrimSpace(metadata["app_id"])
			if actual == "" {
				return fmt.Errorf("alipay app_id missing")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("alipay app_id mismatch: expected %s, got %s", expected, actual)
			}
		}
	case TypeEPUSDT:
		if metadata["pid"] == "" || metadata["pid"] != snapshot.MerchantID {
			return fmt.Errorf("epusdt merchant PID does not match the order")
		}
		if metadata["currency"] == "" || !strings.EqualFold(metadata["currency"], snapshot.Currency) {
			return fmt.Errorf("epusdt fiat currency does not match the order")
		}
	case TypeEasyPay:
		if expected := strings.TrimSpace(snapshot.MerchantID); expected != "" {
			actual := strings.TrimSpace(metadata["pid"])
			if actual == "" {
				return fmt.Errorf("easypay pid missing")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("easypay pid mismatch: expected %s, got %s", expected, actual)
			}
		}
	case TypeStripe:
		if expected := strings.TrimSpace(snapshot.Currency); expected != "" {
			actual := strings.ToUpper(strings.TrimSpace(metadata["currency"]))
			if actual == "" {
				return fmt.Errorf("stripe notification missing currency")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("stripe currency mismatch: expected %s, got %s", expected, actual)
			}
		}
	case TypeAirwallex:
		if expected := strings.TrimSpace(snapshot.MerchantID); expected != "" {
			actual := strings.TrimSpace(metadata["account_id"])
			if actual == "" {
				return fmt.Errorf("airwallex account_id missing")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("airwallex account_id mismatch: expected %s, got %s", expected, actual)
			}
		}
		if expected := strings.TrimSpace(snapshot.Currency); expected != "" {
			actual := strings.ToUpper(strings.TrimSpace(metadata["currency"]))
			if actual == "" {
				return fmt.Errorf("airwallex notification missing currency")
			}
			if !strings.EqualFold(expected, actual) {
				return fmt.Errorf("airwallex currency mismatch: expected %s, got %s", expected, actual)
			}
		}
		if actual := strings.TrimSpace(metadata["status"]); actual != "" && !strings.EqualFold(actual, "SUCCEEDED") {
			return fmt.Errorf("airwallex status mismatch: expected SUCCEEDED, got %s", actual)
		}
	}

	return nil
}

// ProviderMerchantIdentityMetadata returns merchant attributes from an
// optional MerchantIdentityProvider. A nil provider or unsupported
// interface returns nil; the reporter's map is returned as supplied,
// so this wrapper adds no ownership copy or validation.
func ProviderMerchantIdentityMetadata(prov Provider) map[string]string {
	if prov == nil {
		return nil
	}
	reporter, ok := prov.(MerchantIdentityProvider)
	if !ok {
		return nil
	}
	return reporter.MerchantIdentityMetadata()
}

// PaymentOrderCurrency returns a normalized recorded currency when the
// order snapshot can supply one. Missing snapshots, empty currency, or
// invalid currency text selects CNY; normalization checks code syntax
// and retains accepted unlisted codes.
func PaymentOrderCurrency(order *Order) string {
	if snapshot := PsOrderProviderSnapshot(order); snapshot != nil {
		if currency, err := NormalizePaymentCurrency(snapshot.Currency); err == nil {
			return currency
		}
	}
	// A historical order can lack a usable recorded currency. Returning
	// the compatibility default lets refund and reporting consumers retain
	// their existing currency interpretation for that row.
	return DefaultPaymentCurrency
}

// PaymentOrderAmountCurrency returns the saved subscription price currency.
// Balance orders and nil inputs return empty because their face amounts use
// account units. Historical subscriptions without a usable plan currency use
// their recorded payment currency, matching their existing display convention.
func PaymentOrderAmountCurrency(order *Order) string {
	if order == nil || order.OrderType != OrderTypeSubscription {
		return ""
	}
	if raw := strings.TrimSpace(order.PlanSnapshot.Currency); raw != "" {
		if currency, err := NormalizePaymentCurrency(raw); err == nil {
			return currency
		}
	}
	// Older snapshots may omit the price currency. Keep their recorded payment
	// unit because current plan settings cannot establish the historical price denomination.
	return PaymentOrderCurrency(order)
}
