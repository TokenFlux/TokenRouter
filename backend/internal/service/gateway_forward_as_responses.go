package service

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/gin-gonic/gin"
)

func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel, modelCandidates...)
}

func writeResponsesError(c *gin.Context, statusCode int, code, message string) {
	output := gatewayhttp.ForwardConversionOutput{Context: c, Responses: true, Commit: func() { gatewayhttp.MarkResponseCommitted(c) }}
	output.Error(statusCode, code, message)
}
