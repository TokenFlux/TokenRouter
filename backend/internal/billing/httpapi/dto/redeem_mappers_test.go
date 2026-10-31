package dto

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// TestPluginBalanceHistoryExposesItsOperationReason maps plugin adjustments and ordinary redeem credentials for user history.
// The plugin ID and visible reason survive, while internal notes on ordinary credentials remain absent.
func TestPluginBalanceHistoryExposesItsOperationReason(t *testing.T) {
	pluginRecord := &billing.RedeemCode{Type: billing.AdjustmentTypePluginBalance, Notes: "[test.reward] Daily reward"}
	projected := RedeemCodeFromService(pluginRecord)
	require.NotNil(t, projected.Notes)
	require.Equal(t, "[test.reward] Daily reward", *projected.Notes)
	ordinary := RedeemCodeFromService(&billing.RedeemCode{Type: billing.RedeemTypeBalance, Notes: "internal-only"})
	require.Nil(t, ordinary.Notes)
}
