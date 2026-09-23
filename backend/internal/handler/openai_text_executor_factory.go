package handler

import (
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
)

// NewOpenAITextExecutor 在唯一 Recorder 已绑定后构造，供 Responses/Chat/Messages 共用。
func (h *OpenAIGatewayHandler) NewOpenAITextExecutor() *textflow.ResponsesExecutor {
	maxSwitches := 0
	if h != nil {
		maxSwitches = h.maxAccountSwitches
	}
	return textflow.NewResponsesExecutor(&fixedOpenAITextRuntime{dependencies: newOpenAIExecutionDependencies(h)}, textflow.ResponseOptions{MaxSwitches: maxSwitches}, textflow.ResponseOptions{MaxSwitches: maxSwitches, FirstOutputBudget: true})
}
