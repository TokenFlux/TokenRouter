package bridge

import (
	"encoding/json"
)

// geminiResponseToResponses 统一完成 Gemini -> Anthropic -> Responses 的响应转换。
func NativeGeminiResponseToResponses(runtime Runtime, native NativeGeminiRuntime,
	geminiResp map[string]any,
	originalModel string,
	rawData []byte,
	usageOverride *NativeGeminiUsage,
) (*ResponsesResponse, *NativeGeminiUsage, bool, error) {
	claudeRespMap, usage := NativeConvertGeminiToClaudeMessage(native, geminiResp, originalModel, rawData, true)
	usedOverride := false
	if usageOverride != nil && (usageOverride.InputTokens > 0 || usageOverride.OutputTokens > 0 || usageOverride.CacheReadInputTokens > 0) {
		usage = usageOverride
		usedOverride = true
		if usageMap, ok := claudeRespMap["usage"].(map[string]any); ok {
			usageMap["input_tokens"] = usage.InputTokens
			usageMap["output_tokens"] = usage.OutputTokens
			usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
		}
	}

	claudeBytes, err := json.Marshal(claudeRespMap)
	if err != nil {
		return nil, nil, false, err
	}
	var anthropicResp AnthropicResponse
	if err := json.Unmarshal(claudeBytes, &anthropicResp); err != nil {
		return nil, nil, false, err
	}
	responsesResp := AnthropicToResponsesResponse(runtime, &anthropicResp)
	responsesResp.Model = originalModel
	return responsesResp, usage, usedOverride, nil
}

func NativeGeminiResponseToChatCompletions(runtime Runtime, native NativeGeminiRuntime,
	geminiResp map[string]any,
	originalModel string,
	rawData []byte,
	usageOverride *NativeGeminiUsage,
) (*ChatCompletionsResponse, *NativeGeminiUsage, bool, error) {
	responsesResp, usage, usedOverride, err := NativeGeminiResponseToResponses(runtime, native, geminiResp, originalModel, rawData, usageOverride)
	if err != nil {
		return nil, nil, false, err
	}
	return ResponsesToChatCompletions(runtime, responsesResp, originalModel), usage, usedOverride, nil
}
