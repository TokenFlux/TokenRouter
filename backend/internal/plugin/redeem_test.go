package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRedeemIssuanceRejectsUnsupportedCredentialsBeforeStorage submits disallowed code types and malformed entitlement terms without configured persistence.
// Permission, amount, plan, duration, and expiry syntax failures stop before the ledger or native credential creator can perform any work.
func TestRedeemIssuanceRejectsUnsupportedCredentialsBeforeStorage(t *testing.T) {
	service := NewRedeemCodes(nil, nil, nil, nil)
	_, err := service.Issue(t.Context(), Binding{}, RedeemInput{})
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	binding := Binding{PluginID: "test.redeem", Permissions: []string{"redeem.issue"}}
	for _, input := range []RedeemInput{
		{Type: "invitation", Key: "key"},
		{Type: "admin_balance", Value: "1", Key: "key"},
		{Type: "plugin_balance", Value: "1", Key: "key"},
		{Type: "balance", Value: "-1", Key: "key"},
		{Type: "balance", Value: "0", Key: "key"},
		{Type: "balance", Value: "1", PlanID: 7, Key: "key"},
		{Type: "balance", Value: "1", Days: 7, Key: "key"},
		{Type: "concurrency", Value: "1.5", Key: "key"},
		{Type: "concurrency", Value: "2147483648", Key: "key"},
		{Type: "subscription", PlanID: 0, Key: "key"},
		{Type: "subscription", PlanID: 7, Days: -1, Key: "key"},
		{Type: "subscription", PlanID: 7, Value: "5", Key: "key"},
		{Type: "balance", Value: "1", ExpiresUnix: -1, Key: "key"},
		{Type: "balance", Value: "1", Key: "bad key"},
	} {
		_, err := service.Issue(t.Context(), binding, input)
		require.ErrorIs(t, err, ErrInvalidArgument, input)
	}
}
