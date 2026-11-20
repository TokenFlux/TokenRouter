package selection

import (
	"context"
	"log/slog"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// compatible_policy.go applies group model restrictions and shadow-parent
// protocol checks during compatible selection.
// Group model restrictions can target the client name, the group-mapped name,
// or each provider's final upstream name. Compatible selection checks the first
// two before candidate choice, then uses provider mapping and Compact rules for
// upstream restrictions.

// openaiStickySessionTTL supplies the basic compatible binding lifetime used by
// scheduler sticky operations.
const openaiStickySessionTTL = time.Hour

// CheckGroupModelRestriction checks the group's model policy at the requested
// or group-mapped stage. Missing group, policy service, or model inputs return
// false; upstream restrictions wait until a provider supplies its final model.
func (s *Compatible) CheckGroupModelRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
	if groupID == nil || s.groupPolicies == nil || requestedModel == "" {
		return false
	}
	mapping := s.groupPolicies.ResolveGroupMapping(ctx, *groupID, requestedModel)
	billingModel := routing.ModelForRestriction(mapping.RestrictionModelSource, requestedModel, mapping.MappedModel)
	if billingModel == "" {
		return false
	}
	return s.groupPolicies.IsModelRestricted(ctx, *groupID, billingModel)
}

// resolveGroupRoutingModel returns the group's single-hop routing model for
// candidate selection. A nil selector or unavailable mapping retains
// requestedModel through PricingConfigService, so callers can continue using
// the incoming identity.
func (s *Compatible) resolveGroupRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel
	}
	return s.groupPolicies.ResolveRoutingModel(ctx, groupID,
		requestedModel,
	)
}

// UpstreamRoutingModelRestricted checks the provider's final model against the
// group's allowlist. Pass an already group-mapped routingModel; Compact
// requests use the provider's Compact mapping and an empty final name returns
// false.
func (s *Compatible) UpstreamRoutingModelRestricted(ctx context.Context, groupID int64, provider *gatewayprovider.ExecutionProvider, routingModel string, requireCompact bool) bool {
	if s.groupPolicies == nil {
		return false
	}
	upstreamModel := gatewayprovider.ExecutionModelPolicy(provider).UpstreamModel(ctx, routingModel)
	if requireCompact {
		upstreamModel = gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).OpenAIUpstream(routingModel, true)
	}
	if upstreamModel == "" {
		return false
	}
	return s.groupPolicies.IsModelRestricted(ctx, groupID, upstreamModel)
}

// NeedsUpstreamGroupRestriction reports whether an enabled group policy uses
// the upstream model stage. Missing inputs or policy read failures return
// false, and failures are logged for callers diagnosing the unavailable rule.
func (s *Compatible) NeedsUpstreamGroupRestriction(ctx context.Context, groupID *int64) bool {
	if groupID == nil || s.groupPolicies == nil {
		return false
	}
	ch, err := s.groupPolicies.GetGroupPolicy(ctx, *groupID)
	if err != nil {
		// A failed policy read cannot identify an upstream restriction stage. Leave
		// that gate disabled for this call and retain the error in the warning.
		slog.Warn("failed to check openai group upstream model restriction", "group_id", *groupID, "error", err)
		return false
	}
	if ch == nil || !ch.RestrictModels {
		return false
	}
	return ch.RestrictionSource() == routing.BillingModelSourceUpstream
}

// shadowProtocolsAllowed checks a Spark shadow's parent protocol policy for the
// request. Nil or ordinary providers and requests without a client protocol
// pass; a shadow with no usable parent fails the protocol check.
func (s *Compatible) shadowProtocolsAllowed(ctx context.Context, provider *gatewayprovider.ExecutionProvider) bool {
	if provider == nil || !provider.View().IsShadow() {
		return true
	}
	if source, _ := requeststate.ClientProtocolFromContext(ctx); source == "" {
		return true
	}
	parent := s.parentProviderLookup(ctx)(*provider.Record.ParentProviderID)
	return parent != nil && gatewayprovider.ExecutionModelPolicy(parent).AllowsProtocol(ctx, "")
}
