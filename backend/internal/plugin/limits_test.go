package plugin

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// limits_test.go checks complete policy resolution and exact monetary normalization.
// The cases distinguish omitted defaults from explicit zero prohibitions and verify
// that browser-compatible JSON exponent notation cannot weaken the supported precision
// or integer-width boundaries of stored capability ceilings.

// TestLimitsDistinguishDefaultsFromProhibition resolves omitted limits alongside explicit zero and an exact fractional ceiling.
// Neither zero nor eight-decimal precision is lost during parsing and serialization.
func TestLimitsDistinguishDefaultsFromProhibition(t *testing.T) {
	limits, err := ParseLimits([]byte(`{"wallet":{"max_credit_per_op":0,"max_credit_per_day":0.12345678},"subscription":{"allowed_plan_ids":[3,1]}}`))
	require.NoError(t, err)
	require.Equal(t, json.Number("0"), limits.Wallet.MaxCreditPerOp)
	require.Equal(t, json.Number("0.12345678"), limits.Wallet.MaxCreditPerDay)
	require.Equal(t, json.Number("5"), limits.Wallet.MaxCreditPerUserPerDay)
	require.Equal(t, []int64{1, 3}, limits.Subscription.AllowedPlanIDs)
	encoded, err := json.Marshal(limits)
	require.NoError(t, err)
	again, err := ParseLimits(encoded)
	require.NoError(t, err)
	require.Equal(t, limits, again)
}

// TestLimitsRejectMalformedPolicies checks nulls, unknown fields, invalid amounts, and duplicate plan identities.
// Every invalid policy fails before a partially defaulted object can be published.
func TestLimitsRejectMalformedPolicies(t *testing.T) {
	for _, input := range []string{
		`null`,
		`[]`,
		`{} {}`,
		`{"wallet":null}`,
		`{"wallet":{"max_credit_per_op":null}}`,
		`{"wallet":{"max_credit_per_op":-1}}`,
		`{"wallet":{"max_credit_per_op":1e7}}`,
		`{"wallet":{"max_credit_per_op":1e-9}}`,
		`{"wallet":{"max_credit_per_op":1e999999999}}`,
		`{"wallet":{"max_credit_per_op":1e-999999999}}`,
		`{"wallet":{"max_credit_per_op":0.000000001}}`,
		`{"wallet":{"typo":3}}`,
		`{"subscription":{"allowed_plan_ids":[1,1]}}`,
		`{"email":{"max_per_day":-1}}`,
	} {
		_, err := ParseLimits([]byte(input))
		require.ErrorIs(t, err, ErrLimitsInvalid, input)
	}
}

// TestLimitsNormalizeBrowserNumericNotation round-trips exact quotas that JavaScript can serialize with an exponent.
// Canonical output preserves value, retains zero prohibitions, and stays accepted by later policy reads.
func TestLimitsNormalizeBrowserNumericNotation(t *testing.T) {
	for input, expected := range map[string]string{
		"1e-8":                      "0.00000001",
		"1E-7":                      "0.0000001",
		"1e2":                       "100",
		"0.0000000001e10":           "1",
		"1.230000000e0":             "1.23",
		"999999999999999e-8":        "9999999.99999999",
		"0e99999999999999999999999": "0",
		"0.0000e-99999999999999999": "0",
	} {
		limits, err := ParseLimits([]byte(fmt.Sprintf(`{"wallet":{"max_credit_per_op":%s}}`, input)))
		require.NoError(t, err, input)
		require.Equal(t, json.Number(expected), limits.Wallet.MaxCreditPerOp, input)
		encoded, err := json.Marshal(limits)
		require.NoError(t, err)
		again, err := ParseLimits(encoded)
		require.NoError(t, err)
		require.Equal(t, limits, again)
	}
}
