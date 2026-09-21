package service

import (
	"context"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
)

func (s *GatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *requeststate.ParsedRequest,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.AsChat(ctx, &conversionExecutionAdapter{s: s, c: c, account: account, responses: false}, forwardcore.ConversionInput{OAuth: account.IsOAuth()}, body)
	return legacyForwardExecutionResult(result), err
}

func extractCCReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel, modelCandidates...)
}
