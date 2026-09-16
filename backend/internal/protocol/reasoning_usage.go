package protocol

// IncludeIndependentReasoningTokens 仅在 total_tokens 证明推理 token 尚未计入输出时，
// 将独立的 reasoning token 加入计费输出。
// xAI Chat Completions 示例为 prompt=32、completion=9、reasoning=94、total=135；
// Responses 示例为 input=32、output=9、reasoning=110、total=151。OpenAI 标准
// completion_tokens 已包含推理 token，且 total 等于 input+output。
func IncludeIndependentReasoningTokens(input, output, total, reasoning int64) int64 {
	if input < 0 || output < 0 || reasoning <= 0 || total <= 0 {
		return output
	}
	if total == input+output {
		return output
	}
	gap := total - input - output
	if gap <= 0 {
		return output
	}
	if reasoning < gap {
		gap = reasoning
	}
	return output + gap
}
