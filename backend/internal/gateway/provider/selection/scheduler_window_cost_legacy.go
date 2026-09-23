package selection

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func costWindowInput(a *gatewayprovider.ExecutionAccount) billing.CostWindowInput {
	if a == nil {
		return billing.CostWindowInput{}
	}
	return billing.CostWindowInput{ID: a.Record.ID, Enabled: a.View().IsAnthropicOAuthOrSetupToken(), Limit: gatewayprovider.ExecutionRuntimeConfig(a).GetWindowCostLimit(), Reserve: gatewayprovider.ExecutionRuntimeConfig(a).GetWindowCostStickyReserve(), Start: a.Record.SessionWindowStart, End: a.Record.SessionWindowEnd}
}
