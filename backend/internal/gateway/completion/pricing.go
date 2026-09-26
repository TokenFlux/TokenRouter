// 完成处理只决定用量路径和按需查价顺序，金额规则由 billing 唯一实现。
package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Recorder) CalculateRecordUsageCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	account *AccountSnapshot,
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
	// 图片生成：共享价格配置定价为令牌计费时走令牌路径，否则走图片计费
	if result.ImageCount > 0 {
		if resolved, pricingModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey); resolved != nil && resolved.Mode == BillingModeToken {
			return s.CalculateTokenCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, groupMappedModel, multiplier, opts)
		} else if resolved != nil {
			return s.CalculateImageCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, groupMappedModel, pricingModel, resolved, imageMultiplier, opts.PricingAt)
		}
		return s.CalculateImageCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, groupMappedModel, billingModel, nil, imageMultiplier, opts.PricingAt)
	}

	// 语音用量优先按分组模型的连续单位价格结算，未配置时沿用分组通用音频价。
	if result.AudioUsage != nil {
		resolved, pricingModel := s.resolveConfigPricingForUsage(
			ctx, billingModel, apiKey,
		)
		if resolved != nil && resolved.Mode == BillingModePerRequest {
			gid := apiKey.Group.ID
			cost, err := s.billingService.CalculateCostUnified(CostInput{
				Ctx:            ctx,
				Model:          pricingModel,
				GroupID:        &gid,
				Group:          apiKey.Group.Price,
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
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, multiplier)
	}

	// Token 费用与搜索附加费分别计算，搜索不会替代模型本身的 token 费用。
	tokenCost := s.CalculateTokenCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, groupMappedModel, multiplier, opts)
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

func (s *Recorder) ResolveConfigPricing(ctx context.Context, billingModel string, apiKey *KeySnapshot) *ResolvedPricing {
	if s.resolver == nil || apiKey == nil || apiKey.Group == nil {
		return nil
	}
	gid := apiKey.Group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{Model: billingModel, GroupID: &gid, Group: apiKey.Group.Price})
	if resolved.HasConfiguredPricing() {
		return resolved
	}
	return nil
}

func (s *Recorder) CalculateImageCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	account *AccountSnapshot,
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
			Ctx:            ctx,
			Model:          resolvedModel,
			GroupID:        &gid,
			Group:          apiKey.Group.Price,
			Tokens:         tokens,
			RequestCount:   result.ImageCount,
			SizeTier:       sizeTier,
			RateMultiplier: multiplier,
			PricingAt:      pricingAt,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
		if err != nil {
			s.printf("service.gateway", "Calculate image token cost failed: %v", err)
			return &CostBreakdown{ActualCost: 0}
		}
		return cost
	}

	return s.billingService.CalculateImageCost(billingModel, sizeTier, result.ImageCount, multiplier)
}

func (s *Recorder) CalculateTokenCost(
	ctx context.Context,
	result *Result,
	apiKey *KeySnapshot,
	account *AccountSnapshot,
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

	// 分组或共享价格配置显式价格优先，并按价格配置选择计费模型。
	if resolved, resolvedModel := s.resolveConfigPricingForUsage(ctx, billingModel, apiKey); resolved != nil {
		gid := apiKey.Group.ID
		cost, err = s.billingService.CalculateCostUnified(CostInput{
			Ctx:             ctx,
			Model:           resolvedModel,
			GroupID:         &gid,
			Group:           apiKey.Group.Price,
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
				Ctx:             ctx,
				Model:           billingModel,
				GroupID:         &gid,
				Group:           apiKey.Group.Price,
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
				applyCostBreakdownMultiplier(cost, maxReasoningEffortBillingMultiplier(billingModel, stringValueOrEmpty(result.ReasoningEffort), nil))
			}
		}
	}
	if err != nil {
		s.printf("service.gateway", "Calculate cost failed: %v", err)
		return &CostBreakdown{ActualCost: 0}
	}
	return cost
}

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
		// Codex alpha/search 网页搜索按次计费：上游不返回 usage/token 字段，单价只取
		// 分组覆盖价（nil 时默认 0.01 = 官方 $10/1000 次），不参与共享模型价卡定价。
		// 倍率与 image/video 按次口径一致：使用不含高峰因子的基础倍率
		//（用户专属 > 分组 rate_multiplier > 系统默认），与分组表单的价格预览承诺一致。
		return s.billingService.CalculateWebSearchCost(result.WebSearchCalls, webSearchPricePerCallFromAPIKey(apiKey), webSearchMultiplier), nil
	}
	if IsGrokVideoUsageResult(result, billingModels) {
		if resolved := s.ResolveOpenAIConfigPricing(ctx, billingModel, apiKey); resolved == nil || resolved.Mode != BillingModeToken {
			return s.CalculateOpenAIVideoCost(ctx, billingModel, apiKey, result, videoMultiplier), nil
		}
	}
	if result != nil && result.AudioUsage != nil {
		if resolved := s.ResolveOpenAIConfigPricing(ctx, billingModel, apiKey); resolved != nil &&
			(resolved.Mode == BillingModePerRequest) {
			gid := apiKey.Group.ID
			return s.billingService.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: billingModel, GroupID: &gid, Group: apiKey.Group.Price,
				UsageUnits: result.AudioUsage.DurationOrUnits, SizeTier: result.AudioUsage.Mode,
				RateMultiplier: webSearchMultiplier, Resolver: s.resolver, Resolved: resolved,
			})
		}
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, webSearchMultiplier), nil
	}

	if result != nil && result.ImageCount > 0 {
		// 共享价格配置定价为令牌计费时走令牌路径，否则走图片计费
		if resolved := s.ResolveOpenAIConfigPricing(ctx, billingModel, apiKey); resolved == nil || resolved.Mode != BillingModeToken {
			return s.CalculateOpenAIImageCost(ctx, billingModel, apiKey, result, imageMultiplier), nil
		}
	}

	// Token 成本与搜索附加费分开计算，搜索费不得掩盖 token 定价失败。
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
		// 仅搜索且没有模型的纯工具路径允许单独计算搜索费用。
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
	// 费用由令牌费用与搜索附加费相加。
	tokenCost.TotalCost += searchCost.TotalCost
	tokenCost.ActualCost += searchCost.ActualCost
	return tokenCost, nil
}

func IsGrokVideoBillingModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-imagine-video")
}

func IsGrokVideoUsageResult(result *Result, billingModels []string) bool {
	if result == nil || result.VideoCount <= 0 {
		return false
	}
	// VideoCount 本身就是异步视频完成计费的权威依据。
	// 存在模型族时优先匹配，但不得因重命名或映射丢失视频计费模式。
	candidates := append([]string{}, billingModels...)
	candidates = append(candidates, result.BillingModel, result.Model, result.UpstreamModel)
	for _, candidate := range candidates {
		if IsGrokVideoBillingModel(candidate) {
			return true
		}
	}
	return true
}

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
			Ctx:             ctx,
			Model:           billingModel,
			GroupID:         &gid,
			Group:           apiKey.Group.Price,
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

func ForwardResultReasoningEffort(result *Result) string {
	if result == nil || result.ReasoningEffort == nil {
		return ""
	}
	return *result.ReasoningEffort
}

func (s *Recorder) CalculateOpenAIImageCost(ctx context.Context, billingModel string, apiKey *KeySnapshot, result *Result, multiplier float64) *CostBreakdown {
	sizeTier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	resolved := s.ResolveOpenAIConfigPricing(ctx, billingModel, apiKey)
	if resolved != nil {
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID, Group: apiKey.Group.Price,
			RequestCount: result.ImageCount, SizeTier: sizeTier, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			s.printf("service.openai_gateway", "Calculate image model card cost failed: %v", err)
			return &CostBreakdown{}
		}
		return cost
	}
	return s.billingService.CalculateImageCost(billingModel, sizeTier, result.ImageCount, multiplier)
}

func (s *Recorder) CalculateOpenAIVideoCost(ctx context.Context, billingModel string, apiKey *KeySnapshot, result *Result, multiplier float64) *CostBreakdown {
	videoCount := result.VideoCount
	if videoCount <= 0 {
		videoCount = 1
	}
	resolution := NormalizeVideoBillingResolutionOrDefault(result.VideoResolution)
	durationSeconds := NormalizeVideoBillingDurationSecondsOrDefault(result.VideoDurationSeconds)
	resolved := s.ResolveOpenAIConfigPricing(ctx, billingModel, apiKey)
	if resolved != nil {
		units := float64(videoCount)
		if resolved.Mode == BillingModeVideo {
			units *= float64(durationSeconds)
		}
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID, Group: apiKey.Group.Price,
			RequestCount: videoCount, UsageUnits: units, SizeTier: resolution, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			s.printf("service.openai_gateway", "Calculate video model card cost failed: %v", err)
			return &CostBreakdown{}
		}
		return cost
	}
	return s.billingService.CalculateVideoCost(billingModel, resolution, videoCount, durationSeconds, multiplier)
}

func (s *Recorder) FilterCNProviderBillingModelCandidates(
	ctx context.Context,
	account *AccountSnapshot,
	apiKey *KeySnapshot,
	candidates []string,
) []string {
	if account == nil || !account.CNProvider {
		return candidates
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if IsCNProviderClaudeFallbackCandidate(candidate) {
			// 纯倍率可以参与媒体计费，但不能替国产模型建立 Claude 的显式基础价。
			resolved := s.ResolveOpenAIConfigPricing(ctx, candidate, apiKey)
			if !resolved.HasEffectiveOverridePricing() {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func IsCNProviderClaudeFallbackCandidate(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "claude") ||
		strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet") ||
		strings.Contains(model, "haiku")
}

func (s *Recorder) ResolveOpenAIConfigPricing(ctx context.Context, billingModel string, apiKey *KeySnapshot) *ResolvedPricing {
	if s.resolver == nil || apiKey == nil || apiKey.Group == nil {
		return nil
	}
	gid := apiKey.Group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{Model: billingModel, GroupID: &gid, Group: apiKey.Group.Price})
	if resolved.HasConfiguredPricing() {
		return resolved
	}
	return nil
}

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
		// 图片轮次的上游文本模型不是图片计价 SKU，必须保留显式解析出的图片模型。
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

func GroupBillsOpenAIFastAtStandard(apiKey *KeySnapshot, account *AccountSnapshot, serviceTier string) bool {
	if apiKey == nil || apiKey.Group == nil || !apiKey.Group.FreeOpenAIFast {
		return false
	}
	if account == nil || !account.OpenAI || !apiKey.Group.SupportsOpenAIFast {
		return false
	}
	switch normalizeBillingServiceTier(serviceTier) {
	case "priority", "fast":
		return true
	default:
		return false
	}
}
