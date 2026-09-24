package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) RecordMessagesStreamError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamRequestID, kind, message string) {
	if c == nil {
		return
	}
	message = logredact.SanitizeUpstreamQueries(message)
	SetOpsUpstreamError(c, http.StatusBadGateway, message, "")
	event := ops.OpsUpstreamErrorEvent{
		Platform:           capability.PlatformOpenAI,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	}
	if account != nil {
		event.Platform = account.Record.Platform
		event.AccountID = account.Record.ID
		event.AccountName = account.Record.Name
	}
	AppendOpsUpstreamError(c, event)
}

func (p *OpenAIResponseOutput) ReadBufferedTerminal(
	resp *http.Response,
	c *gin.Context,
	logPrefix string,
	requestID string,
) (*protocolopenai.ResponsesResponse, protocolopenai.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
	return openai.ReadCompatBufferedTerminal(resp, p.BufferedOptions(c, logPrefix, requestID))
}
