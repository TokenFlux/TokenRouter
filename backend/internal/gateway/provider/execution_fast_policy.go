package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// execution_fast_policy.go binds request settings and provider scope to Fast
// tier evaluation.
// Fast request rules can be scoped to the authenticated payer and override
// global rules while retaining configuration order within each scope.
// Execution evaluation reads a context settings pointer when present or
// runtime settings otherwise; it folds tier case and whitespace, while
// tierpolicy.Resolve performs canonical tier validation before invoking its
// callback.

// openAIFastPolicyCtxKey stores a caller-supplied Fast settings pointer for
// contexts derived from one request or WS session.
var openAIFastPolicyCtxKey = openAIFastPolicyCtxKeyType{}

// ExecutionFastPolicy supplies runtime Fast settings and model-price
// capability checks to tier decisions.
type ExecutionFastPolicy struct {
	// Readers supplies live settings when no context pointer is bound. Nil Readers
	// causes Evaluate to pass before attempting a runtime settings read.
	Readers *RuntimeReaders
	Prices  *billing.PriceResolver // forced Fast price capability reader
}

// openAIFastPolicyCtxKeyType identifies a borrowed settings pointer in request
// context.
type openAIFastPolicyCtxKeyType struct{}

// Evaluate folds tier case and whitespace, then evaluates Fast settings with
// the trusted payer ID and provider scope. Missing readers, blank tiers, read
// errors, or absent settings return pass.
//
// User-specific rules precede global rules, and each set selects its first
// qualifying rule before model whitelist action is resolved. Context settings
// are read by reference; callers keep them immutable during evaluation. Direct
// callers can supply unknown nonblank tiers, which an all-tier rule can match;
// tierpolicy.Resolve canonicalizes its input before calling this adapter.
func (s *ExecutionFastPolicy) Evaluate(ctx context.Context, provider *ExecutionProvider, model, serviceTier string) (action, errMsg string) {
	if s == nil || s.Readers == nil {
		return anthropic.BetaPolicyActionPass, ""
	}
	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	if tier == "" {
		return anthropic.BetaPolicyActionPass, ""
	}
	settings := FastPolicySettingsFromContext(ctx)
	if settings == nil {
		fetched, err := s.Readers.Gateway.GetOpenAIFastPolicySettings(ctx)
		// Unavailable settings cannot supply a matching rule. Pass the tier through
		// so request processing continues under the caller's remaining policy stages.
		if err != nil || fetched == nil {
			return anthropic.BetaPolicyActionPass, ""
		}
		settings = fetched
	}
	return tierpolicy.Evaluate(settings, openAIFastPolicyUserID(ctx), provider != nil && provider.View().IsOAuth(), provider != nil && provider.View().IsBedrock(), model, tier)
}

// openAIFastPolicyUserID returns the positive payer ID from the authenticated
// access snapshot. Nil contexts, absent snapshots, and nonpositive IDs return
// zero so user-scoped Fast rules have no trusted payer match.
func openAIFastPolicyUserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	userID := access.PayerUserID
	if userID <= 0 {
		return 0
	}
	return userID
}

// WithFastPolicyContext binds the supplied settings pointer to ctx for
// subsequent evaluation. Nil context or settings returns ctx unchanged; the
// pointer is borrowed, so callers keep its rules immutable while derived
// contexts use it.
func WithFastPolicyContext(ctx context.Context, settings *tierpolicy.OpenAIFastPolicySettings) context.Context {
	if ctx == nil || settings == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIFastPolicyCtxKey, settings)
}

// FastPolicySettingsFromContext returns the stored settings pointer when its
// context value has the expected type. Nil contexts or other values return
// nil, letting Evaluate read runtime settings when no snapshot is available.
func FastPolicySettingsFromContext(ctx context.Context) *tierpolicy.OpenAIFastPolicySettings {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(openAIFastPolicyCtxKey).(*tierpolicy.OpenAIFastPolicySettings); ok {
		return v
	}
	return nil
}

// GroupFastPolicy reads the admitted group's effective Fast policy for OpenAI
// targets. Missing context or target, another platform, or an invalid group
// context selects follow_request instead of applying unavailable group data.
func GroupFastPolicy(ctx context.Context, provider *ExecutionProvider) string {
	if ctx == nil || provider == nil || !provider.View().IsOpenAI() {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	group, _ := requeststate.GroupFromContext(ctx)
	if !routing.IsGroupContextValid(group) {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	return group.EffectiveOpenAIFastPolicy()
}

// Input binds group, Key, settings evaluation, and forced-Fast capability
// callbacks for tierpolicy.Resolve. The callbacks read their context and
// provider when invoked, so callers keep those request inputs stable during a
// decision.
func (s *ExecutionFastPolicy) Input(ctx context.Context, value *ExecutionProvider, model string) tierpolicy.DecisionInput {
	return tierpolicy.DecisionInput{
		Model: model, GroupPolicy: GroupFastPolicy(ctx, value), OpenAI: value != nil && value.View().IsOpenAI(),
		Evaluate: func(tier string) (string, string) { return s.Evaluate(ctx, value, model, tier) },
		KeyPolicy: func() string {
			return APIKeyFastModePolicy(ctx)
		},
		CanForceTier: func(tier string) bool {
			return s.canForceTier(ctx, value, model, tier)
		},
	}
}

// canForceTier checks the selected OpenAI target against effective group
// pricing. Priority accepts the model's service-tier flag. Ultrafast requires
// a specific price declaration, and missing prices or other tiers are ineligible.
func (s *ExecutionFastPolicy) canForceTier(ctx context.Context, provider *ExecutionProvider, model, tier string) bool {
	if provider == nil || !provider.View().IsOpenAI() {
		return false
	}
	switch tier {
	case tierpolicy.OpenAIFastTierPriority:
		return SupportsFastMode(ctx, s.Prices, model)
	case tierpolicy.OpenAIFastTierUltrafast:
		resolved := resolveFastModePricing(ctx, s.Prices, model)
		return resolved.SupportsUltrafast()
	default:
		return false
	}
}
