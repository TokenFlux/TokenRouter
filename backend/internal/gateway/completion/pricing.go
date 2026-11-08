package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// pricing.go selects charge modes and calculates costs from captured token,
// media, and search usage. Observed media counts do not by themselves select
// the charge mode. An effective configured token card can charge image or video
// usage as tokens, while ordinary media cards charge image units or video
// seconds. The OpenAI path selects dedicated Alpha Search and audio operations
// before general token-plus-search handling; the native path gives positive
// image usage its own first decision.
//
// OpenAI token pricing tries the supplied candidates in order, then reports the
// last failure if none produces a cost. The production candidate selector normally
// supplies one configured billing identity. A search surcharge cannot conceal a
// missing model price, while an entirely model-free tool request can charge search
// alone. Native helpers log calculation errors and return zero-cost placeholders;
// OpenAI helpers return errors for the recorder's pricing-failure policy.

// CalculateRecordUsageCost selects native-bucket pricing from captured media,
// audio, or token observations. Callers provide a nonnil result and calculator;
// a bound resolver also requires a nonnil key when token pricing can be selected.
// Nil options use a zero pricing instant.
//
// Positive image counts prefer configured token mode, another configured mode,
// then catalogue token evidence or per-image pricing. Audio can use configured
// per-request units and falls back to operation prices when that configured
// calculation fails. Other results combine model costs with positive search costs.
// The supplied multiplier handles token, audio, and search work; imageMultiplier
// is the separate media factor, so callers choose their applicable funding factors.
func (s *Recorder) CalculateRecordUsageCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	provider *ProviderSnapshot,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	groupMappedModel string,
	multiplier float64,
	imageMultiplier float64,
	opts *PricingOptions,
) *CostBreakdown {
	if opts == nil {
		opts = &PricingOptions{}
	}
	// The configured mode determines whether counts or measured tokens are charged;
	// retain image units only after the token-card and catalogue-evidence decisions decline.
	if result.ImageCount > 0 {
		resolved, pricingModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
		if resolved != nil && resolved.Mode == BillingModeToken {
			return s.CalculateTokenCost(ctx, result, apiKey, provider, billingModel, requestedModel, billingModelSource, groupMappedModel, multiplier, opts)
		} else if resolved != nil {
			return s.CalculateImageCost(ctx, result, apiKey, provider, billingModel, requestedModel, billingModelSource, groupMappedModel, pricingModel, resolved, imageMultiplier, opts.PricingAt)
		}
		if s.imageUsesTokenPricing(pricingModel, result.ImageSize, result.Usage) {
			return s.CalculateTokenCost(ctx, result, apiKey, provider, billingModel, requestedModel, billingModelSource, groupMappedModel, multiplier, opts)
		}
		return s.CalculateImageCost(ctx, result, apiKey, provider, billingModel, requestedModel, billingModelSource, groupMappedModel, billingModel, nil, imageMultiplier, opts.PricingAt)
	}

	// Audio result units already use the operation's measurement scale; a configured
	// request card can price those units before operation-default resolution is needed.
	if result.AudioUsage != nil {
		resolved, pricingModel := s.resolveConfigPricingForUsage(
			ctx, billingModel, apiKey,
		)
		if resolved != nil && resolved.Mode == BillingModePerRequest {
			gid := apiKey.Group.ID
			cost, err := s.billingService.CalculateCostUnified(CostInput{
				Ctx:     ctx,
				Model:   pricingModel,
				GroupID: &gid,

				UsageUnits:     result.AudioUsage.DurationOrUnits,
				SizeTier:       result.AudioUsage.Mode,
				RateMultiplier: multiplier,
				PricingAt:      opts.PricingAt,
				Resolver:       s.resolver,
				Resolved:       resolved,
			})
			if err == nil {
				return cost
			}
		}
		// Absent or failed native configured audio pricing uses the operation settings
		// and catalogue defaults, whose missing-price handling remains with the calculator.
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, multiplier)
	}

	// Search retains its own measured operation cost after native model pricing;
	// positive surcharges add to totals even when that model yielded a zero placeholder.
	tokenCost := s.CalculateTokenCost(ctx, result, apiKey, provider, billingModel, requestedModel, billingModelSource, groupMappedModel, multiplier, opts)
	if result.SearchCount > 0 {
		price := groupSearchPricePer1kFromAPIKey(apiKey)
		if price != nil && *price == 0 {
			s.printf("service.gateway", "[Billing] search_price_per_1k explicit 0; search free group_model=%s count=%d", billingModel, result.SearchCount)
		}
		searchCost := s.billingService.CalculateSearchCost(result.SearchCount, price, multiplier)
		if searchCost != nil && (searchCost.TotalCost > 0 || searchCost.ActualCost > 0) {
			if tokenCost == nil {
				return searchCost
			}
			tokenCost.TotalCost += searchCost.TotalCost
			tokenCost.ActualCost += searchCost.ActualCost
		}
	}
	return tokenCost
}

// ResolveConfigPricing returns configured pricing for the selected model and
// the key's group snapshot ID. Missing resolver, key, or group yields nil; a
// catalogue-only resolution also returns nil for caller-specific catalogue handling.
func (s *Recorder) ResolveConfigPricing(ctx context.Context, billingModel string, apiKey *KeySnapshot) *ResolvedPricing {
	resolved, _ := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
	return resolved
}

// CalculateImageCost charges native image usage through supplied or resolved
// configured pricing, then catalogue per-image rates when unconfigured. Callers
// provide a nonnil result and calculator; configured pricing also requires key.Group.
// Errors are logged and converted to zero-cost placeholders, whose mode is image
// on the catalogue-error path.
func (s *Recorder) CalculateImageCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	provider *ProviderSnapshot,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	groupMappedModel string,
	resolvedModel string,
	resolved *ResolvedPricing,
	multiplier float64,
	pricingAt time.Time,
) *CostBreakdown {
	sizeTier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	if resolved == nil {
		resolved, resolvedModel = s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
	}
	if resolved != nil {
		tokens := UsageTokens{
			InputTokens:       result.Usage.InputTokens,
			OutputTokens:      result.Usage.OutputTokens,
			ImageOutputTokens: result.Usage.ImageOutputTokens,
		}
		gid := apiKey.Group.ID
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx:     ctx,
			Model:   resolvedModel,
			GroupID: &gid,

			Tokens:         tokens,
			RequestCount:   imageBillingUnits(result, resolved),
			SizeTier:       sizeTier,
			RateMultiplier: multiplier,
			PricingAt:      pricingAt,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
		if err != nil {
			// Keep an analysis-compatible zero placeholder after configured image failure;
			// the recorder can retain the image count even though this price was unavailable.
			s.printf("service.gateway", "Calculate image token cost failed: %v", err)
			return &CostBreakdown{ActualCost: 0}
		}
		return cost
	}

	cost, err := s.billingService.CalculateImageCost(resolvedModel, sizeTier, imageBillingUnits(result, nil), multiplier)
	if err != nil {
		// Missing catalogue image pricing retains image mode for the measured output
		// row, leaving the actual charge at zero and reporting the failed price lookup.
		s.printf("service.gateway", "Calculate image cost failed: %v", err)
		return &CostBreakdown{BillingMode: string(BillingModeImage)}
	}
	return cost
}

// CalculateTokenCost prices separate native input, cache, output, and image-output
// buckets for the selected billing model. Nil options use a zero supplied pricing
// instant, and callers provide a nonnil result and calculator. A bound resolver
// also requires a nonnil key for its group/catalogue selection.
//
// Configured pricing or an available group resolver uses CalculateCostUnified with
// service tier and final effort. The catalogue path applies a separate max-effort
// factor after service-tier pricing; a failed second price read leaves that factor
// at its default. Calculation errors are logged and return a zero token-mode
// placeholder, allowing measured work to continue into record construction.
func (s *Recorder) CalculateTokenCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	provider *ProviderSnapshot,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	groupMappedModel string,
	multiplier float64,
	opts *PricingOptions,
) *CostBreakdown {
	serviceTier := ForwardServiceTier(result)
	tokens := UsageTokens{
		InputTokens:           result.Usage.InputTokens,
		OutputTokens:          result.Usage.OutputTokens,
		CacheCreationTokens:   result.Usage.CacheCreationInputTokens,
		CacheReadTokens:       result.Usage.CacheReadInputTokens,
		CacheCreation5mTokens: result.Usage.CacheCreation5mTokens,
		CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
		ImageOutputTokens:     result.Usage.ImageOutputTokens,
	}

	var cost *CostBreakdown
	var err error
	if opts == nil {
		opts = &PricingOptions{}
	}

	// Use the selected model's configured card before catalogue-only calculation;
	// another route identity cannot supply its price after this source was selected.
	if resolved, resolvedModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey); resolved != nil {
		gid := apiKey.Group.ID
		cost, err = s.billingService.CalculateCostUnified(CostInput{
			Ctx:     ctx,
			Model:   resolvedModel,
			GroupID: &gid,

			Tokens:          tokens,
			RequestCount:    1,
			RateMultiplier:  multiplier,
			PricingAt:       opts.PricingAt,
			ServiceTier:     serviceTier,
			ReasoningEffort: stringValueOrEmpty(result.ReasoningEffort),
			Resolver:        s.resolver,
			Resolved:        resolved,
		})
	} else {
		switch {
		case s.resolver != nil && apiKey.Group != nil:
			gid := apiKey.Group.ID
			cost, err = s.billingService.CalculateCostUnified(CostInput{
				Ctx:     ctx,
				Model:   billingModel,
				GroupID: &gid,

				Tokens:          tokens,
				RequestCount:    1,
				RateMultiplier:  multiplier,
				PricingAt:       opts.PricingAt,
				ServiceTier:     serviceTier,
				ReasoningEffort: stringValueOrEmpty(result.ReasoningEffort),
				Resolver:        s.resolver,
			})
		default:
			cost, err = s.billingService.CalculateCostWithServiceTier(billingModel, tokens, multiplier, serviceTier)
			if err == nil {
				// The successful base cost remains usable if this later catalogue read fails;
				// nil price evidence leaves the optional max-effort factor at one.
				price, _ := s.billingService.GetModelPricing(billingModel)
				applyCostBreakdownMultiplier(cost, maxReasoningEffortBillingMultiplier(billingModel, stringValueOrEmpty(result.ReasoningEffort), price))
			}
		}
	}
	if err != nil {
		// Preserve measured native usage after a pricing error with a token-mode
		// zero cost, so record construction can still retain a reconcilable request fact.
		s.printf("service.gateway", "Calculate cost failed: %v", err)
		return &CostBreakdown{BillingMode: string(BillingModeToken)}
	}
	if cost != nil && cost.BillingMode == "" {
		cost.BillingMode = string(BillingModeToken)
	}
	return cost
}

// CalculateOpenAIRecordUsageCostAt selects a charge from already separated token
// buckets and captured operation quantities. Positive Alpha Search calls take the
// dedicated per-call path before media, audio, or general token processing.
// Callers provide a calculator and a key when token pricing can use a bound
// resolver; a nil result still permits supplied model candidates and token buckets.
//
// Video and image observations use media pricing unless configured token mode,
// or qualifying catalogue image-token evidence, chooses tokens. Audio uses
// configured per-request units or operation prices. These paths use the supplied
// media/base factors; general token costs use multiplier and pricingAt.
//
// Token candidates are tried in order, retaining the last error. Positive search
// cost is added only after a token cost succeeds when a model was supplied; a
// model-free tool request can return search cost alone. A wholly unpriced selected
// model therefore reports an error even when a search surcharge is available.
func (s *Recorder) CalculateOpenAIRecordUsageCostAt(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	pricingAt time.Time,
) (*CostBreakdown, error) {
	billingModel := firstUsageBillingModel(billingModels)
	if result != nil && result.WebSearchCalls > 0 {
		// Alpha Search calls select their dedicated operation charge even if other
		// quantities are present. The calculator reads configured or catalogue defaults;
		// absent defaults are warned and yield zero rather than a fixed built-in rate.
		return s.billingService.CalculateWebSearchCost(result.WebSearchCalls, webSearchPricePerCallFromAPIKey(apiKey), webSearchMultiplier), nil
	}
	if IsGrokVideoUsageResult(result, billingModels) {
		if resolved := s.ResolveConfigPricing(ctx, billingModel, apiKey); resolved == nil || resolved.Mode != BillingModeToken {
			return s.CalculateOpenAIVideoCost(ctx, billingModel, apiKey, result, videoMultiplier)
		}
	}
	if result != nil && result.AudioUsage != nil {
		if resolved := s.ResolveConfigPricing(ctx, billingModel, apiKey); resolved != nil &&
			(resolved.Mode == BillingModePerRequest) {
			gid := apiKey.Group.ID
			return s.billingService.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: billingModel, GroupID: &gid,
				UsageUnits: result.AudioUsage.DurationOrUnits, SizeTier: result.AudioUsage.Mode,
				RateMultiplier: webSearchMultiplier, Resolver: s.resolver, Resolved: resolved,
			})
		}
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, webSearchMultiplier), nil
	}

	if result != nil && result.ImageCount > 0 {
		// Positive image quantity can still use a token card; catalogue token pricing
		// requires observed usage and a failed per-image lookup before this media path declines.
		resolved, lookupModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
		if resolved != nil && resolved.Mode != BillingModeToken {
			return s.CalculateOpenAIImageCost(ctx, billingModel, apiKey, result, imageMultiplier)
		}
		if resolved == nil && !s.imageUsesTokenPricing(lookupModel, result.ImageSize, result.Usage) {
			return s.CalculateOpenAIImageCost(ctx, billingModel, apiKey, result, imageMultiplier)
		}
	}

	// Keep the last candidate failure until a model cost succeeds, so a positive
	// search surcharge cannot turn missing selected-model pricing into a priced request.
	var tokenCost *CostBreakdown
	var lastErr error
	if len(billingModels) > 0 && billingModel != "" {
		for _, candidate := range billingModels {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			cost, err := s.CalculateOpenAIRecordUsageTokenCostAt(
				ctx,
				apiKey,
				candidate,
				multiplier,
				pricingAt,
				tokens,
				serviceTier,
				ForwardResultReasoningEffort(result),
			)
			if err == nil {
				tokenCost = cost
				if tokenCost != nil && tokenCost.BillingMode == "" {
					tokenCost.BillingMode = string(BillingModeToken)
				}
				break
			}
			lastErr = err
		}
	}

	var searchCost *CostBreakdown
	if result != nil && result.SearchCount > 0 {
		price := groupSearchPricePer1kFromAPIKey(apiKey)
		if price != nil && *price == 0 {
			s.observeEvent(BillingEvent{Kind: "search_free", SearchCount: result.SearchCount, Model: billingModel, KeyID: apiKey.ID, GroupID: apiKey.GroupID})
		}
		searchCost = s.billingService.CalculateSearchCost(result.SearchCount, price, webSearchMultiplier)
	}

	tokenBillingAttempted := len(billingModels) > 0 && billingModel != ""
	if tokenCost == nil {
		if tokenBillingAttempted {
			if lastErr == nil {
				lastErr = fmt.Errorf("%w: no non-empty billing model candidates", ErrModelPricingUnavailable)
			}
			return nil, fmt.Errorf("calculate OpenAI usage cost failed for billing models %s: %w", strings.Join(billingModels, ","), lastErr)
		}
		// No model was offered for token pricing, so search can describe the complete
		// tool-only charge; a supplied but unpriced model took the earlier error branch.
		if searchCost != nil {
			return searchCost, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("%w: openai usage billing model is empty", ErrModelPricingUnavailable)
		}
		return nil, fmt.Errorf("calculate OpenAI usage cost failed for billing models %s: %w", strings.Join(billingModels, ","), lastErr)
	}
	if searchCost == nil || (searchCost.TotalCost == 0 && searchCost.ActualCost == 0) {
		return tokenCost, nil
	}

	tokenCost.TotalCost += searchCost.TotalCost
	tokenCost.ActualCost += searchCost.ActualCost
	return tokenCost, nil
}

// IsGrokVideoUsageResult reports whether a nonnil result has positive VideoCount.
// Positive video quantity also qualifies results whose billing model was renamed
// or whose candidate names identify an unknown model family.
func IsGrokVideoUsageResult(result *Result, billingModels []string) bool {
	return result != nil && result.VideoCount > 0
}

// IsUsagePricingUnavailableError recognizes the shared missing-price sentinel
// through error wrapping and two case-folded legacy message phrases. Nil returns
// false; other errors are distinguished so RecordOpenAI can return them before settlement.
func IsUsagePricingUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrModelPricingUnavailable) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no pricing available") || strings.Contains(msg, "pricing not found")
}

// CalculateOpenAIRecordUsageTokenCostAt delegates one candidate's measured buckets
// to unified pricing with service tier and final effort. An available group
// resolver receives pricingAt; the catalogue-only call omits that supplied instant.
// Callers provide a calculator and handle the returned pricing error. A bound
// resolver also requires a nonnil key before checking its group.
func (s *Recorder) CalculateOpenAIRecordUsageTokenCostAt(
	ctx context.Context,
	apiKey *KeySnapshot,
	billingModel string,
	multiplier float64,
	pricingAt time.Time,
	tokens UsageTokens,
	serviceTier string,
	reasoningEffort string,
) (*CostBreakdown, error) {
	if s.resolver != nil && apiKey.Group != nil {
		gid := apiKey.Group.ID
		return s.billingService.CalculateCostUnified(CostInput{
			Ctx:     ctx,
			Model:   billingModel,
			GroupID: &gid,

			Tokens:          tokens,
			RequestCount:    1,
			RateMultiplier:  multiplier,
			PricingAt:       pricingAt,
			ServiceTier:     serviceTier,
			ReasoningEffort: reasoningEffort,
			Resolver:        s.resolver,
		})
	}
	return s.billingService.CalculateCostUnified(CostInput{
		Ctx: ctx, Model: billingModel, Tokens: tokens, RateMultiplier: multiplier,
		ServiceTier: serviceTier, ReasoningEffort: reasoningEffort,
	})
}

// ForwardResultReasoningEffort reads the captured final effort as supplied.
// Nil result or effort pointer returns empty, and this helper does not trim or
// normalize it; the pricing consumer applies its own effort rules.
func ForwardResultReasoningEffort(result *Result) string {
	if result == nil || result.ReasoningEffort == nil {
		return ""
	}
	return *result.ReasoningEffort
}

// imageUsesTokenPricing reports whether image helpers should select catalogue
// token pricing when configured pricing is unavailable. Callers provide a
// calculator and a selected billing model alongside captured size and usage evidence.
//
// At least one ordinary, cache, or image bucket must be positive. A successful
// per-image lookup keeps media pricing; a failed lookup permits token selection
// only when GetModelPricing succeeds. That successful lookup is enough for this
// availability check, even when the later token calculation can still fail.
func (s *Recorder) imageUsesTokenPricing(model, size string, usage TokenUsage) bool {
	if usage.InputTokens <= 0 && usage.OutputTokens <= 0 && usage.CacheReadInputTokens <= 0 && usage.CacheCreationInputTokens <= 0 && usage.ImageInputTokens <= 0 && usage.ImageOutputTokens <= 0 {
		return false
	}
	if _, err := s.billingService.DefaultImagePrice(model, NormalizeImageBillingTierOrDefault(size)); err == nil {
		return false
	}
	_, err := s.billingService.GetModelPricing(model)
	return err == nil
}

// CalculateOpenAIImageCost prices a nonnil image result using normalized size,
// configured card units, or catalogue per-image rates. Callers supply the key and
// calculator; failures return both a mode-tagged zero breakdown and the pricing error.
func (s *Recorder) CalculateOpenAIImageCost(ctx context.Context, billingModel string, apiKey *KeySnapshot, result *Result, multiplier float64) (*CostBreakdown, error) {
	sizeTier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	resolved, lookupModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
	if resolved != nil {
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID,
			RequestCount: imageBillingUnits(result, resolved), SizeTier: sizeTier, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			// Return the attempted card mode with the error so missing-price recording
			// can retain image metadata while the caller decides whether zero charging applies.
			s.printf("service.openai_gateway", "Calculate image model card cost failed: %v", err)
			return &CostBreakdown{BillingMode: string(resolved.Mode)}, err
		}
		return cost, nil
	}
	cost, err := s.billingService.CalculateImageCost(lookupModel, sizeTier, imageBillingUnits(result, nil), multiplier)
	if err != nil {
		// Retain image mode beside the catalogue failure so the recorder can classify
		// measured media while choosing whether this pricing error allows zero charging.
		return &CostBreakdown{BillingMode: string(BillingModeImage)}, err
	}
	return cost, nil
}

// CalculateOpenAIVideoCost prices video quantity and normalized resolution or
// duration. Nonpositive VideoCount defaults to one for direct callers; duration
// uses eight seconds when absent and retains every positive observed second.
//
// A configured video card measures total seconds across outputs, while other card
// modes receive output count as units. With no configured card, catalogue pricing
// uses count and duration together. The key and result must be nonnil; failures
// return a zero breakdown retaining the attempted mode with the pricing error.
func (s *Recorder) CalculateOpenAIVideoCost(ctx context.Context, billingModel string, apiKey *KeySnapshot, result *Result, multiplier float64) (*CostBreakdown, error) {
	videoCount := result.VideoCount
	if videoCount <= 0 {
		videoCount = 1
	}
	resolution := NormalizeVideoBillingResolutionOrDefault(result.VideoResolution)
	durationSeconds := NormalizeVideoBillingDurationSecondsOrDefault(result.VideoDurationSeconds)
	resolved, lookupModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey)
	if resolved != nil {
		units := float64(videoCount)
		// Video cards price seconds for each output; count alone is the unit for
		// other configured modes, so multiply duration only for the video card here.
		if resolved.Mode == BillingModeVideo {
			units *= float64(durationSeconds)
		}
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID,
			RequestCount: videoCount, UsageUnits: units, SizeTier: resolution, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			// Keep the attempted card mode beside the pricing error, allowing the recorder
			// to retain completed video dimensions and duration under its missing-price policy.
			s.printf("service.openai_gateway", "Calculate video model card cost failed: %v", err)
			return &CostBreakdown{BillingMode: string(resolved.Mode)}, err
		}
		return cost, nil
	}
	cost, err := s.billingService.CalculateVideoCost(lookupModel, resolution, videoCount, durationSeconds, multiplier)
	if err != nil {
		// Retain video mode beside the catalogue failure so missing-price handling can
		// keep completed output count, normalized resolution, and duration in its row.
		return &CostBreakdown{BillingMode: string(BillingModeVideo)}, err
	}
	return cost, nil
}

// FilterCompatibleClaudeBillingCandidates selects explicitly priced Claude
// aliases for compatible Kimi, Zhipu, and DeepSeek providers. A configured
// amount or an available profile reference establishes the administrator's
// price choice. Ordinary catalog prices and source-only modifiers leave these
// aliases excluded.
//
// Reference selection retains catalog provenance, so its target can have usable
// token or media prices without a custom card. Media availability uses the same
// resolved lookup identity as public quotes and completion charges. Other
// providers retain the supplied slice; Kimi, Zhipu, and DeepSeek results
// contain trimmed nonblank identities and can be empty when every candidate is
// excluded.
func (s *Recorder) FilterCompatibleClaudeBillingCandidates(
	ctx context.Context,
	provider *ProviderSnapshot,
	apiKey *KeySnapshot,
	candidates []string,
) []string {
	if provider == nil || !provider.RequiresExplicitClaudePricing {
		return candidates
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if IsClaudeModelAlias(candidate) {
			if s.resolver == nil || apiKey == nil || apiKey.Group == nil {
				continue
			}
			groupID := apiKey.Group.ID
			resolved := s.resolver.Resolve(ctx, PricingInput{Model: candidate, GroupID: &groupID})
			referencePriced := resolved.ConfiguredReference && !resolved.IsUnpriced()
			if resolved.ConfiguredReference && !referencePriced && s.billingService != nil {
				// A token-normalized result can lack token rates while its referenced target
				// has image or video unit prices. Quote that same target before excluding it.
				referencePriced = s.billingService.DisplayPricingWithResolvedMultipliers(candidate, 1, resolved).PriceStatus == "priced"
			}
			if !resolved.HasEffectiveOverridePricing() && !referencePriced {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

// IsClaudeModelAlias reports whether a trimmed, case-folded name contains
// claude, opus, sonnet, or haiku. These substring heuristics identify
// candidates requiring an effective override for Kimi, Zhipu, and DeepSeek
// billing, including larger custom names.
func IsClaudeModelAlias(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "claude") ||
		strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet") ||
		strings.Contains(model, "haiku")
}

// OpenAIUsageBillingModel selects the price identity from captured result and
// routing evidence. Nil result returns empty; a nonblank explicit BillingModel
// replaces the initial logical/outbound candidate before configured source rules.
//
// Upstream source retains that explicit identity when positive image usage can
// name an image model distinct from the outbound text model. Changed nonblank
// group mapping and nonblank requested-source evidence can override it. An
// unchanged group mapping leaves the explicit candidate available, while unknown
// source values retain the initial choice. Display and raw response model metadata
// remain separate from this returned pricing identity.
func OpenAIUsageBillingModel(result *Result, fields PricingUsageFields) string {
	if result == nil {
		return ""
	}
	billingModel := ForwardResultBillingModel(result.Model, result.UpstreamModel)
	explicitBillingModel := strings.TrimSpace(result.BillingModel)
	if explicitBillingModel != "" {
		billingModel = explicitBillingModel
	}

	switch fields.BillingModelSource {
	case BillingModelSourceUpstream:
		// Image output can have a distinct explicit pricing identity from the sent
		// text model, so positive image usage keeps that candidate under upstream source.
		if upstreamModel := strings.TrimSpace(result.UpstreamModel); upstreamModel != "" && (result.ImageCount <= 0 || explicitBillingModel == "") {
			billingModel = upstreamModel
		}
	case BillingModelSourceGroupMapped:
		mappedModel := strings.TrimSpace(fields.GroupMappedModel)
		if mappedModel != "" && mappedModel != strings.TrimSpace(fields.OriginalModel) {
			billingModel = mappedModel
		}
	case BillingModelSourceRequested:
		if requestedModel := strings.TrimSpace(fields.OriginalModel); requestedModel != "" {
			billingModel = requestedModel
		}
	}
	return billingModel
}

// GroupBillsOpenAIFastAtStandard reports eligibility for standard-priced premium
// OpenAI usage. The key group must enable free Fast and support it, the billing
// provider must be OpenAI, and the normalized tier must be priority or fast.
// Missing key, group, or provider evidence returns false.
func GroupBillsOpenAIFastAtStandard(apiKey *KeySnapshot, provider *ProviderSnapshot, serviceTier string) bool {
	if apiKey == nil || apiKey.Group == nil || !apiKey.Group.FreeOpenAIFast {
		return false
	}
	if provider == nil || !provider.OpenAI || !apiKey.Group.SupportsOpenAIFast {
		return false
	}
	switch normalizeBillingServiceTier(serviceTier) {
	case "priority", "fast":
		return true
	default:
		return false
	}
}

// imageBillingUnits selects observed frames only for image-priced output.
// Per-request cards retain file counts, and token cards ignore this quantity.
// Historical and static results have no frame observation and keep image counts.
func imageBillingUnits(result *Result, resolved *ResolvedPricing) int {
	if result.ImageFrameCount > 0 && (resolved == nil || resolved.Mode == BillingModeImage) {
		return result.ImageFrameCount
	}
	return result.ImageCount
}
