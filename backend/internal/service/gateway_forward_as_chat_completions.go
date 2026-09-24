package service

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func extractCCReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel, modelCandidates...)
}
