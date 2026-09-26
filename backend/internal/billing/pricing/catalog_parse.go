package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CatalogDiagnostics 描述纯解析发现的目录问题，日志输出由 provider 负责。
type CatalogDiagnostics struct {
	Skipped                           int
	OrphanCacheTiers, LopsidedLadders []string
}

// ParsePricingEntries 在已读取的原始条目上执行唯一价格解析，不读取配置或文件。
func ParsePricingEntries(rawData map[string]json.RawMessage) (map[string]*LiteLLMModelPricing, CatalogDiagnostics, error) {
	result := make(map[string]*LiteLLMModelPricing)
	skipped := 0
	var orphanCacheTiers, lopsidedLadders []string

	for modelName, rawEntry := range rawData {
		// 跳过 sample_spec 等文档条目
		if modelName == "sample_spec" {
			continue
		}

		// 尝试解析每个条目
		var entry LiteLLMRawEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			skipped++
			continue
		}

		// 只保留有有效价格的条目
		if entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil && entry.OutputCostPerImage == nil && entry.OutputCostPerImageToken == nil && entry.InputCostPerImageToken == nil {
			continue
		}

		pricing := &LiteLLMModelPricing{
			CacheCreationPricePresent: entry.CacheCreationInputTokenCost != nil,
			CacheReadPricePresent:     entry.CacheReadInputTokenCost != nil,
			ImageInputPricePresent:    entry.InputCostPerImageToken != nil,
			ImageOutputPricePresent:   entry.OutputCostPerImageToken != nil,
			LiteLLMProvider:           entry.LiteLLMProvider,
			Mode:                      entry.Mode,
			SupportsPromptCaching:     entry.SupportsPromptCaching,
			SupportsServiceTier:       entry.SupportsServiceTier,
			SupportedModalities:       entry.SupportedModalities,
			SupportedOutputModalities: entry.SupportedOutputModalities,
			SupportsVision:            entry.SupportsVision,
			SupportsAudioInput:        entry.SupportsAudioInput,
			SupportsAudioOutput:       entry.SupportsAudioOutput,
			SupportsVideoInput:        entry.SupportsVideoInput,
			TokenPricingAbsent:        entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil,
		}
		// 保持原字段优先，兼容部分厂商使用的输入模态字段名。
		if len(pricing.SupportedModalities) == 0 {
			pricing.SupportedModalities = entry.SupportedInputModalities
		}

		if entry.InputCostPerToken != nil {
			pricing.InputCostPerToken = *entry.InputCostPerToken
		}
		if entry.InputCostPerTokenPriority != nil {
			pricing.InputCostPerTokenPriority = *entry.InputCostPerTokenPriority
		}
		if entry.OutputCostPerToken != nil {
			pricing.OutputCostPerToken = *entry.OutputCostPerToken
		}
		if entry.OutputCostPerTokenPriority != nil {
			pricing.OutputCostPerTokenPriority = *entry.OutputCostPerTokenPriority
		}
		if entry.CacheCreationInputTokenCost != nil {
			pricing.CacheCreationInputTokenCost = *entry.CacheCreationInputTokenCost
		}
		if entry.CacheCreationInputTokenCostPriority != nil {
			pricing.CacheCreationInputTokenCostPriority = *entry.CacheCreationInputTokenCostPriority
		}
		if entry.CacheCreationInputTokenCostAbove1hr != nil {
			pricing.CacheCreationInputTokenCostAbove1hr = *entry.CacheCreationInputTokenCostAbove1hr
		}
		if entry.CacheReadInputTokenCost != nil {
			pricing.CacheReadInputTokenCost = *entry.CacheReadInputTokenCost
		}
		if entry.CacheReadInputTokenCostPriority != nil {
			pricing.CacheReadInputTokenCostPriority = *entry.CacheReadInputTokenCostPriority
		}
		if entry.LongContextInputTokenThreshold != nil {
			pricing.LongContextInputTokenThreshold = *entry.LongContextInputTokenThreshold
		}
		if entry.LongContextInputCostMultiplier != nil {
			pricing.LongContextInputCostMultiplier = *entry.LongContextInputCostMultiplier
		}
		if entry.LongContextOutputCostMultiplier != nil {
			pricing.LongContextOutputCostMultiplier = *entry.LongContextOutputCostMultiplier
		}
		if entry.OutputCostPerImage != nil {
			pricing.OutputCostPerImage = *entry.OutputCostPerImage
		}
		if entry.OutputCostPerImageToken != nil {
			pricing.OutputCostPerImageToken = *entry.OutputCostPerImageToken
		}
		if entry.InputCostPerImageToken != nil {
			pricing.InputCostPerImageToken = *entry.InputCostPerImageToken
		}

		// 显式 long_context 字段（包括显式 0）优先于目录中的 above 绝对价字段。
		hasExplicitLongContext := entry.LongContextInputTokenThreshold != nil ||
			entry.LongContextInputCostMultiplier != nil ||
			entry.LongContextOutputCostMultiplier != nil
		if !hasExplicitLongContext {
			DeriveLongContextFromAboveTierFields(rawEntry, pricing)
			if IsLopsidedLongContextLadder(pricing) {
				lopsidedLadders = append(lopsidedLadders, fmt.Sprintf("%s(input x%.2f, output x%.2f)", modelName,
					pricing.LongContextInputCostMultiplier, pricing.LongContextOutputCostMultiplier))
			}
		}
		if orphans := OrphanCacheTierFields(rawEntry); len(orphans) > 0 {
			orphanCacheTiers = append(orphanCacheTiers, modelName+"("+strings.Join(orphans, ",")+")")
		}

		result[modelName] = pricing
	}

	diagnostics := CatalogDiagnostics{Skipped: skipped, OrphanCacheTiers: orphanCacheTiers, LopsidedLadders: lopsidedLadders}

	if len(result) == 0 {
		return nil, diagnostics, fmt.Errorf("no valid pricing entries found")
	}

	return result, diagnostics, nil
}

// ApplyCatalogOverrides 保留原地字段浅合并与 null 删除，新增模型由后续层处理。
func ApplyCatalogOverrides(rawData, overrides map[string]json.RawMessage) (map[string]json.RawMessage, []string) {
	var invalid []string
	if len(overrides) == 0 {
		return rawData, invalid
	}
	for name, patch := range overrides {
		base, ok := rawData[name]
		if !ok {
			continue
		}
		merged, valid := MergePricingOverrideEntry(base, patch)
		if !valid {
			invalid = append(invalid, name)
			continue
		}
		rawData[name] = merged
	}
	return rawData, invalid
}

// MergeFallbackEntries 仅填补主目录缺失的模型，返回实际增加数量。
func MergeFallbackEntries(data, fallback map[string]*LiteLLMModelPricing) (map[string]*LiteLLMModelPricing, int) {
	if data == nil {
		data = make(map[string]*LiteLLMModelPricing)
	}
	merged := 0
	for name, entry := range fallback {
		if _, ok := data[name]; !ok {
			data[name] = entry
			merged++
		}
	}
	return data, merged
}

// MissingOverrideEntries 保留 override 尚未命中的原始条目，不提前覆盖 fallback 层。
func MissingOverrideEntries(data map[string]*LiteLLMModelPricing, overrides map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	for name, entry := range overrides {
		if _, ok := data[name]; !ok {
			out[name] = entry
		}
	}
	return out
}
