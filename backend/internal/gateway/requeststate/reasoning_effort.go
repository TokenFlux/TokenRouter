package requeststate

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
)

func DeriveOpenAIReasoningEffortFromModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}

	modelID := strings.TrimSpace(model)
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}

	parts := strings.FieldsFunc(strings.ToLower(modelID), func(r rune) bool {
		switch r {
		case '-', '_', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}

	// 国产模型的 max 是显式请求档位，不把同名模型后缀推导成 usage 档位。
	if parts[len(parts)-1] == "max" && !capability.IsOpenAIModelAtLeastVersion(modelID, 5, 6) {
		return ""
	}
	return capability.NormalizeRecordedOpenAIEffortForModel(parts[len(parts)-1], modelID)
}

// DeriveOpenAIReasoningEffortFromModelCandidates 依次对每个候选模型做后缀推导，
// 返回第一个非空结果。
func DeriveOpenAIReasoningEffortFromModelCandidates(models []string) string {
	for _, model := range models {
		if value := DeriveOpenAIReasoningEffortFromModel(model); value != "" {
			return value
		}
	}
	return ""
}

// ExtractOpenAIReasoningEffortFromBody 按优先级传入模型候选（如 upstreamModel,
// billingModel, originalModel）。显式 effort 只做格式归一化并如实记录；body 未携带
// effort 时才从模型后缀推导并执行模型能力判断。OAuth 的 normalizeCodexModel 会
// 剥掉 upstreamModel 的 effort 后缀，因此推导时必须保留原始模型候选。
func ExtractOpenAIReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	reasoningEffort := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if reasoningEffort == "" {
		reasoningEffort = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if reasoningEffort != "" {
		normalized := openai.NormalizeRecordedReasoningEffort(reasoningEffort)
		if normalized == "" {
			return nil
		}
		return &normalized
	}

	value := DeriveOpenAIReasoningEffortFromModelCandidates(modelCandidates)
	if value == "" {
		return nil
	}
	return &value
}

// CanonicalRequestedReasoningEffort 提取策略改写前客户端请求的推理档位。
// 显式字段优先（包括 none）；缺失显式字段时再从模型名末尾的档位后缀推导。
func CanonicalRequestedReasoningEffort(body []byte, modelCandidates ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "output_config.effort").String())
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	for _, model := range modelCandidates {
		if effort := canonicalReasoningEffortFromModelSuffix(model); effort != "" {
			return &effort
		}
	}
	if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
		if effort := canonicalReasoningEffortFromModelSuffix(model); effort != "" {
			return &effort
		}
	}
	return nil
}

func canonicalReasoningEffortFromModelSuffix(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if slash := strings.LastIndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	parts := strings.FieldsFunc(strings.ToLower(model), func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	if len(parts) == 0 {
		return ""
	}
	return routing.NormalizeMaxReasoningEffort(parts[len(parts)-1])
}

// ExtractEffectiveOpenAIReasoningEffortFromBody 从最终上游请求体读取实际转发档位。
// 原请求提供非空 effort、但最终请求体已不再携带时，不允许再从模型后缀补值；
// 空字符串、空白字符串和 null 沿用既有语义，视为未提供。
func ExtractEffectiveOpenAIReasoningEffortFromBody(upstreamBody, originalBody []byte, modelCandidates ...string) *string {
	if strings.TrimSpace(gjson.GetBytes(originalBody, "reasoning.effort").String()) != "" ||
		strings.TrimSpace(gjson.GetBytes(originalBody, "reasoning_effort").String()) != "" {
		return ExtractOpenAIReasoningEffortFromBody(upstreamBody)
	}
	return ExtractOpenAIReasoningEffortFromBody(upstreamBody, modelCandidates...)
}

func ExtractOpenAIServiceTier(reqBody map[string]any) *string {
	if reqBody == nil {
		return nil
	}
	raw, ok := reqBody["service_tier"].(string)
	if !ok {
		return nil
	}
	return openai.NormalizeServiceTier(raw)
}

func ExtractOpenAIServiceTierFromBody(body []byte) *string {
	if len(body) == 0 {
		return nil
	}
	return openai.NormalizeServiceTier(gjson.GetBytes(body, "service_tier").String())
}

// ExtractOpenAIReasoningEffort 的模型候选语义同 ExtractOpenAIReasoningEffortFromBody。
func ExtractOpenAIReasoningEffort(reqBody map[string]any, modelCandidates ...string) *string {
	if value, present := openai.GetOpenAIReasoningEffortFromReqBody(reqBody); present {
		if value == "" {
			return nil
		}
		return &value
	}

	value := DeriveOpenAIReasoningEffortFromModelCandidates(modelCandidates)
	if value == "" {
		return nil
	}
	return &value
}

// CanonicalRequestedReasoningEffortFromReqBody 是 map 形态请求体的同等入口。
func CanonicalRequestedReasoningEffortFromReqBody(reqBody map[string]any, modelCandidates ...string) *string {
	if reqBody == nil {
		return CanonicalRequestedReasoningEffort(nil, modelCandidates...)
	}
	raw := ""
	if reasoning, ok := reqBody["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	if raw == "" {
		if effort, ok := reqBody["reasoning_effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	if raw == "" {
		if outputConfig, ok := reqBody["output_config"].(map[string]any); ok {
			if effort, ok := outputConfig["effort"].(string); ok {
				raw = strings.TrimSpace(effort)
			}
		}
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	return CanonicalRequestedReasoningEffort(nil, modelCandidates...)
}
