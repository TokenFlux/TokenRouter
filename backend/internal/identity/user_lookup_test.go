package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExactUserLookupNormalizesOneBoundedCriterion checks copied IDs and trimmed exact selectors.
// Ambiguous selectors, invalid identities, and empty requests fail before repositories can broaden the discovery scope.
func TestExactUserLookupNormalizesOneBoundedCriterion(t *testing.T) {
	ids := []int64{1, 2}
	normalized, err := (UserLookup{IDs: ids}).Normalize()
	require.NoError(t, err)
	normalized.IDs[0] = 9
	require.Equal(t, int64(1), ids[0])
	value, err := (UserLookup{Email: " USER@Example.Invalid "}).Normalize()
	require.NoError(t, err)
	require.Equal(t, "USER@Example.Invalid", value.Email)
	for _, input := range []UserLookup{{}, {IDs: []int64{1}, Username: "name"}, {IDs: []int64{0}}, {IDs: make([]int64, 101)}, {Email: "invalid\x00value"}} {
		_, err := input.Normalize()
		require.ErrorIs(t, err, ErrUserLookupInvalid)
	}
}
