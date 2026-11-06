package payment

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// currency.go converts payment amounts between decimal text and integer API
// units using currency-specific precision.
// Payment amount precision and integer API encoding are separate
// currency properties. Most configured currencies use the same decimal
// scale for both, while the ISK and UGX entries accept whole-unit
// amounts but encode each unit as one hundred API units. For example,
// ISK twelve encodes as twelve hundred, while a fractional ISK amount
// fails the input-precision check.
//
// Decimal parsing checks whether the numeric amount fits its accepted
// precision before scaling it for the API. Signed values remain signed;
// callers impose business amount limits and integer-range requirements.
// Currency normalization validates three ASCII letters, and unlisted
// valid codes use two-decimal units. Display and precision lookup also
// use that default for invalid codes, while the string-to-integer
// conversion rejects invalid currency text.

// DefaultPaymentCurrency selects CNY when currency input is empty or
// provider configuration supplies no usable currency choice for its
// implementation.
const DefaultPaymentCurrency = "CNY"

// Currency-unit presets and their lookup table separate API exponents
// from accepted fractional digits, including the retained legacy
// whole-unit input with hundredth-unit API encoding.
var (
	zeroDecimalAmountUnit  = paymentCurrencyAmountUnit{apiMinorUnit: 0, maxFractionDigits: 0}
	twoDecimalAmountUnit   = paymentCurrencyAmountUnit{apiMinorUnit: 2, maxFractionDigits: 2}
	threeDecimalAmountUnit = paymentCurrencyAmountUnit{apiMinorUnit: 3, maxFractionDigits: 3}
	stripeLegacyZeroAmount = paymentCurrencyAmountUnit{apiMinorUnit: 2, maxFractionDigits: 0}

	paymentCurrencyAmountUnits = map[string]paymentCurrencyAmountUnit{
		"BIF": zeroDecimalAmountUnit,
		"CLP": zeroDecimalAmountUnit,
		"DJF": zeroDecimalAmountUnit,
		"GNF": zeroDecimalAmountUnit,
		"JPY": zeroDecimalAmountUnit,
		"KMF": zeroDecimalAmountUnit,
		"KRW": zeroDecimalAmountUnit,
		"MGA": zeroDecimalAmountUnit,
		"PYG": zeroDecimalAmountUnit,
		"RWF": zeroDecimalAmountUnit,
		"VND": zeroDecimalAmountUnit,
		"VUV": zeroDecimalAmountUnit,
		"XAF": zeroDecimalAmountUnit,
		"XOF": zeroDecimalAmountUnit,
		"XPF": zeroDecimalAmountUnit,
		"ISK": stripeLegacyZeroAmount,
		"UGX": stripeLegacyZeroAmount,
		"BHD": threeDecimalAmountUnit,
		"IQD": threeDecimalAmountUnit,
		"JOD": threeDecimalAmountUnit,
		"KWD": threeDecimalAmountUnit,
		"LYD": threeDecimalAmountUnit,
		"OMR": threeDecimalAmountUnit,
		"TND": threeDecimalAmountUnit,
	}
)

// paymentCurrencyAmountUnit holds API scaling and accepted decimal precision
// for one currency.
type paymentCurrencyAmountUnit struct {
	// apiMinorUnit determines integer API encoding, while maxFractionDigits
	// limits accepted input and display precision. The legacy whole-unit
	// preset uses different exponents, so validating input precision cannot
	// substitute for choosing the API scale.
	apiMinorUnit      int // API amount exponent
	maxFractionDigits int // accepted fractional digits
}

// NormalizePaymentCurrency trims and uppercases a three-letter ASCII
// code, using CNY for empty input. Invalid length or nonletter content
// returns an error; accepted text is syntactically checked and is not
// looked up in an ISO membership registry.
func NormalizePaymentCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" {
		return DefaultPaymentCurrency, nil
	}
	if len(currency) != 3 {
		return "", fmt.Errorf("payment currency must be a 3-letter ISO currency code")
	}
	for _, ch := range currency {
		if ch < 'A' || ch > 'Z' {
			return "", fmt.Errorf("payment currency must be a 3-letter ISO currency code")
		}
	}
	return currency, nil
}

// CurrencyMinorUnit returns the exponent used to encode one currency
// unit as an integer API amount. Empty input selects CNY; invalid or
// unlisted currency codes use the two-decimal preset, including its
// API scale.
func CurrencyMinorUnit(currency string) int {
	return paymentCurrencyAmountUnitFor(currency).apiMinorUnit
}

// CurrencyMaxFractionDigits returns the accepted decimal precision for
// payment input and display. Empty input selects CNY, and invalid or
// unlisted codes use two fractional digits; the legacy ISK and UGX
// entries return zero.
func CurrencyMaxFractionDigits(currency string) int {
	return paymentCurrencyAmountUnitFor(currency).maxFractionDigits
}

// FormatAmountForCurrency formats amount at the currency's accepted
// precision and returns the resulting string. Callers supply finite floats; decimal
// formatting rounds the amount and retains fixed trailing places
// according to the selected currency preset.
func FormatAmountForCurrency(amount float64, currency string) string {
	return decimal.NewFromFloat(amount).StringFixed(int32(CurrencyMaxFractionDigits(currency)))
}

// paymentCurrencyAmountUnitFor returns the configured currency unit
// preset or the two-decimal default. Empty input normalizes to CNY;
// invalid text and syntactically valid codes absent from the table
// use that same default.
func paymentCurrencyAmountUnitFor(currency string) paymentCurrencyAmountUnit {
	normalized, err := NormalizePaymentCurrency(currency)
	// Precision lookup supports formatting even when a caller supplied
	// invalid currency text. Conversion entry points separately normalize
	// and reject that text before using a scale.
	if err != nil {
		return twoDecimalAmountUnit
	}
	if amountUnit, ok := paymentCurrencyAmountUnits[normalized]; ok {
		return amountUnit
	}
	// Unlisted syntactically valid codes use the ordinary two-decimal
	// preset; normalization alone accepts their names and supplies no
	// currency-specific exponent for this table.
	return twoDecimalAmountUnit
}

// AmountToMinorUnit parses a signed decimal amount and returns its
// integer API representation for currency. The numeric value must fit
// the accepted input precision, so extra trailing zeros are permitted
// but a nonzero excess fraction is rejected.
//
// Input precision is checked separately from API scaling because the
// legacy whole-unit currencies use hundredth-unit API encoding. Invalid
// amount or currency text returns an error. The scaled amount needs to
// fit int64: decimal.IntPart supplies the result without an overflow
// error, and callers validate business sign and range independently.
func AmountToMinorUnit(amountStr, currency string) (int64, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(amountStr))
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", amountStr)
	}
	normalizedCurrency, err := NormalizePaymentCurrency(currency)
	if err != nil {
		return 0, err
	}
	amountUnit := paymentCurrencyAmountUnitFor(normalizedCurrency)
	// Check representability at the accepted input precision before API
	// scaling, so legacy whole-unit currencies reject fractions despite
	// their hundredth-unit encoding.
	precisionFactor := decimal.New(1, int32(amountUnit.maxFractionDigits))
	scaledForPrecision := d.Mul(precisionFactor)
	if !scaledForPrecision.Equal(scaledForPrecision.Truncate(0)) {
		if amountUnit.maxFractionDigits == 0 {
			return 0, fmt.Errorf("payment amount for %s must be a whole number", normalizedCurrency)
		}
		return 0, fmt.Errorf("payment amount for %s must not have more than %d decimal places", normalizedCurrency, amountUnit.maxFractionDigits)
	}
	// Scale the validated amount with its API exponent, including the
	// legacy whole-unit entries whose API scale differs from accepted
	// input precision.
	factor := decimal.New(1, int32(amountUnit.apiMinorUnit))
	minorAmount := d.Mul(factor)
	return minorAmount.IntPart(), nil
}

// MinorUnitToAmount converts a signed integer API amount into its
// currency units. The currency helper supplies the encoding scale,
// including legacy exponents; the decimal quotient is returned as an
// approximate float for response and order values.
func MinorUnitToAmount(value int64, currency string) float64 {
	// Divide by the currency API scale to restore major units.
	factor := decimal.New(1, int32(CurrencyMinorUnit(currency)))
	return decimal.NewFromInt(value).Div(factor).InexactFloat64()
}

// YuanToFen parses a signed CNY amount into its integer fen value.
// It delegates numeric precision checks to AmountToMinorUnit, accepting
// trailing zeros while rejecting a nonzero fraction smaller than one
// fen; business positivity checks remain with callers.
func YuanToFen(yuanStr string) (int64, error) {
	return AmountToMinorUnit(yuanStr, DefaultPaymentCurrency)
}

// FenToYuan returns a signed fen amount in CNY units as a float.
// It uses the CNY API exponent through MinorUnitToAmount, so negative
// values retain their sign and the float represents the decimal
// quotient approximately.
func FenToYuan(fen int64) float64 {
	return MinorUnitToAmount(fen, DefaultPaymentCurrency)
}

// ProviderConfigCurrency reads the configured currency for the trimmed
// Stripe, Airwallex or EPUSDT provider key. Other keys use CNY; empty or invalid
// configuration also selects CNY, allowing order snapshots and payment
// formatting to use the compatibility default.
func ProviderConfigCurrency(providerKey string, cfg map[string]string) string {
	switch strings.TrimSpace(providerKey) {
	case TypeStripe, TypeAirwallex, TypeEPUSDT:
		currency, err := NormalizePaymentCurrency(cfg["currency"])
		if err == nil {
			return currency
		}
	}
	// Unsupported provider keys and malformed optional currency settings
	// retain CNY for snapshot and formatting consumers; this helper returns
	// a usable label rather than a configuration error.
	return DefaultPaymentCurrency
}
