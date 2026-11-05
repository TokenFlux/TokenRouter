package httpapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// management_operations_test.go checks administrator input contracts exposed
// through the shared management catalog. Raw HTTP members need explicit wire
// types so MCP callers can preserve omitted updates, clear nullable overrides,
// and distinguish numbers from strings before dispatch.

// TestUpdateUserRechargeMultiplierSchema checks the same nullable number used
// by HTTP account edits. Catalog validation accepts omission and explicit null
// while rejecting strings, booleans and collections for this field.
func TestUpdateUserRechargeMultiplierSchema(t *testing.T) {
	operation := ManagementOperations["users.update"]
	require.NoError(t, operation.Validate(map[string]any{"path": map[string]any{"id": "42"}, "body": map[string]any{}}))
	for _, value := range []any{nil, float64(18), float64(0.5)} {
		require.NoError(t, operation.Validate(map[string]any{
			"path": map[string]any{"id": "42"},
			"body": map[string]any{"balance_recharge_multiplier": value},
		}))
	}
	for _, value := range []any{"18", true, []any{}, map[string]any{}} {
		require.Error(t, operation.Validate(map[string]any{
			"path": map[string]any{"id": "42"},
			"body": map[string]any{"balance_recharge_multiplier": value},
		}))
	}
}
