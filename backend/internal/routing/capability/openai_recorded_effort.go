package capability

import (
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// NormalizeRecordedOpenAIEffortForModel 保留实际模型对 max 的支持边界。
func NormalizeRecordedOpenAIEffortForModel(raw string, model string) string {
	value := protocolopenai.NormalizeRecordedReasoningEffort(raw)
	switch value {
	case "max":
		if !OpenAIModelSupportsReasoningEffort(model, value) {
			return ""
		}
	}
	return value
}
