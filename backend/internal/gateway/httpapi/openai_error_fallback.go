package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// openai_error_fallback.go decides whether forwarding failures still need
// client output after heartbeat or response commitment.
// An HTTP writer can be committed by Compact SSE heartbeats, Images JSON
// whitespace, or actual response content before forwarding reports an error.
// EnsureResponse stops heartbeat writers before combining commitment markers
// with heartbeat-adjusted byte counts, so an error follows the format already
// established for the client.
//
// A Compact stream containing only heartbeats still needs a Responses terminal
// event, while image padding can be followed by a JSON error. Meaningful image
// response bytes or a dedicated committed error suppress the fallback. The
// separate communication predicate uses byte differences, a cyber marker and
// recognized error prefixes as evidence; it infers prior delivery from those
// observations rather than decoding the existing body.

// EnsureResponse attempts a protocol failure response when the current writer
// still needs one. A nil context or writer returns false. Both heartbeat
// writers are stopped before output state is examined, and a Compact heartbeat
// changes the requested format to streaming.
//
// Heartbeat-adjusted bytes distinguish Images padding from response content,
// and Compact content from keepalive-only commitment. Existing meaningful
// committed output or an already delivered terminal SSE frame suppresses this
// fallback; padding can still receive JSON and Compact keepalives can receive a
// terminal event. Cyber-warning evidence supplies a request error and valid
// upstream status. Model-attribute failures retain their structured status and
// reason; remaining errors receive the generic 502 message. True reports
// attempted presentation, not successful network delivery.
func (h OpenAIErrorOutput) EnsureResponse(c *gin.Context, streamStarted bool, err error) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	// Stop both heartbeat writers before reading their counts or taking over
	// Writer; otherwise a late keepalive could change the format decision during fallback.
	compactKeepaliveCommitted := StopOpenAICompactSSEKeepaliveCommitted(c)
	if compactKeepaliveCommitted {
		streamStarted = true
	}
	imageKeepalivePresent := OpenAIImagesJSONKeepalivePresent(c)
	StopOpenAIImagesJSONKeepaliveCommitted(c)
	imageKeepalivePaddingOnly := false
	imageKeepaliveResponseWritten := false
	if imageKeepalivePresent {
		adjustedSize := HeartbeatAdjustedWrittenSize(c)
		imageKeepalivePaddingOnly = adjustedSize < 0
		imageKeepaliveResponseWritten = adjustedSize >= 0
	}
	compactKeepaliveHasMeaningfulOutput := compactKeepaliveCommitted && HeartbeatAdjustedWrittenSize(c) > 0
	// Dedicated commitment or an already delivered terminal SSE frame suppresses
	// replacement after meaningful output, while a keepalive-only Compact
	// commitment still needs its terminal Responses event.
	if TerminalSSEDelivered(c) ||
		(IsResponseCommitted(c) && (!compactKeepaliveCommitted || compactKeepaliveHasMeaningfulOutput)) ||
		(!compactKeepaliveCommitted && imageKeepaliveResponseWritten) {
		return false
	}
	if WriteOpenAIModelAttributeError(c, err, streamStarted) {
		return true
	}
	errType := "upstream_error"
	message := "Upstream request failed"
	status := http.StatusBadGateway
	if warning, ok := forwardcore.WarningFromError(err); ok && gatewayprovider.IsOpenAICyberWarningPayload(warning.ResponseBody, warning.Message) {
		errType = "invalid_request_error"
		message = gatewayprovider.ExtractOpenAICyberWarningMessage(warning.ResponseBody, warning.Message)
		if warning.StatusCode >= 400 && warning.StatusCode <= 599 {
			status = warning.StatusCode
		}
	}
	// Any written SSE response needs an in-stream error, while image whitespace
	// keeps JSON usable because padding alone contributes no completed response object.
	if c.Writer.Written() && !imageKeepalivePaddingOnly {
		streamStarted = true
	}
	h.StreamError(c, status, errType, message, streamStarted)
	return true
}

// ShouldLogOpenAIForwardFailureAsWarn reports whether an unwritten fallback
// left already-committed client output. A written fallback, missing context or
// missing writer returns false; otherwise the current writer's Written result
// determines the logging level.
func ShouldLogOpenAIForwardFailureAsWarn(c *gin.Context, wroteFallback bool) bool {
	if wroteFallback {
		return false
	}
	if c == nil || c.Writer == nil {
		return false
	}
	return c.Writer.Written()
}

// OpenAIForwardErrorAlreadyCommunicated infers whether forwarding delivered a
// terminal failure before returning err. Nil errors or missing writer state
// return false. Equal heartbeat-adjusted byte counts mean this attempt
// supplied no additional business output and suppress that inference.
//
// After a byte difference, a request cyber-policy mark or a recognized
// failure-message prefix supplies the remaining evidence. The helper does not
// decode the existing output or verify its terminal structure. Callers pass
// the writer size captured before forwarding and use this result to decide
// whether appending a generic error could duplicate a delivered failure.
func OpenAIForwardErrorAlreadyCommunicated(c *gin.Context, writerSizeBeforeForward int, err error) bool {
	if err == nil || c == nil || c.Writer == nil {
		return false
	}
	// Compare the attempt's starting size against the total adjusted byte count;
	// keepalives alone provide no evidence that the returned error reached the client.
	if HeartbeatAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return false
	}
	if GetOpsCyberPolicy(c) != nil {
		return true
	}

	msg := strings.TrimSpace(err.Error())
	for _, prefix := range []string{
		"upstream response failed:",
		"non-streaming openai protocol error:",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}
