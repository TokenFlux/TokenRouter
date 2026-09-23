// 平台型号只决定 thinking 转换选项；字节算法由 requeststate 唯一实现。
package provider

import (
	"strings"

	modelidentity "github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	antigravity "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// FilterThinkingBlocks 从请求体中移除不适合直发的 thinking block。
// 过滤失败时返回原 body，避免整流逻辑影响主请求。
// 主要用于避免无效 thinking block signature 触发上游 400。
//
// 策略：
//   - 当 thinking.type 不是 "enabled"/"adaptive"：移除所有 thinking 相关块
//   - 当 thinking.type 是 "enabled"/"adaptive"：仅移除缺失/无效 signature 的 thinking 块（避免 400）
//     例如缺失、空值或占位 signature 的块
//
// 调用方传入 mappedModel 时会按上游协议族分流：仅 Anthropic 官方语义执行过滤；
// DeepSeek/Kimi/GLM/MiniMax 等 passback-required 上游必须原样回传历史 thinking block。
// 未传 mappedModel 时保留旧行为，便于既有单元测试和纯工具调用继续使用。
func FilterThinkingBlocks(body []byte, mappedModel ...string) []byte {
	return requeststate.FilterThinkingBlocks(body, thinkingRequestOptions(mappedModel...))
}

func FilterThinkingBlocksForRetry(body []byte, mappedModel ...string) []byte {
	return requeststate.FilterThinkingBlocksForRetry(body, thinkingRequestOptions(mappedModel...))
}

func FilterSignatureSensitiveBlocksForRetry(body []byte, mappedModel ...string) []byte {
	return requeststate.FilterSignatureSensitiveBlocksForRetry(body, thinkingRequestOptions(mappedModel...))
}

// DefaultEffortForThinkingEnabled 给"开启 thinking 但协议层没有 effort 档位概念"
// 的国产模型族返回默认 effort，用于 usage_log.reasoning_effort 展示。
func DefaultEffortForThinkingEnabled(mappedModel string) *string {
	return requeststate.DefaultEffortForThinkingEnabled(thinkingRequestOptions(mappedModel))
}

// ApplyThinkingEnabledFallback 在调用方尚未解析出 effort 时，为启用 thinking 的
// 国产 passback-required 上游补默认 effort；已显式传入的 effort 永远不覆盖。
func ApplyThinkingEnabledFallback(effort *string, body []byte, mappedModel string) *string {
	return requeststate.ApplyThinkingEnabledFallback(effort, body, thinkingRequestOptions(mappedModel))
}

// NormalizeGLMOpenAIReasoningEffort 将 OpenAI Chat Completions 的
// reasoning_effort 档位映射到 GLM/z.ai 原生 high/max 档位。
// 仅对映射后的 glm-* 模型生效，其它上游保持原请求不变。
func NormalizeGLMOpenAIReasoningEffort(body []byte, mappedModel string) ([]byte, bool) {
	return requeststate.NormalizeGLMOpenAIReasoningEffort(body, thinkingRequestOptions(mappedModel))
}

func isGLM53Model(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "glm-5.3")
}

// NormalizeGLM53AnthropicThinking 将客户端显式 thinking 强度映射到 GLM-5.3
// Anthropic 兼容档位；未提供强度或 thinking 偏好时保持请求不变，交由上游默认处理。
func NormalizeGLM53AnthropicThinking(body []byte, mappedModel string) ([]byte, bool) {
	return requeststate.NormalizeGLM53AnthropicThinking(body, isGLM53Model(mappedModel))
}

// =========================
// Thinking Budget Rectifier
// =========================

// NormalizeChineseLLMThinking 修正国产 Anthropic 兼容上游的 thinking.type 差异。
// 当前仅 MiniMax M 系列需要把 Anthropic SDK 默认的 enabled 改成 adaptive。
func NormalizeChineseLLMThinking(body []byte, mappedModel string) ([]byte, bool) {
	return requeststate.NormalizeChineseLLMThinking(body, strings.HasPrefix(strings.ToLower(strings.TrimSpace(mappedModel)), "minimax-m"))
}

// 请求体、raw range 和会话上下文只由 requeststate 实现。

// thinkingRequestOptions 只按原协议分类选择开关；报文算法由 requeststate 唯一实现。
func thinkingRequestOptions(models ...string) requeststate.ThinkingRequestOptions {
	options := requeststate.ThinkingRequestOptions{PreFilter: true, RetryFilters: true, DummySignature: antigravity.DummyThoughtSignature}
	if len(models) == 0 {
		return options
	}
	model := models[0]
	options.PreFilter = modelidentity.ShouldPreFilterThinkingBlocks(model)
	options.RetryFilters = modelidentity.ShouldApplyRetryFilters(model)
	options.PassbackRequired = modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired
	normalized := strings.ToLower(strings.TrimSpace(model))
	options.NativeReasoningEffort = strings.HasPrefix(normalized, "deepseek-")
	options.GLM = strings.HasPrefix(normalized, "glm-")
	options.GLM53 = isGLM53Model(model)
	return options
}
