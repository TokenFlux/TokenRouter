package service

import (
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel, modelCandidates...)
}
