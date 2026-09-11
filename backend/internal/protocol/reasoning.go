package protocol

// 推理档位是 wire 值，管理员映射和排序策略由 routing 拥有。
const (
	ReasoningNone    = "none"
	ReasoningMinimal = "minimal"
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
	ReasoningXHigh   = "xhigh"
	ReasoningMax     = "max"
)

// OpenAIReasoningEfforts 返回可比较档位的独立副本。
func OpenAIReasoningEfforts() []string {
	return []string{ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax}
}

// AnthropicReasoningEfforts 不包含 OpenAI 专用的 none/minimal。
func AnthropicReasoningEfforts() []string {
	return []string{ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax}
}

// OpenAIReasoningMappingValues 允许显式关闭推理，none 不参与上限排序。
func OpenAIReasoningMappingValues() []string {
	return append([]string{ReasoningNone}, OpenAIReasoningEfforts()...)
}
