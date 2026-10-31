package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWalletValidatesPermissionAndParametersBeforeCallerGrant submits malformed debits without valid caller authorization.
// Permission failures take precedence, then argument validation, and only a valid business request reaches grant verification or ledger work.
func TestWalletValidatesPermissionAndParametersBeforeCallerGrant(t *testing.T) {
	grants, err := NewGrants("wallet-validation-fixture", nil)
	require.NoError(t, err)
	wallet := NewWalletService(nil, grants, nil, nil, nil)
	input := WalletInput{UserID: 1, Amount: "0", Key: "call"}
	_, err = wallet.Debit(t.Context(), Binding{PluginID: "test.wallet"}, input)
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	binding := Binding{PluginID: "test.wallet", Permissions: []string{"wallet.debit"}}
	_, err = wallet.Debit(t.Context(), binding, input)
	require.ErrorIs(t, err, ErrInvalidArgument)
	input.Amount = "1"
	_, err = wallet.Debit(t.Context(), binding, input)
	require.ErrorIs(t, err, ErrGrantInvalid)
	input.UserID = 0
	_, err = wallet.Debit(t.Context(), binding, input)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
