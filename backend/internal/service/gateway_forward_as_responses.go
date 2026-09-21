package service

import (
	"context"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/gin-gonic/gin"
)

func (s *GatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *requeststate.ParsedRequest,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.AsResponses(ctx, &conversionExecutionAdapter{s: s, c: c, account: account, responses: true}, forwardcore.ConversionInput{OAuth: account.IsOAuth()}, body)
	return legacyForwardExecutionResult(result), err
}

func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel, modelCandidates...)
}

func writeResponsesError(c *gin.Context, statusCode int, code, message string) {
	output := gatewayhttp.ForwardConversionOutput{Context: c, Responses: true, Commit: func() { gatewayhttp.MarkResponseCommitted(c) }}
	output.Error(statusCode, code, message)
}
