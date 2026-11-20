package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// upstream_response_limit.go reads size-limited upstream response bodies and
// presents overflow failures through protocol-specific callbacks.
// Buffered upstream responses need a size bound before protocol decoding
// consumes a complete body. The shared reader detects overflow and retains the
// size sentinel; HTTP reporting records that failure and invokes the caller's
// protocol-specific output while leaving body cleanup with the caller.

// TooLargeWriter writes a response-size error in the inbound protocol's format.
type TooLargeWriter func(*gin.Context)

// ReadUpstreamResponseBody delegates bounded body reading and reports overflow
// through Ops before optional client output. Nonpositive limits use the shared
// reader default; callers keep positive limits below MaxInt64 for maxBytes+1.
// Read
// failures return no body and callers retain reader cleanup; only the size
// sentinel invokes onTooLarge.
func ReadUpstreamResponseBody(reader io.Reader, maxBytes int64, c *gin.Context, onTooLarge TooLargeWriter) ([]byte, error) {
	body, err := httpclient.ReadResponseBodyLimited(reader, maxBytes)
	if err != nil {
		// Report only classified overflow through this client callback, keeping
		// ordinary read errors available to the caller's separate retry and failure
		// handling.
		if errors.Is(err, httpclient.ErrResponseBodyTooLarge) {
			SetOpsUpstreamError(c, http.StatusBadGateway, "upstream response too large", "")
			if onTooLarge != nil {
				onTooLarge(c)
			}
		}
		return nil, err
	}
	return body, nil
}

// AnthropicResponseTooLarge writes a 502 size-limit failure using the Messages
// envelope. When event-stream framing is already committed, it emits an
// Anthropic SSE error frame and records the intended status instead of JSON.
func AnthropicResponseTooLarge(c *gin.Context) {
	WriteAnthropicStreamError(c, http.StatusBadGateway, "upstream_error", "", "Upstream response too large", CommittedSSE(c), MarkOpsStreamError)
}

// OpenAIResponseTooLarge writes a 502 size-limit failure using the OpenAI
// envelope. When event-stream framing is already committed, it emits a terminal
// SSE error event and records the intended status instead of JSON.
func OpenAIResponseTooLarge(c *gin.Context) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadGateway, "upstream_error", "Upstream response too large", true)
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Upstream response too large"}})
}
