package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// fast_policy.go applies API Key Fast policy to Anthropic speed and beta
// signaling.
// Forced Fast mode depends on trusted Key policy and the selected group's
// effective price capability. The shared lookup also supplies OpenAI tier
// eligibility. Anthropic rewriting treats body speed and beta headers as one
// requested mode. Force-off removes Fast signaling, while eligible direct API
// Key traffic can gain speed and its beta token together.
//
// The ingress model retained in telemetry supplies pricing identity before a
// fallback model is considered, keeping capability checks tied to the group's
// price lookup. Rewritten header maps are cloned, and body mutation errors
// return the supplied body with the current header value. System beta policy
// remains a later decision, so Key force-on cannot by itself bypass beta
// filtering or blocking.

// APIKeyFastModePolicy reads and normalizes the authenticated access
// snapshot's Fast policy. Nil context or unrecognized stored policy selects
// follow_request, letting normal client tier input remain authoritative for
// that Key.
func APIKeyFastModePolicy(ctx context.Context) string {
	if ctx == nil {
		return apikey.APIKeyFastModePolicyFollowRequest
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	raw := access.FastModePolicy()
	policy, ok := apikey.NormalizeAPIKeyFastModePolicy(strings.TrimSpace(raw))
	// Invalid stored policy text supplies no trusted override. Follow request input
	// so malformed configuration cannot silently force a different Fast decision.
	if !ok {
		return apikey.APIKeyFastModePolicyFollowRequest
	}
	return policy
}

// WithAPIKeyFastModePolicy binds a normalized Key Fast policy to a derived
// access context. Nil context becomes Background, and unrecognized policy text
// becomes follow_request before the apikey helper creates its value.
func WithAPIKeyFastModePolicy(ctx context.Context, policy string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, ok := apikey.NormalizeAPIKeyFastModePolicy(strings.TrimSpace(policy))
	// Invalid stored policy text supplies no trusted override. Follow request input
	// so malformed configuration cannot silently force a different Fast decision.
	if !ok {
		normalized = apikey.APIKeyFastModePolicyFollowRequest
	}
	return apikey.WithFastModePolicy(ctx, normalized)
}

// FastModePricingModel prefers a nonblank ingress model from telemetry before
// the supplied fallback. Both returned names are trimmed, so pricing
// capability lookup can use the request's existing model identity.
func FastModePricingModel(ctx context.Context, fallback string) string {
	if ctx != nil {
		if model, ok := ctx.Value(telemetry.Model).(string); ok && strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
	}
	// Missing ingress identity leaves the caller's resolved model available as the
	// price lookup key; trim it using the same treatment as telemetry model text.
	return strings.TrimSpace(fallback)
}

// SupportsFastMode checks service-tier pricing capability for the context's
// positive group ID and ingress pricing model. Missing context, resolver,
// group, or resolved price returns false, preventing forced Fast injection
// with absent capability evidence.
func SupportsFastMode(ctx context.Context, resolver *billing.PriceResolver, model string) bool {
	resolved := resolveFastModePricing(ctx, resolver, model)
	return resolved != nil && resolved.SupportsServiceTier
}

// resolveFastModePricing reads the effective price for the admitted group and
// ingress model. An absent context, group ID, or price resolver leaves pricing
// unavailable for forced acceleration.
func resolveFastModePricing(ctx context.Context, resolver *billing.PriceResolver, model string) *billing.ResolvedPricing {
	if ctx == nil || resolver == nil {
		return nil
	}
	group, ok := requeststate.GroupFromContext(ctx)
	if !ok || group == nil || group.ID <= 0 {
		return nil
	}
	groupID := group.ID
	return resolver.Resolve(ctx, billing.PricingInput{
		Model:   FastModePricingModel(ctx, model),
		GroupID: &groupID,
	})
}

// SupportsAnthropicFastMode permits forced Fast capability for direct
// Anthropic API Key targets with compatible group pricing. Other credential
// types and missing targets return false, while SupportsFastMode supplies the
// model-price decision.
func SupportsAnthropicFastMode(ctx context.Context, resolver *billing.PriceResolver,

	provider *ExecutionProvider, model string,
) bool {
	return provider != nil && provider.View().IsAnthropic() && provider.Record.Type == capability.ProviderTypeAPIKey &&
		SupportsFastMode(ctx, resolver, model)
}

// addAnthropicBetaToken appends a missing beta token to the trimmed existing
// header. An already present token returns the original header, and blank
// header text produces the token alone.
func addAnthropicBetaToken(header, token string) string {
	if claude.ContainsBetaToken(header, token) {
		return header
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return token
	}
	return header + "," + token
}

// ApplyAnthropicFastMode resolves Key Fast policy and returns request bytes
// plus the header map used for forwarding. Force-off applies to Anthropic
// targets; other rewriting requires a direct Anthropic API Key target with
// group pricing support.
//
// Body speed and Fast beta token both count as client Fast requests when Key
// policy follows input or selects OpenAI-only Ultrafast. Force-on enables both
// signals, while force-off removes fast speed and its beta token. Other beta
// values survive. Header changes use a clone, body-update errors return the
// supplied bytes, and system beta filtering or blocking still runs afterward.
func ApplyAnthropicFastMode(
	ctx context.Context, resolver *billing.PriceResolver,

	provider *ExecutionProvider,
	model string,
	body []byte,
	headers http.Header,
) ([]byte, http.Header, error) {
	policy := APIKeyFastModePolicy(ctx)

	if policy == apikey.APIKeyFastModePolicyForceOff {
		if provider == nil || !provider.View().IsAnthropic() {
			return body, headers, nil
		}
		if headers == nil {
			headers = make(http.Header)
		}
		updated := body
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "speed").String()), "fast") {
			var err error
			updated, err = sjson.DeleteBytes(body, "speed")
			if err != nil {
				return body, headers, fmt.Errorf("remove Claude fast speed: %w", err)
			}
		}
		// Keep the caller's header map intact while removing Fast beta signaling;
		// the returned body and cloned headers carry the force-off decision together.
		cloned := headers.Clone()
		fastOnly := map[string]struct{}{claude.BetaFastMode: {}}
		claude.SetHeaderRaw(cloned, "anthropic-beta", claude.StripBetaTokensWithSet(claude.GetHeaderRaw(cloned, "anthropic-beta"), fastOnly))
		return updated, cloned, nil
	}

	if !SupportsAnthropicFastMode(ctx, resolver, provider, model) {
		return body, headers, nil
	}
	if headers == nil {
		headers = make(http.Header)
	}

	fastRequested := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "speed").String()), "fast") ||
		claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode)
	shouldFast := fastRequested
	if policy == apikey.APIKeyFastModePolicyForceOn {
		shouldFast = true
	}

	if shouldFast {
		updated, err := sjson.SetBytes(body, "speed", "fast")
		if err != nil {
			return body, headers, fmt.Errorf("set Claude fast speed: %w", err)
		}
		// Publish speed and its beta token together after speed encoding succeeds,
		// using cloned headers so caller-held request headers retain their existing values.
		cloned := headers.Clone()
		claude.SetHeaderRaw(cloned, "anthropic-beta", addAnthropicBetaToken(claude.GetHeaderRaw(cloned, "anthropic-beta"), claude.BetaFastMode))
		return updated, cloned, nil
	}

	return body, headers, nil
}
