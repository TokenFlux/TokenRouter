// Thinking 报文策略接收外层确定的资格，不识别账号或具体平台实现。
package requeststate

import (
	"strings"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ThinkingRequestOptions 固化原调用入口的协议族和模型资格，保留未指定模型时的兼容过滤。
type ThinkingRequestOptions struct {
	PreFilter, RetryFilters                 bool
	PassbackRequired, NativeReasoningEffort bool
	GLM, GLM53                              bool
	DummySignature                          string
}

func FilterThinkingBlocks(body []byte, options ThinkingRequestOptions) []byte {
	if !options.PreFilter {
		return body
	}
	return FilterThinkingBlocksInternal(body, options.DummySignature)
}

func FilterThinkingBlocksForRetry(body []byte, options ThinkingRequestOptions) []byte {
	if !options.RetryFilters {
		return body
	}
	return protocolanthropic.FilterThinkingBlocksForRetry(body)
}

func FilterSignatureSensitiveBlocksForRetry(body []byte, options ThinkingRequestOptions) []byte {
	if !options.RetryFilters {
		return body
	}
	return protocolanthropic.FilterSignatureSensitiveBlocksForRetry(body)
}

func FilterThinkingBlocksInternal(body []byte, dummySignature string) []byte {
	return protocolanthropic.FilterThinkingBlocksInternal(body, dummySignature)
}

func DefaultEffortForThinkingEnabled(options ThinkingRequestOptions) *string {
	if !options.PassbackRequired {
		return nil
	}
	if options.NativeReasoningEffort {
		// DeepSeek 原生支持 reasoning_effort，不在这里注入默认值。
		return nil
	}
	effort := "high"
	return &effort
}

func OpenAIBodyHasThinkingEnabled(body []byte) bool {
	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	return thinkingType == "enabled" || thinkingType == "adaptive"
}

func ApplyThinkingEnabledFallback(effort *string, body []byte, options ThinkingRequestOptions) *string {
	if effort != nil {
		return effort
	}
	if !OpenAIBodyHasThinkingEnabled(body) {
		return nil
	}
	return DefaultEffortForThinkingEnabled(options)
}

func NormalizeGLMOpenAIReasoningEffort(body []byte, options ThinkingRequestOptions) ([]byte, bool) {
	if !options.GLM {
		return body, false
	}

	path := "reasoning.effort"
	raw := strings.TrimSpace(gjson.GetBytes(body, path).String())
	if raw == "" {
		path = "reasoning_effort"
		raw = strings.TrimSpace(gjson.GetBytes(body, path).String())
	}
	if raw == "" {
		return body, false
	}

	mapped := NormalizeGLMEffortToken(raw)
	if options.GLM53 && mapped == "high" && NormalizeEffortToken(raw) == "low" {
		mapped = "low"
	}
	if mapped == "" || mapped == raw {
		return body, false
	}

	modified, err := sjson.SetBytes(body, path, mapped)
	if err != nil {
		return body, false
	}
	return modified, true
}

func NormalizeEffortToken(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
}

func NormalizeGLMEffortToken(raw string) string {
	value := NormalizeEffortToken(raw)
	if value == "" {
		return ""
	}

	switch value {
	case "low", "medium", "high":
		return "high"
	case "xhigh", "extrahigh", "max", "ultracode":
		return "max"
	default:
		return ""
	}
}

func NormalizeGLM53AnthropicThinking(body []byte, enabled bool) ([]byte, bool) {
	if !enabled {
		return body, false
	}

	raw := gjson.GetBytes(body, "output_config.effort").String()
	if strings.TrimSpace(raw) == "" {
		raw = gjson.GetBytes(body, "thinking.type").String()
	}

	var effort string
	switch NormalizeEffortToken(raw) {
	case "disabled", "off", "none", "minimal", "low":
		effort = "low"
	case "enabled", "adaptive", "medium", "high":
		effort = "high"
	case "xhigh", "max", "ultra":
		effort = "max"
	default:
		return body, false
	}

	modified, err := sjson.SetBytes(body, "thinking.type", "enabled")
	if err != nil {
		return body, false
	}
	modified, err = sjson.SetBytes(modified, "output_config.effort", effort)
	if err != nil {
		return body, false
	}
	return modified, true
}

func NormalizeChineseLLMThinking(body []byte, minimaxAdaptive bool) ([]byte, bool) {
	if !minimaxAdaptive {
		return body, false
	}
	if gjson.GetBytes(body, "thinking.type").String() != "enabled" {
		return body, false
	}
	modified, err := sjson.SetBytes(body, "thinking.type", "adaptive")
	if err != nil {
		return body, false
	}
	return modified, true
}
