package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_group_fallback.go resolves authorized fallback groups and traverses
// client-restricted group chains.
// A text request can move to another configured group after the current group
// rejects its client. Each destination is resolved through API-Key
// authorization before its protocol, funding, and session isolation are
// checked. The returned Key and subscription belong to that destination, so
// later route planning uses the selected group.
//
// Client restriction traversal follows configured fallback IDs and records
// visited groups to stop cycles. It checks cancellation before each hop and
// returns a compatible current group immediately. Initial traversal rechecks
// funds without consuming RPM; the final handler performs its normal admission.
// Runtime group fallback can include RPM accounting through the same resolver.

// runtimeGroupFallbackResolver returns an authorized destination Key and its
// resolved subscription.
type runtimeGroupFallbackResolver func(context.Context, *apikey.APIKey, int64, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error)

// provideFundingAdmission combines billing eligibility and scheduler-owned user
// RPM admission.
func provideFundingAdmission(funds *billing.Eligibility, rpm scheduler.UserRPMCache, rates billing.UserGroupRateRepository) *admission.FundingAdmission {
	limiter := scheduler.NewRPMAdmission(rpm, rates, scheduler.Diagnostics{Logf: logging.LegacyPrintf})
	return admission.NewFundingAdmission(funds, limiter)
}

// provideRuntimeGroupFallbackResolver binds destination validation with funding
// and RPM checks. It returns nil when keys or funding is absent; callers invoke
// the resolver after deciding which configured target group to try.
func provideRuntimeGroupFallbackResolver(keys *apikey.APIKeyService, funding *admission.FundingAdmission, subscriptions *billing.SubscriptionService, cache session.GatewayCache) runtimeGroupFallbackResolver {
	return provideGroupFallbackResolver(keys, funding, subscriptions, cache, false)
}

// provideGroupFallbackResolver creates destination validation and returns nil
// when keys or funding is absent. Its callback requires the current Key,
// resolves the requested destination, enforces the Key Token ceiling, and
// rejects unsupported client protocols before resolving enforced funding.
//
// After funding admission succeeds, session isolation is checked with the
// original Key's user identity and current execution hints. fundingOnly selects
// the funding-only recheck that skips RPM. The selected Key and subscription
// are returned together so callers can publish matching authorization and
// funding state.
func provideGroupFallbackResolver(keys *apikey.APIKeyService, funding *admission.FundingAdmission, subscriptions *billing.SubscriptionService, cache session.GatewayCache, fundingOnly bool) runtimeGroupFallbackResolver {
	if keys == nil || funding == nil {
		return nil
	}
	var subscriptionReader admission.SubscriptionReader
	if subscriptions != nil {
		subscriptionReader = subscriptions
	}
	isolate := messageSessionIsolation(cache)
	return func(ctx context.Context, key *apikey.APIKey, id int64, source protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
		// Resolve the destination first so protocol, funds, and isolation checks
		// consume an authorized group view rather than the current group's settings.
		resolved, err := keys.ResolveRuntimeGroup(ctx, key, id)
		if err != nil {
			return nil, nil, err
		}
		if err := keys.CheckTokenRateLimit(ctx, resolved); err != nil {
			return nil, nil, err
		}
		if resolved == nil || resolved.Group == nil || !resolved.Group.AllowsClientProtocol(source) {
			return nil, nil, apperror.Forbidden("PROTOCOL_NOT_ALLOWED", "fallback group does not allow this protocol")
		}
		funds, err := admission.ResolveFundingFromKey(ctx, resolved, subscriptionReader, true)
		if err != nil {
			return nil, nil, err
		}
		if err := funding.CheckKey(ctx, resolved, funds.Subscription, "", fundingOnly); err != nil {
			return nil, nil, err
		}
		hints := requeststate.ExecutionHintsFromContext(ctx)
		sessionUserID := key.UserID
		if key.User != nil {
			sessionUserID = key.User.ID
		}
		if err := isolate(ctx, resolved, sessionUserID, hints.SessionIsolationSource, hints.SessionIsolationHash); err != nil {
			return nil, nil, err
		}
		return resolved, funds.Subscription, nil
	}
}

// provideClientGroupFallbackResolver returns a client-restriction traversal
// using funding-only destination checks. Missing keys or funding produces a nil
// callback. A compatible starting group returns the current Key with a nil
// subscription; each followed hop replaces both values using destination
// authorization.
//
// The traversal records visited group IDs and checks the next target before
// following it, rejecting cycles and missing fallback configuration.
// Cancellation stops before the next iteration. Funding-only checks leave RPM
// accounting to final admission after the handler receives the selected group.
func provideClientGroupFallbackResolver(keys *apikey.APIKeyService, funding *admission.FundingAdmission, subscriptions *billing.SubscriptionService, cache session.GatewayCache) func(context.Context, *apikey.APIKey, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
	resolve := provideGroupFallbackResolver(keys, funding, subscriptions, cache, true)
	if resolve == nil {
		return nil
	}
	return func(ctx context.Context, key *apikey.APIKey, source protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
		current := key
		var subscription *billing.UserSubscription
		// Keep every traversed group ID so configured fallback cycles terminate
		// before another authorization and funding pass repeats the same destination.
		visited := map[int64]struct{}{}
		for current != nil && current.Group != nil {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if _, exists := visited[current.Group.ID]; exists {
				return nil, nil, apperror.Forbidden("GROUP_FALLBACK_CYCLE", "fallback group cycle detected")
			}
			visited[current.Group.ID] = struct{}{}
			if !current.Group.ClaudeCodeOnly || (source == protocol.ProtocolAnthropicMessages && requeststate.IsClaudeCodeClient(ctx)) {
				return current, subscription, nil
			}
			target := current.Group.FallbackGroupID
			if target == nil || *target <= 0 {
				return nil, nil, routing.ErrClaudeCodeOnly
			}
			if _, exists := visited[*target]; exists {
				return nil, nil, apperror.Forbidden("GROUP_FALLBACK_CYCLE", "fallback group cycle detected")
			}
			var err error
			// A restricted client follows the configured target after cycle checks.
			// Destination validation returns the matching Key and subscription for the next hop.
			current, subscription, err = resolve(ctx, current, *target, source)
			if err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, apperror.Forbidden("GROUP_REQUIRED", "API Key must be assigned to a group")
	}
}
