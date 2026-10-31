package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSubscriptionGrantRequiresPermissionAndExplicitPositiveDays checks authorization and the direct-grant duration contract before ledger access.
// Zero days cannot select plan defaults on this RPC, and invalid recipient or plan identifiers never reach issuance.
func TestSubscriptionGrantRequiresPermissionAndExplicitPositiveDays(t *testing.T) {
	service := NewSubscriptions(nil, nil, nil)
	binding := Binding{PluginID: "test.subscriptions"}
	input := SubscriptionInput{UserID: 1, PlanID: 2, Days: 1, Key: "grant"}
	_, err := service.Grant(t.Context(), binding, input)
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	binding.Permissions = []string{"subscription.grant"}
	for _, invalid := range []SubscriptionInput{
		{UserID: 1, PlanID: 2, Days: 0, Key: "grant"},
		{UserID: 0, PlanID: 2, Days: 1, Key: "grant"},
		{UserID: 1, PlanID: 0, Days: 1, Key: "grant"},
	} {
		_, err := service.Grant(t.Context(), binding, invalid)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}
