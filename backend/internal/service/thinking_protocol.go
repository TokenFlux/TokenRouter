package service

import "strings"

// ThinkingProtocol 描述上游对 thinking block 的处理契约。
// 不同上游对历史 thinking block 的要求相反：Anthropic 官方要求有效签名，
// 第三方 Anthropic 兼容上游通常要求原样回传历史 thinking block。
type ThinkingProtocol int

const (
	// ThinkingProtocolUnknown 表示无法识别协议族，默认保守不剥离。
	ThinkingProtocolUnknown ThinkingProtocol = iota

	// ThinkingProtocolAnthropicStrict 表示 Anthropic 官方语义：缺失或非法签名应剥离。
	ThinkingProtocolAnthropicStrict

	// ThinkingProtocolPassbackRequired 表示第三方兼容上游语义：thinking block 必须原样回传。
	ThinkingProtocolPassbackRequired
)

// ResolveThinkingProtocol 根据上游模型 ID 推断 thinking 协议族。
//
// Anthropic 原生路径应传映射后的上游模型；Gemini messages compat 的 Claude body
// retry 则传客户端原始模型，因为被整流的是 Anthropic 请求体本身。
func ResolveThinkingProtocol(modelID string) ThinkingProtocol {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return ThinkingProtocolUnknown
	}

	switch {
	case strings.HasPrefix(id, "deepseek-"),
		strings.HasPrefix(id, "kimi-"),
		strings.HasPrefix(id, "moonshot-"),
		strings.HasPrefix(id, "glm-"):
		return ThinkingProtocolPassbackRequired
	}
	if strings.HasPrefix(id, "minimax-m") {
		return ThinkingProtocolPassbackRequired
	}
	if (strings.HasPrefix(id, "qwen-") ||
		strings.HasPrefix(id, "qwen2-") ||
		strings.HasPrefix(id, "qwen3-") ||
		strings.HasPrefix(id, "qwen4-")) && strings.Contains(id, "-thinking") {
		return ThinkingProtocolPassbackRequired
	}

	switch {
	case strings.HasPrefix(id, "claude-"),
		strings.HasPrefix(id, "opus-"),
		strings.HasPrefix(id, "sonnet-"),
		strings.HasPrefix(id, "haiku-"):
		return ThinkingProtocolAnthropicStrict
	}

	return ThinkingProtocolUnknown
}

// ShouldPreFilterThinkingBlocks 判断是否应在转发前剥离无效 thinking block。
func ShouldPreFilterThinkingBlocks(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}

// ShouldRectifyThinkingSignatureError 判断是否应在 400 后触发 thinking 签名整流 retry。
func ShouldRectifyThinkingSignatureError(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}

// ShouldApplyRetryFilters 判断 retry 路径是否应执行 thinking/tool block 变形。
func ShouldApplyRetryFilters(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}
