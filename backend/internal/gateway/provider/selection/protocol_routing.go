package selection

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

func (s *Compatible) shadowProtocolsAllowed(ctx context.Context, account *gatewayprovider.ExecutionAccount) bool {
	if account == nil || !account.View().IsShadow() {
		return true
	}
	if source, _ := requeststate.ClientProtocolFromContext(ctx); source == "" {
		return true
	}
	parent := s.parentAccountLookup(ctx)(*account.Record.ParentAccountID)
	return parent != nil && gatewayprovider.ExecutionModelPolicy(parent).AllowsProtocol(ctx)
}
