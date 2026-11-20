package selection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
)

// long_session_model.go validates later WebSocket turn models against the
// connection's selected provider and group policy.
// A WebSocket connection keeps its selected provider across turns, so a later
// model must fit that provider's current route and runtime state. Client model
// restrictions run before group mapping; the resulting routing name then goes
// through the fixed provider's protocol, capability, and transient checks.
//
// Upstream model restrictions need the provider's final mapping as well as the
// group policy. A successful turn returns the intermediate routing name for
// forwarding, while a rejected turn returns an error and empty model. This
// separates the client, group, and upstream identities while keeping each turn
// on the provider selected for its connection.

// ResolveOpenAIWSRoutingModelForProvider returns the group-mapped model a fixed
// WebSocket provider can serve for this turn. Empty client input is rejected,
// and an empty group mapping falls back to the trimmed client model.
//
// The chosen provider must satisfy protocol, model, endpoint capability, and
// transient eligibility checks. Group restrictions at the client or mapped
// stage run before provider eligibility, while an upstream restriction runs
// after provider mapping. A failure rejects the turn because the connection
// retains its selected provider.
func (s *Compatible) ResolveOpenAIWSRoutingModelForProvider(
	ctx context.Context,
	groupID *int64,
	provider *gatewayprovider.ExecutionProvider,
	requestedModel string,
	requiredCapability providercore.OpenAIEndpointCapability,
) (string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", errors.New("websocket request model is empty")
	}
	if s.CheckGroupModelRestriction(ctx, groupID, requestedModel) {
		return "", fmt.Errorf("model %s is restricted by group model policy", requestedModel)
	}

	routingModel := strings.TrimSpace(s.resolveGroupRoutingModel(ctx, groupID, requestedModel))
	// An empty group-mapping result supplies no dispatch identity. Keep this turn's
	// trimmed client name so fixed-provider capability checks still receive a model.
	if routingModel == "" {
		routingModel = requestedModel
	}
	if provider == nil || !gatewayprovider.
		CompatibleProviderEligible(
			ctx,
			provider,
			provider.Record.Platform,
			routingModel,
			false,
			requiredCapability,
		) {
		return "", fmt.Errorf("model %s is not supported by the selected websocket provider", requestedModel)
	}
	if s.isOpenAIProviderRequestRuntimeBlocked(ctx, provider, routingModel) {
		return "", fmt.Errorf("model %s is temporarily unavailable on the selected websocket provider", requestedModel)
	}
	if groupID != nil && s.NeedsUpstreamGroupRestriction(ctx, groupID) &&
		s.UpstreamRoutingModelRestricted(ctx, *groupID, provider, routingModel, false) {
		return "", fmt.Errorf("model %s is restricted after provider mapping", requestedModel)
	}
	return routingModel, nil
}
