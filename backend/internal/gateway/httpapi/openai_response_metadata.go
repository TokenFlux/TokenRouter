package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// openai_response_metadata.go binds response identifiers to owners and
// providers and presents protocol errors.
// HTTP Responses continuation needs both provider affinity and downstream
// ownership for a response identifier. Binding records those two observations
// independently, and a storage failure is logged while response delivery
// continues. The current effective API-key group scopes both bindings after
// any group switch.
//
// An invalid nonstreaming upstream result normally becomes JSON with filtered
// headers. A Compact heartbeat can already have committed HTTP 200, so
// ProtocolError stops the heartbeat and uses a failed Responses event when
// that stream is active. This keeps the error compatible with the output
// format already established for the caller while recording the intended
// upstream failure in Ops.

// BindResponseProvider records provider affinity and, when present, the
// authenticated HTTP owner for a trimmed response ID. Missing output,
// provider, positive provider ID, response ID or store skips binding. The
// effective request group and ResponseTTL supply the namespace and expiry.
//
// Provider and owner writes are separate best-effort operations. A failed
// provider bind is logged before the owner bind is attempted, and an
// owner-bind error is also logged; callers continue response handling after
// either failure. Supply ResponseTTL when a state store is configured.
func (p *OpenAIResponseOutput) BindResponseProvider(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, responseID string) {
	if p == nil || provider == nil || provider.Record.ID <= 0 {
		return
	}
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	store := p.Responses
	if store == nil {
		return
	}
	groupID := OpenAIResponseGroupID(c)
	ttl := p.ResponseTTL()
	// Provider affinity and user ownership have separate storage operations. Keep
	// attempting owner publication after an affinity error so each available binding survives.
	gatewayprovider.LogOpenAIWSBindResponseProviderWarn(groupID, provider.Record.ID, responseID, store.BindResponseProvider(ctx, groupID, responseID, provider.Record.ID, ttl))
	if owner, ok := ResponseOwnerFromContext(c); ok {
		if err := store.BindHTTPResponseOwner(ctx, groupID, responseID, owner.UserID, owner.APIKeyID, p.ResponseTTL()); err != nil {
			logging.L().Warn(
				"openai.http_bind_response_owner_failed",
				zap.Int64("group_id", groupID),
				zap.Int64("provider_id", provider.Record.ID),
				zap.Int64("user_id", owner.UserID),
				zap.Int64("api_key_id", owner.APIKeyID),
				zap.String("response_id", gatewayprovider.TruncateOpenAIWSLogValue(responseID, gatewayprovider.OpenAIWSIDValueMaxLen)),
				zap.Error(err),
			)
		}
	}
}

// ProtocolError records a sanitized nonstreaming protocol failure and writes
// its client error. Callers supply a live request, response and output
// configuration after the reader has rejected the upstream representation.
// Empty messages use the standard invalid-response explanation.
//
// The output format depends on prior Compact or downstream SSE heartbeat
// commitment. Stopping a committed Compact stream emits a response.failed event
// under its existing HTTP status, and another committed SSE stream uses
// StreamError without mutating headers; otherwise filtered upstream headers
// precede a JSON 502 envelope. The returned error retains the same diagnostic
// message for execution logging.
func (p *OpenAIResponseOutput) ProtocolError(resp *http.Response, c *gin.Context, message string) error {
	message = logredact.SanitizeUpstreamQueries(strings.TrimSpace(message))
	if message == "" {
		message = "Upstream returned an invalid non-streaming response"
	}
	SetOpsUpstreamError(c, http.StatusBadGateway, message, "")
	// Stop the Compact heartbeat before taking over its writer. A committed
	// stream needs a terminal Responses event because its HTTP status is already sent.
	if OpenAICompactClientWantsStream(c) && StopOpenAICompactSSEKeepaliveCommitted(c) {
		WriteOpenAICompactSSEFailureMessage(c, http.StatusBadGateway, "upstream_error", message, MarkOpsStreamError)
		return fmt.Errorf("non-streaming openai protocol error: %s", message)
	}
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadGateway, "upstream_error", message, true)
		return fmt.Errorf("non-streaming openai protocol error: %s", message)
	}
	egressadapter.WriteFilteredHeaders(c.Writer.Header(), resp.Header, p.Headers)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	return fmt.Errorf("non-streaming openai protocol error: %s", message)
}

// OpenAIResponseGroupID reads the effective API-key group used for response
// ownership. Nil contexts, absent or differently typed keys, and keys without
// GroupID return zero; group switches therefore use the key currently stored
// in Gin.
func OpenAIResponseGroupID(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	value, exists := c.Get("api_key")
	if !exists {
		return 0
	}
	key, ok := value.(*apikey.APIKey)
	if !ok || key == nil || key.GroupID == nil {
		return 0
	}
	return *key.GroupID
}
