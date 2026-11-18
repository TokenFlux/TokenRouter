package completion

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// normalize.go reconciles requested and observed service tiers and normalizes
// image billing dimensions. An upstream tier declaration can show that a
// requested premium tier was served at a cheaper level. Billing accepts known
// downward changes and retains the request for equal, more expensive, unknown,
// or absent declarations. OAuth-like OpenAI providers treat a default response
// echo as nonauthoritative, while an explicit cheaper tier still changes the
// recorded charge category.
//
// Image dimensions are normalized before cost calculation so decoded output
// sizes can determine the billing tier. normalizeResult mutates the captured
// result, then emits a downgrade event using the executing provider's identity.
// That identity can differ from the credential parent used to decide the OpenAI
// tier exception, so callers supply both observations separately.

// ServiceTierBillingResolution records normalized request, response, and charge
// tiers selected for a completed attempt.
type ServiceTierBillingResolution struct {
	// Requested and Observed keep normalized evidence even when Billing retains
	// the request; Downgraded identifies the cheaper accepted response tier.
	Requested  string // normalized outbound tier
	Observed   string // normalized response tier
	Billing    string // selected billing tier
	Downgraded bool   // accepted cheaper response
}

// ResolveBillingServiceTier selects a charge tier from outbound and response
// evidence. Both inputs are trimmed and normalized; absent or identical response
// evidence retains the requested tier.
//
// Only a known response rank lower than the request rank produces a downgrade.
// Unknown request names use the standard comparison rank, and unknown response
// names are ignored. Equal-ranked aliases retain the request spelling after
// normalization, so a response cannot raise the charge category or replace it
// merely by naming an equivalent tier.
func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	resolution := ServiceTierBillingResolution{Requested: requested, Observed: observed, Billing: requested}
	if observed == "" || observed == requested {
		return resolution
	}
	// Accept cheaper recognized service only; an unknown or more expensive response
	// cannot establish a larger user charge than the outbound request selected.
	observedRank, known := ServiceTierCostRank(observed)
	if !known {
		return resolution
	}
	requestedRank, _ := ServiceTierCostRank(requested)
	if observedRank >= requestedRank {
		return resolution
	}
	resolution.Billing = observed
	resolution.Downgraded = true
	return resolution
}

// ServiceTierCostRank reports the normalized tier's relative billing category.
// Flex ranks below standard aliases, then Priority and Ultrafast rank higher.
// Unknown names return the standard rank with known=false for caller selection rules.
func ServiceTierCostRank(tier string) (rank int, known bool) {
	switch normalizeBillingServiceTier(tier) {
	case "flex":
		return 0, true
	case "", "default", "standard", "auto", "scale":
		return 1, true
	case "priority", "fast":
		return 2, true
	case "ultrafast":
		return 3, true
	default:
		return 1, false
	}
}

// applyCacheOverride reclassifies cache-write detail on a nonnil usage value.
// The exact target "1h" selects that bucket; other targets select five minutes.
// Aggregate counts stay unchanged, and initializing missing detail alone returns false.
func applyCacheOverride(usage *TokenUsage, target string) bool {
	projected := protocol.TokenUsage{CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheCreation5mTokens: usage.CacheCreation5mTokens, CacheCreation1hTokens: usage.CacheCreation1hTokens}
	// Protocol usage owns aggregate-versus-detail handling; copy back only TTL detail
	// so this billing adjustment leaves input, output, and aggregate observations intact.
	changed := protocol.ApplyCacheTTLOverride(&projected, target)
	usage.CacheCreation5mTokens = projected.CacheCreation5mTokens
	usage.CacheCreation1hTokens = projected.CacheCreation1hTokens
	return changed
}

// normalizeResult prepares captured image dimensions and the charge tier before
// pricing. Nil results yield an empty resolution. Image output evidence is used
// for image usage, except that OpenAI video usage skips that image path.
//
// The generic path can derive the requested tier from Anthropic Speed. OpenAI
// uses billing-provider OAuth eligibility, while downgrade events name the
// executing observedProvider. Only accepted downgrades replace ServiceTier and an
// existing Anthropic Speed; other resolutions retain the result's pointer and
// raw speed evidence. Callers own the captured result during these mutations.
func (s *Recorder) normalizeResult(r *Result, a *ProviderSnapshot, openAI bool, observedProvider *ProviderSnapshot, requestID string) ServiceTierBillingResolution {
	if r == nil {
		return ServiceTierBillingResolution{}
	}
	// Decoded output sizes can override the caller hint; the synthetic 2K default
	// is excluded as input evidence so it cannot outrank actual output observations.
	if r.ImageCount > 0 && (!openAI || r.VideoCount <= 0) {
		input := strings.TrimSpace(r.ImageInputSize)
		if input == "" && strings.TrimSpace(r.ImageSize) != pricing.ImageBillingSize2K {
			input = strings.TrimSpace(r.ImageSize)
		}
		sizes := r.ImageOutputSizes
		if len(sizes) == 0 && strings.TrimSpace(r.ImageOutputSize) != "" {
			sizes = []string{r.ImageOutputSize}
		}
		v := pricing.ResolveImageBillingSize(input, sizes)
		r.ImageSize, r.ImageInputSize, r.ImageOutputSize, r.ImageSizeSource, r.ImageSizeBreakdown = v.BillingSize, v.InputSize, v.OutputSize, v.Source, v.Breakdown
	}
	requested := stringValueOrEmpty(r.ServiceTier)
	if !openAI && requested == "" {
		requested = strings.TrimSpace(r.Usage.Speed)
	}
	resolution := ResolveBillingServiceTier(requested, r.UpstreamResponseServiceTier)
	if openAI {
		resolution = ResolveOpenAIServiceTierBilling(a != nil && a.OAuthLike, requested, r.UpstreamResponseServiceTier)
	}
	// Publish a cheaper charge category into the captured result before pricing and
	// logging consume it; retained raw response evidence still explains the decision.
	if resolution.Downgraded {
		tier := resolution.Billing
		r.ServiceTier = &tier
		if !openAI && r.Usage.Speed != "" {
			r.Usage.Speed = tier
		}
		event := BillingEvent{Kind: "tier_downgrade", Component: "service.gateway", RequestID: strings.TrimSpace(requestID), RequestedTier: resolution.Requested, ObservedTier: resolution.Observed, BilledTier: resolution.Billing}
		if openAI {
			event.Component = "service.openai_gateway"
		}
		if observedProvider != nil {
			event.ProviderID = observedProvider.ID
			event.Platform = observedProvider.Platform
		}
		s.observeEvent(event)
	}
	return resolution
}

// ResolveOpenAIServiceTierBilling applies OpenAI's OAuth default-echo exception.
// For OAuth-like providers, normalized "default" retains the outbound charge tier;
// all other observations use the generic known-downward-tier selection rule.
func ResolveOpenAIServiceTierBilling(oauthLike bool, requested, observed string) ServiceTierBillingResolution {
	// OAuth default echoes do not establish a cheaper served tier; retain outbound
	// evidence while still allowing explicit flex or standard observations below.
	if oauthLike && CodexOAuthResponseTierIsNonAuthoritative(observed) {
		return ServiceTierBillingResolution{Requested: normalizeBillingServiceTier(requested), Observed: normalizeBillingServiceTier(observed), Billing: normalizeBillingServiceTier(requested)}
	}
	return ResolveBillingServiceTier(requested, observed)
}

// CodexOAuthResponseTierIsNonAuthoritative reports whether normalized response
// evidence is the OpenAI OAuth default echo excluded from downgrade selection.
func CodexOAuthResponseTierIsNonAuthoritative(observed string) bool {
	return normalizeBillingServiceTier(observed) == "default"
}
