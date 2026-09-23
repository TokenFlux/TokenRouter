package requeststate

import (
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/tidwall/gjson"
)

// hasOpenAIUltraReasoningSuffix 仅识别 OpenAI GPT 模型，避免误伤其它平台的 Ultra 命名。
func hasOpenAIUltraReasoningSuffix(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(capability.LastOpenAIModelSegment(model)))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	return strings.HasPrefix(normalized, "gpt-") && strings.HasSuffix(normalized, "-ultra")
}

// validateOpenAIReasoningEffort 拒绝 Codex 客户端专用的 Ultra 模式。
// Ultra 在 Codex 内部表示 max 推理加主动多代理，不是 OpenAI 上游协议档位。
func ValidateOpenAIReasoningEffort(body []byte, requestedModel string) error {
	efforts := []string{
		gjson.GetBytes(body, "reasoning.effort").String(),
		gjson.GetBytes(body, "reasoning_effort").String(),
		gjson.GetBytes(body, "output_config.effort").String(),
		gjson.GetBytes(body, "response.reasoning.effort").String(),
		gjson.GetBytes(body, "response.reasoning_effort").String(),
		gjson.GetBytes(body, "session.reasoning.effort").String(),
		gjson.GetBytes(body, "session.reasoning_effort").String(),
	}
	for _, effort := range efforts {
		if strings.EqualFold(strings.TrimSpace(effort), "ultra") {
			return errors.New(`reasoning effort "ultra" is not supported; use "max"`)
		}
	}

	models := []string{
		requestedModel,
		gjson.GetBytes(body, "model").String(),
		gjson.GetBytes(body, "session.model").String(),
	}
	for _, model := range models {
		if hasOpenAIUltraReasoningSuffix(model) {
			return errors.New(`model reasoning suffix "ultra" is not supported; use "max"`)
		}
	}
	return nil
}
