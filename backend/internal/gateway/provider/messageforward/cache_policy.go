package messageforward

import (
	"context"

	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// cache_policy.go selects Anthropic cache TTL injection and response usage
// classification.
// Anthropic cache TTL injection and usage classification answer different
// questions: global OAuth injection rewrites request lifetimes to one hour
// while classifying returned cache creation into the five-minute bucket,
// whereas an API Key's forced Claude 1h cache setting rewrites eligible Claude
// request lifetimes while preserving upstream one-hour usage billing. Provider
// overrides take precedence when the response reader asks how to classify cache
// creation tokens.
//
// Both settings and Key context state are read during each decision, so dynamic
// invalidation affects later requests. Message breakpoint rewriting has its own
// opt-in flag and removes existing message cache controls before adding
// selected breakpoints. The functions return delegated request bytes and leave
// response token extraction to the upstream reader.

// globalOAuthTTLInjection reports whether the global administrator setting
// enables one-hour request cache injection for an Anthropic OAuth or Setup
// Token target. Missing targets or settings readers disable that policy.
func (r *Runtime) globalOAuthTTLInjection(ctx context.Context, target *gatewayadapter.ExecutionProvider) bool {
	return target != nil && target.View().IsAnthropicOAuthOrSetupToken() && r.dependencies.Settings != nil && r.dependencies.Settings.IsAnthropicCacheTTL1hInjectionEnabled(ctx)
}

// injectTTL reports whether global OAuth injection or the authenticated API
// Key's forced Claude 1h cache setting applies to target and the supplied model
// candidates. Missing targets disable injection across both policies.
func (r *Runtime) injectTTL(ctx context.Context, target *gatewayadapter.ExecutionProvider, models ...string) bool {
	return r.globalOAuthTTLInjection(ctx, target) || gatewayadapter.ShouldForceClaude1hCache(ctx, target, models...)
}

// cacheUsageOverride selects the cache creation bucket requested by the
// response reader. A provider-specific override wins even when global injection
// is disabled, and a missing provider produces no override.
//
// When an eligible OAuth provider uses global one-hour injection, this function
// selects the five-minute usage bucket; API Key forced one-hour caching leaves
// the flag false so upstream one-hour cache creation bills normally.
func (r *Runtime) cacheUsageOverride(ctx context.Context, target *gatewayadapter.ExecutionProvider) (string, bool) {
	if target == nil {
		return "", false
	}
	if target.View().IsCacheTTLOverrideEnabled() {
		return target.View().GetCacheTTLOverrideTarget(), true
	}
	if r.globalOAuthTTLInjection(ctx, target) {
		return "5m", true
	}
	return "", false
}

// rewriteCache replaces message cache breakpoints when the runtime setting
// enables rewriting. Otherwise it returns the supplied slice, including
// existing client cache annotations and their TTL values.
func (r *Runtime) rewriteCache(ctx context.Context, body []byte) []byte {
	if r.dependencies.Settings == nil || !r.dependencies.Settings.IsRewriteMessageCacheControlEnabled(ctx) {
		return body
	}
	// Remove existing message anchors before selecting replacement
	// breakpoints so client TTL annotations do not remain attached
	// alongside the configured message-cache policy.
	return anthropic.AddMessageCacheBreakpoints(anthropic.StripMessageCacheControl(body))
}
