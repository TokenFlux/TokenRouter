package ws

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"strings"
)

// ApplyReasoningEffortPolicy 将同一套分组策略应用到 WS 请求帧。
// requestModel 由调用方提供客户端模型，支持后续省略 model 的多轮帧。
func ApplyReasoningEffortPolicy(payload []byte, hooks *OpenAIIngressHooks, requestModel string) ([]byte, error) {
	if hooks == nil || (hooks.MaxReasoningEffort == "" && len(hooks.ReasoningEffortMappings) == 0) {
		return payload, nil
	}
	updated, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicyForModel(
		payload,
		hooks.MaxReasoningEffort,
		hooks.ReasoningEffortMappings,
		hooks.MaxReasoningEffortOverLimit,
		strings.TrimSpace(requestModel),
	)
	if err != nil {
		return payload, err
	}
	if changed {
		return updated, nil
	}
	return payload, nil
}
