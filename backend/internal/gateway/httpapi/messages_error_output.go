package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// messages_error_output.go writes Messages and compatible protocol failures
// using failover evidence and existing output state.
// An HTTP writer can contain heartbeat or partial stream bytes before a
// Messages attempt has communicated its final result. Fallback presentation
// therefore checks semantic commitment separately from physical writes, and
// distinguishes a new non-SSE error body from stream output that may still
// need a terminal event. The chosen client route determines the error
// envelope.
//
// Failover exhaustion retains retry guidance and interprets credential
// failure, capacity shedding, silent refusal and rate limits before ordinary
// status mapping. Responses streams append a generic terminal only while
// output is empty or contains heartbeats alone; Chat exhaustion emits a stream
// error for heartbeat-only output and returns unchanged once content has been
// delivered. Direct error helpers also switch to terminal SSE frames when
// early heartbeats have already committed an event stream. Gemini error rules
// take precedence over default Google status messages when a rule matches the
// upstream body.

// AnthropicForwardErrorOutput writes Messages errors. Callers manage commit and stream state.
type AnthropicForwardErrorOutput struct {
	// Context supplies the active writer. Callers manage semantic commitment and
	// stream state before invoking these error output methods.
	Context *gin.Context
}

// MessagesErrorOutput writes protocol errors using shared passthrough rules.
type MessagesErrorOutput struct {
	// Rules supplies optional failover presentation matches; nil leaves Anthropic
	// and Gemini exhaustion on their corresponding default error mappings.
	Rules *errorpolicy.ErrorPassthroughService
}

// Message writes the supplied Anthropic error envelope as JSON, or as an SSE
// error event when the response has already committed event-stream framing.
// Callers supply a usable context and manage semantic commitment separately.
func (o AnthropicForwardErrorOutput) Message(status int, kind, message string) {
	if CommittedSSE(o.Context) {
		WriteAnthropicStreamError(o.Context, status, kind, "", message, true, MarkOpsStreamError)
		return
	}
	o.Context.JSON(status, gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
}

// Raw writes supplied upstream bytes as application/json with the requested
// status, or emits a protocol SSE error event when event-stream framing is
// already committed. Uncommitted bytes pass through unvalidated; committed SSE
// extracts safe error fields so raw JSON is never appended to an event stream.
func (o AnthropicForwardErrorOutput) Raw(status int, body []byte) {
	if CommittedSSE(o.Context) {
		errType, code, message := extractSSEErrorFields(status, body, "upstream_error", "Upstream request failed")
		WriteAnthropicStreamError(o.Context, status, errType, code, message, true, MarkOpsStreamError)
		return
	}
	o.Context.Data(status, "application/json", body)
}

// ConcurrencyError maps a user or provider slot-acquisition failure into
// client error fields. It delegates JSON or route-specific SSE presentation
// using the supplied stream-started state, retaining the slot-specific
// optional error code.
func (h MessagesErrorOutput) ConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := ConcurrencyErrorResponse(err, slotType)
	h.StreamErrorWithCode(c, status, errType, code, message, streamStarted)
}

// ExhaustedStatus presents a mapped upstream status when no failure body is
// available. It records the original upstream status for Ops before writing
// the client-facing error using the supplied stream state.
func (h MessagesErrorOutput) ExhaustedStatus(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := AnthropicUpstreamError(statusCode)
	SetOpsUpstreamError(c, statusCode, errMsg, "")
	h.StreamError(c, status, errType, errMsg, streamStarted)
}

// StreamError delegates a protocol error without an additional error code. The
// supplied stream-started state determines JSON or route-specific SSE output
// through StreamErrorWithCode, which also handles model-attribute response
// overrides.
func (h MessagesErrorOutput) StreamError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.StreamErrorWithCode(c, status, errType, "", message, streamStarted)
}

// StreamErrorWithCode presents a protocol error with an optional code and
// current stream state. Model-attribute state can first replace the status and
// message; the shared stream writer records applicable Ops error observations.
func (h MessagesErrorOutput) StreamErrorWithCode(c *gin.Context, status int, errType, code, message string, streamStarted bool) {
	status, message = modelAttributeError(c, status, message)
	WriteAnthropicStreamError(c, status, errType, code, message, streamStarted, MarkOpsStreamError)
}

// EnsureResponse attempts a generic upstream failure when semantic output has
// not been committed. Nil context or writer, or an existing commitment,
// returns false; true means presentation was attempted rather than proving
// that the writer accepted every byte.
//
// Physical headers can already be written by a heartbeat or partial output
// while the semantic marker remains unset. That condition promotes the local
// stream flag before delegation, so the existing HTTP status stays in place
// and the client route selects an SSE failure envelope instead of a
// replacement JSON response.
func (h MessagesErrorOutput) EnsureResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if IsResponseCommitted(c) || TerminalSSEDelivered(c) {
		return false
	}
	// Already-written headers prevent a new HTTP error status; promote streaming
	// here so fallback adds the route-specific failure to existing output instead.
	if c.Writer.Written() {
		streamStarted = true
	}
	h.StreamError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
	return true
}

// ForwardErrorAlreadyCommunicated reports whether a forwarding error
// accompanied a changed byte count and a nonempty, non-SSE content type.
// Callers capture writerSizeBeforeForward before the attempt; nil errors,
// missing context/writer, unchanged size and empty content type return false.
// A header-only transition from Size=-1 to zero can satisfy this predicate.
//
// The predicate does not parse the body or prove a complete error envelope. It
// treats newly written JSON or other non-SSE output as communicated, while SSE
// heartbeats and partial data remain eligible for outer terminal-error
// handling even when they changed the writer size.
func ForwardErrorAlreadyCommunicated(c *gin.Context, writerSizeBeforeForward int, err error) bool {
	if err == nil || c == nil || c.Writer == nil {
		return false
	}
	if HeartbeatAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return false
	}

	// Normalize the header before distinguishing SSE from plain output; byte
	// growth alone also includes heartbeats that have not communicated this failure.
	contentType := strings.ToLower(strings.TrimSpace(c.Writer.Header().Get("Content-Type")))
	if contentType == "" {
		return false
	}
	return !strings.Contains(contentType, "text/event-stream")
}

// Error delegates a plain Anthropic JSON error without an extra code. Callers
// choose this path before stream output, since the shared error writer uses
// normal HTTP status and envelope presentation.
func (h MessagesErrorOutput) Error(c *gin.Context, status int, errType, message string) {
	h.ErrorWithCode(c, status, errType, "", message)
}

// ErrorWithCode delegates Anthropic JSON output with the supplied optional
// code, or emits a terminal SSE error when event-stream framing is already
// committed. Callers supply the target status and code; early heartbeats still
// produce a valid protocol error event instead of appending raw JSON.
func (h MessagesErrorOutput) ErrorWithCode(c *gin.Context, status int, errType, code, message string) {
	if CommittedSSE(c) {
		WriteAnthropicStreamError(c, status, errType, code, message, true, MarkOpsStreamError)
		return
	}
	WriteAnthropicError(c, status, errType, code, message)
}

// Exhausted delegates classified failover presentation with optional
// configured rules and stream state. The shared Anthropic writer also
// recognizes OpenAI silent refusal bodies and chooses the resulting client
// message.
func (h MessagesErrorOutput) Exhausted(c *gin.Context, failure *forwardcore.UpstreamFailoverError, platform string, started bool) {
	var rules ErrorRuleMatcher
	if h.Rules != nil {
		rules = h.Rules
	}
	WriteAnthropicFailover(c, failure, platform, started, rules, forwardcore.IsOpenAISilentRefusalErrorBody, forwardcore.OpenAISilentRefusalClientMessage())
}

// ResponsesError writes an OpenAI Responses error envelope with code and
// message fields. When event-stream framing is already committed, it instead
// delegates a terminal SSE failure event and records the intended status.
func (h MessagesErrorOutput) ResponsesError(c *gin.Context, status int, code, message string) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, code, code, message, true, false)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// ResponsesExhausted presents final failover failure for a Responses client,
// retaining upstream retry guidance when available. Credential failure takes
// precedence over a capacity-shed message, then silent refusal and rate
// limiting, before generic exhaustion. A nil failure uses a 502 generic error.
//
// When streaming has started, the method records the chosen Ops error and
// appends response.failed only if the writer is empty or contains heartbeat
// output alone. Existing semantic or terminal content remains in place,
// avoiding a second generic terminal after useful converted output;
// nonstreaming requests receive the chosen JSON error.
func (h MessagesErrorOutput) ResponsesExhausted(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if lastErr != nil {
		CopyFailoverRetryAfter(c, lastErr.ResponseHeaders)
	}
	statusCode := http.StatusBadGateway
	if lastErr != nil && lastErr.StatusCode > 0 {
		statusCode = lastErr.StatusCode
	}
	status, code, message := statusCode, "server_error", "All available providers exhausted"
	if lastErr != nil && lastErr.IsCredentialFailure() {
		status, message = CredentialFailoverClientResponse(lastErr)
	} else if lastErr != nil && gatewayprovider.IsOpenAICapacityShed(lastErr) && strings.TrimSpace(lastErr.ClientMessage) != "" {
		status = lastErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		message = lastErr.ClientMessage
	} else if lastErr != nil && forwardcore.IsOpenAISilentRefusalErrorBody(lastErr.ResponseBody) {
		SetOpsUpstreamError(c, statusCode, forwardcore.OpenAISilentRefusalClientMessage(), "")
		status, code, message = http.StatusBadGateway, "upstream_error", forwardcore.OpenAISilentRefusalClientMessage()
	} else if lastErr != nil && statusCode == http.StatusTooManyRequests {
		status, code, message = http.StatusTooManyRequests, "rate_limit_error", "All available providers are currently rate-limited. Please retry later."
	}
	if streamStarted || CommittedSSE(c) {
		// Admission or downstream heartbeats can flush HTTP output without a terminal
		// result. Append a failure only for that empty/heartbeat state, preserving
		// any semantic output already delivered by the Responses converter.
		MarkOpsStreamError(c, code, message, status)
		if c != nil && c.Writer != nil && HeartbeatAdjustedWrittenSize(c) <= 0 {
			WriteResponsesFailedSSE(c, code, "", message, ErrorRequestID(c), ErrorRequestModel(c))
		}
		return
	}
	h.ResponsesError(c, status, code, message)
}

// ChatError writes an OpenAI Chat Completions error envelope with type and
// message fields. When event-stream framing is already committed, it instead
// emits a terminal SSE error frame and records the intended status.
func (h MessagesErrorOutput) ChatError(c *gin.Context, status int, errType, message string) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, true)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
}

// ChatExhausted presents Chat failover exhaustion, retaining upstream retry
// guidance when a failure exists. When streaming or early heartbeats already
// committed SSE, heartbeat-only output receives an SSE error event while
// streams with delivered content return unchanged.
//
// Credential failure wins over a capacity-shed client message, while silent
// refusal is checked before ordinary upstream status presentation. The
// original status remains available for Ops when a silent refusal becomes a
// generic upstream-error response, so client output and upstream diagnostics
// describe their respective failure boundaries.
func (h MessagesErrorOutput) ChatExhausted(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	started := streamStarted || CommittedSSE(c)
	// Return unchanged once meaningful Chat stream content has been delivered,
	// avoiding an appended error after partial output owned by the stream reader.
	if started && HeartbeatAdjustedWrittenSize(c) > 0 {
		return
	}
	if lastErr != nil {
		CopyFailoverRetryAfter(c, lastErr.ResponseHeaders)
	}
	statusCode := http.StatusBadGateway
	if lastErr != nil && lastErr.StatusCode > 0 {
		statusCode = lastErr.StatusCode
	}
	status, errType, message := statusCode, "server_error", "All available providers exhausted"
	if lastErr != nil && lastErr.IsCredentialFailure() {
		status, message = CredentialFailoverClientResponse(lastErr)
	} else if lastErr != nil && gatewayprovider.IsOpenAICapacityShed(lastErr) && strings.TrimSpace(lastErr.ClientMessage) != "" {
		status = lastErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		message = lastErr.ClientMessage
	} else if lastErr != nil && forwardcore.IsOpenAISilentRefusalErrorBody(lastErr.ResponseBody) {
		SetOpsUpstreamError(c, statusCode, forwardcore.OpenAISilentRefusalClientMessage(), "")
		status, errType, message = http.StatusBadGateway, "upstream_error", forwardcore.OpenAISilentRefusalClientMessage()
	}
	if started {
		DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, true)
		return
	}
	h.ChatError(c, status, errType, message)
}

// GeminiExhausted presents exhaustion using a Google error envelope. A
// matching configured rule on a nonempty upstream body selects status, message
// and monitoring behavior before defaults; otherwise it records the upstream
// failure and maps its status.
func (h MessagesErrorOutput) GeminiExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError) {
	if failoverErr == nil {
		WriteGoogleError(c, http.StatusBadGateway, "Upstream request failed")
		return
	}

	CopyFailoverRetryAfter(c, failoverErr.ResponseHeaders)
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody

	// A matching Gemini rule owns final status and message selection; returning
	// from this branch prevents default mapping from overwriting its chosen response.
	if h.Rules != nil && len(responseBody) > 0 {
		if rule := h.Rules.MatchRule(failoverErr.ProviderPlatform(capability.PlatformGemini), statusCode, responseBody); rule != nil {
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			msg := upstream.ExtractErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				MarkOpsSkipMonitoring(c)
			}

			WriteGoogleError(c, respCode, msg)
			return
		}
	}

	// Record the upstream status and message for Ops error logs.
	upstreamMsg := upstream.ExtractErrorMessage(responseBody)
	SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	status, message := GeminiUpstreamError(statusCode)
	WriteGoogleError(c, status, message)
}

// GeminiUpstreamError maps upstream statuses to safe Google-compatible client
// failure messages. Authentication and ordinary server failures become 502,
// rate limits retain 429, and upstream overload status 529 becomes 503; all
// other statuses use generic 502.
func GeminiUpstreamError(statusCode int) (int, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "Upstream request failed"
	}
}
