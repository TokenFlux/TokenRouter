package handler

import (
	"net/http"
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// Responses handles OpenAI Responses API endpoint for Anthropic platform groups.
// POST /v1/responses
// This converts Responses API requests to Anthropic format, forwards to Anthropic
// upstream, and converts responses back to Responses format.
func (h *GatewayHandler) Responses(c *gin.Context) { h.NewCompatibleTextHTTPHandler().Responses(c) }

// responsesErrorResponse writes an error in OpenAI Responses API format.
func (h *GatewayHandler) responsesErrorResponse(c *gin.Context, status int, code, message string) {
	gatewayhttp.WriteCompatibleResponsesError(c, status, code, message)
}

// handleResponsesFailoverExhausted writes a failover-exhausted error in Responses format.
func (h *GatewayHandler) handleResponsesFailoverExhausted(c *gin.Context, lastErr *service.UpstreamFailoverError, streamStarted bool) {
	if lastErr != nil {
		copyFailoverRetryAfter(c, lastErr.ResponseHeaders)
	}
	statusCode := http.StatusBadGateway
	if lastErr != nil && lastErr.StatusCode > 0 {
		statusCode = lastErr.StatusCode
	}
	status, code, message := statusCode, "server_error", "All available accounts exhausted"
	if lastErr != nil && lastErr.IsCredentialFailure() {
		status, message = credentialFailoverClientResponse(lastErr)
	} else if lastErr != nil && lastErr.IsOpenAICapacityShed() && strings.TrimSpace(lastErr.ClientMessage) != "" {
		status = lastErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		message = lastErr.ClientMessage
	} else if lastErr != nil && service.IsOpenAISilentRefusalErrorBody(lastErr.ResponseBody) {
		service.SetOpsUpstreamError(c, statusCode, service.OpenAISilentRefusalClientMessage(), "")
		status, code, message = http.StatusBadGateway, "upstream_error", service.OpenAISilentRefusalClientMessage()
	} else if lastErr != nil && statusCode == http.StatusTooManyRequests {
		status, code, message = http.StatusTooManyRequests, "rate_limit_error", "All available accounts are currently rate-limited. Please retry later."
	}
	if streamStarted {
		// A slot-wait heartbeat commits HTTP 200 before any upstream response.
		// In that case a terminal frame is still required; once any semantic or
		// official terminal bytes exist, preserve them without appending a second
		// generic response.failed.
		service.MarkOpsStreamError(c, code, message, status)
		if c != nil && c.Writer != nil && (c.Writer.Size() <= 0 || gatewayStreamHasOnlyHeartbeats(c)) {
			writeResponsesFailedSSE(c, code, "", message)
		}
		return
	}
	h.responsesErrorResponse(c, status, code, message)
}
