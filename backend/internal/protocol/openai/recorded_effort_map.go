package openai

// GetOpenAIReasoningEffortFromReqBody 只提取请求中显式给出的档位。
// 显式值代表客户端真实请求，记录时不应再按模型名称推测上游能力；模型后缀
// 推导由 deriveOpenAIReasoningEffortFromModel 单独处理并继续保留能力门槛。
func GetOpenAIReasoningEffortFromReqBody(reqBody map[string]any) (value string, present bool) {
	if reqBody == nil {
		return "", false
	}

	// Primary: reasoning.effort
	if reasoning, ok := reqBody["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			return NormalizeRecordedReasoningEffort(effort), true
		}
	}

	// Fallback: some clients may use a flat field.
	if effort, ok := reqBody["reasoning_effort"].(string); ok {
		return NormalizeRecordedReasoningEffort(effort), true
	}

	return "", false
}
