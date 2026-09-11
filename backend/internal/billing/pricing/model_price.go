package pricing

import (
	"fmt"
	"strings"
)

// GetModelPricing 获取模型价格配置
func ResolveModelPricing(model string, catalogPrice *LiteLLMModelPricing, prices map[string]*ModelPricing, policy ModelPolicy) (*ModelPricing, bool, error) {
	// 标准化模型名称（转小写）
	model = strings.ToLower(model)

	// 1. 优先从动态价格服务获取
	if catalogPrice != nil {
		litellmPricing := catalogPrice
		// 仅有图片价、无 token 价的条目（如 LiteLLM 的 imagen 类模型）不能用于
		// token 计费：直接返回会把 token 流量按 $0 计费。跳过后走 fallback，
		// 无 fallback 则 fail-closed（ErrModelPricingUnavailable）。
		// 图片计费路径（getDefaultImagePrice / getImageUnitPrice）直接读
		// PricingService，不受影响。
		if litellmPricing != nil && litellmPricing.TokenPricingAbsent {
			litellmPricing = nil
		}
		if litellmPricing != nil {
			// 启用 5m/1h 分类计费的条件：
			// 1. 存在 1h 价格
			// 2. 1h 价格 > 5m 价格（防止 LiteLLM 数据错误导致少收费）
			price5m := litellmPricing.CacheCreationInputTokenCost
			price1h := litellmPricing.CacheCreationInputTokenCostAbove1hr
			enableBreakdown := price1h > 0 && price1h > price5m
			return ApplyModelSpecificPricingPolicy(model, &ModelPricing{
				InputPricePerToken:                 litellmPricing.InputCostPerToken,
				InputPricePerTokenPriority:         litellmPricing.InputCostPerTokenPriority,
				OutputPricePerToken:                litellmPricing.OutputCostPerToken,
				OutputPricePerTokenPriority:        litellmPricing.OutputCostPerTokenPriority,
				CacheCreationPricePerToken:         litellmPricing.CacheCreationInputTokenCost,
				CacheCreationPricePerTokenPriority: litellmPricing.CacheCreationInputTokenCostPriority,
				CacheReadPricePerToken:             litellmPricing.CacheReadInputTokenCost,
				CacheReadPricePerTokenPriority:     litellmPricing.CacheReadInputTokenCostPriority,
				CacheCreation5mPrice:               price5m,
				CacheCreation1hPrice:               price1h,
				SupportsCacheBreakdown:             enableBreakdown,
				SupportsServiceTier:                litellmPricing.SupportsServiceTier,
				// xAI 的目录语义是达到阈值即进入高档，其他提供商保持严格大于。
				LongContextThresholdInclusive: strings.EqualFold(litellmPricing.LiteLLMProvider, "xai"),
				LongContextInputThreshold:     litellmPricing.LongContextInputTokenThreshold,
				LongContextInputMultiplier:    litellmPricing.LongContextInputCostMultiplier,
				LongContextOutputMultiplier:   litellmPricing.LongContextOutputCostMultiplier,
				ImageInputPricePerToken:       litellmPricing.InputCostPerImageToken,
				ImageOutputPricePerToken:      litellmPricing.OutputCostPerImageToken,
				MaxReasoningEffortMultiplier:  DefaultMaxReasoningEffortMultiplier(model),
			}, policy), false, nil
		}
	}

	// 2. 使用硬编码回退价格
	fallback := LookupFallbackPrice(prices, model, policy)
	if fallback != nil {

		cloned := *fallback
		if cloned.MaxReasoningEffortMultiplier == nil {
			cloned.MaxReasoningEffortMultiplier = DefaultMaxReasoningEffortMultiplier(model)
		}
		return ApplyModelSpecificPricingPolicy(model, &cloned, policy), true, nil
	}

	return nil, false, fmt.Errorf("%w for model: %s", ErrModelPricingUnavailable, model)
}
