package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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
		`{"wallet":{"max_credit_per_op":1e2}}`,
		`{"wallet":{"max_credit_per_op":0.000000001}}`,
		`{"wallet":{"typo":3}}`,
		`{"subscription":{"allowed_plan_ids":[1,1]}}`,
		`{"email":{"max_per_day":-1}}`,
	} {
		_, err := ParseLimits([]byte(input))
		require.ErrorIs(t, err, ErrLimitsInvalid, input)
	}
}
