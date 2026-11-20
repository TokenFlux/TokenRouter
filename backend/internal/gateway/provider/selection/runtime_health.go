package selection

import (
	"context"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// runtime_health.go checks credential and model cooldown state for compatible
// provider requests.
// Compatible runtime eligibility observes both credential failures and
// provider-model cooldowns. Credential checks use the current refresh identity,
// while model checks resolve the provider's canonical scheduling name before
// querying the shared transient store at the current time.

// isOpenAIProvider reports whether a target belongs to OpenAI or Grok for
// compatible credential-block checks. Nil targets return false, so those checks
// can skip absent records before accessing their runtime identity.
func isOpenAIProvider(provider *gatewayprovider.ExecutionProvider) bool {
	return provider != nil && (provider.Record.Platform == capability.PlatformOpenAI || provider.Record.Platform == capability.PlatformGrok)
}

// isOpenAIProviderRuntimeBlocked checks credential failure state for OpenAI and
// Grok targets. Nil selectors and other platforms pass, and the store receives
// the target's current refresh identity to distinguish replaced credentials.
func (s *Compatible) isOpenAIProviderRuntimeBlocked(provider *gatewayprovider.ExecutionProvider) bool {
	if s == nil || !isOpenAIProvider(provider) {
		return false
	}
	return s.runtimeBlockState().Blocked(provider.Record.ID, func() string {
		return providercore.RefreshCredentialIdentity(gatewayprovider.ExecutionRecord(provider))
	})
}

// openAIProviderModelTransientModel delegates normalization of scheduling names
// used by transient cooldown observations.
func openAIProviderModelTransientModel(canonicalModel string) string {
	return providercore.NormalizeTransientModel(canonicalModel)
}

// clearOpenAIProviderModelTransientState records success for the supplied
// provider and model in the bound transient store. An absent store is ignored,
// letting successful basic requests clear available observations too.
func (s *Compatible) clearOpenAIProviderModelTransientState(providerID int64, model string) {
	state := s.getOpenAIProviderModelTransientState()
	if state == nil {
		return
	}
	state.RecordSuccess(providerID, model)
}

// isOpenAIProviderModelRuntimeBlocked checks the bound transient store using
// the target's effort-aware canonical scheduling model. Nil selectors, nil
// providers, or an absent store return false; active cooldowns are evaluated
// at the current time.
func (s *Compatible) isOpenAIProviderModelRuntimeBlocked(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	if s == nil || provider == nil {
		return false
	}
	state := s.getOpenAIProviderModelTransientState()
	if state == nil {
		return false
	}
	canonicalModel := gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).CanonicalSchedulingModel(requestedModel)
	return state.IsBlocked(provider.Record.ID, openAIProviderModelTransientModel(canonicalModel), time.Now())
}

// isOpenAIProviderRequestRuntimeBlocked reports either a credential block or a
// mapped-model cooldown for this target. A nil selector returns false, and the
// credential check short-circuits a later model lookup.
func (s *Compatible) isOpenAIProviderRequestRuntimeBlocked(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	return s != nil && (s.isOpenAIProviderRuntimeBlocked(provider) || s.isOpenAIProviderModelRuntimeBlocked(ctx, provider, requestedModel))
}
