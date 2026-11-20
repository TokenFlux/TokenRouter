package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
)

// openai_execution_errors.go formats forwarding failures for Messages, Chat,
// and Responses clients while retaining protocol-specific error data.
// Converted Chat and Messages errors need protocol-specific envelopes even
// when the upstream rejection is already structured JSON. Messages wrapping
// retains the raw error value, and invalid or absent error objects pass
// through as supplied so the HTTP adapter can preserve that upstream
// representation.
//
// Passthrough output rebuilds selected errors using fixed client messages and
// admits a validated Retry-After header. Compact output is offered the body
// before JSON is committed because a heartbeat may already have established
// SSE, and every writer also checks downstream SSE commitment so early idle
// keepalives end with a terminal protocol error frame rather than raw JSON.
// These helpers keep intended error status, JSON structure and streaming
// terminal framing distinct; the forwarding executor chooses health policy and
// retry eligibility before final presentation reaches them.

// WriteFastPolicyBlockedResponse marks a local fast-tier denial and writes its
// client error. Nil request context or blocked error is ignored; Compact
// heartbeat commitment selects a failed Responses event, otherwise the
// response is JSON 403.
func WriteFastPolicyBlockedResponse(c *gin.Context, err *tierpolicy.BlockedError) {
	if c == nil || err == nil {
		return
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	WriteForwardFastPolicyBlocked(c, err.Message, StopOpenAICompactSSEKeepaliveCommitted, func(c *gin.Context, status int, kind, message string) {
		WriteOpenAICompactSSEFailureMessage(c, status, kind, message, MarkOpsStreamError)
	})
}

// WriteForwardAnthropicError writes the supplied type and message in Anthropic
// JSON. When event-stream framing is already committed, it instead emits an
// Anthropic SSE error event and records the intended status for Ops.
func WriteForwardAnthropicError(c *gin.Context, statusCode int, errType, message string) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, statusCode, errType, message, true)
		return
	}
	c.JSON(statusCode, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardAnthropicErrorBody presents captured upstream error bytes as an
// Anthropic envelope, or as an Anthropic SSE error frame when event-stream
// framing is already committed. Callers supply the final status and an
// available writer. On uncommitted responses a valid error value is embedded from
// its raw JSON, retaining its nested representation and diagnostic fields.
//
// When error is absent or its raw value is invalid on an uncommitted response,
// the supplied body is written directly with JSON content type. That path can
// therefore contain invalid JSON if the caller supplied it. Committed SSE
// responses instead extract a safe error type and sanitized message so raw JSON
// is never appended onto an active event stream.
func WriteForwardAnthropicErrorBody(c *gin.Context, statusCode int, body []byte) {
	if CommittedSSE(c) {
		errType, _, message := extractSSEErrorFields(statusCode, body, "upstream_error", "Upstream request failed")
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, statusCode, errType, message, true)
		return
	}
	errorObject := gjson.GetBytes(body, "error")
	// A missing or invalid error value cannot be wrapped as structured Anthropic
	// JSON. Keep the captured rejection bytes available to the client in that case.
	if !errorObject.Exists() || !gjson.Valid(errorObject.Raw) {
		c.Data(statusCode, "application/json; charset=utf-8", body)
		return
	}
	// Embed the raw error value so nested code, param and unknown diagnostic
	// fields retain their representation during the outer protocol-envelope conversion.
	wrapped := []byte(`{"type":"error","error":` + errorObject.Raw + `}`)
	c.Data(statusCode, "application/json; charset=utf-8", wrapped)
}

// BuildForwardAnthropicStreamError encodes trimmed type and message in an
// Anthropic SSE error. The returned string includes event and data lines with
// a blank terminator; serialization failure uses the fixed policy-rejection
// envelope.
func BuildForwardAnthropicStreamError(errType, message string) string {
	payload, err := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
	// A failed serialization still needs complete protocol framing; return the
	// fixed rejection event so the caller can end the active client stream.
	if err != nil {
		return `event: error` + "\n" + `data: {"type":"error","error":{"type":"invalid_request_error","message":"Request blocked by upstream cyber-security policy"}}` + "\n\n"
	}
	return "event: error\ndata: " + string(payload) + "\n\n"
}

// WriteForwardChatError marks terminal response state before writing the Chat
// JSON error, or emits a terminal SSE error frame when event-stream framing is
// already committed. Callers provide the chosen client status and live context;
// the commitment marker prevents outer attempt handling from replacing that
// dedicated rejection.
func WriteForwardChatError(c *gin.Context, statusCode int, errType, message string) {
	MarkResponseCommitted(c)
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, statusCode, errType, message, true)
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardChatErrorBody commits response state and writes supplied JSON
// error bytes, or emits a terminal SSE error frame when event-stream framing is
// already committed. Uncommitted bodies are forwarded as supplied rather than
// validated here; committed SSE extracts safe error fields so raw JSON is never
// appended to an event stream.
func WriteForwardChatErrorBody(c *gin.Context, statusCode int, body []byte) {
	MarkResponseCommitted(c)
	if CommittedSSE(c) {
		errType, code, message := extractSSEErrorFields(statusCode, body, "upstream_error", "Upstream request failed")
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, statusCode, errType, code, message, true, false)
		return
	}
	c.Data(statusCode, "application/json; charset=utf-8", body)
}

// BuildForwardChatStreamError encodes a trimmed classification code and
// message in Chat SSE data. The error type is invalid_request_error, and a
// serialization failure returns the fixed cyber-policy payload with complete
// SSE termination.
func BuildForwardChatStreamError(code, message string) string {
	payload, err := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
	// A failed serialization still needs complete protocol framing; return the
	// fixed rejection event so the caller can end the active client stream.
	if err != nil {
		return `data: {"error":{"type":"invalid_request_error","code":"cyber_policy","message":"Request blocked by upstream cyber-security policy"}}` + "\n\n"
	}
	return "data: " + string(payload) + "\n\n"
}

// WriteForwardResponsesFallbackError writes the selected error type and
// message in Responses JSON. When event-stream framing is already committed, it
// instead emits a terminal SSE failure event and records the intended status.
func WriteForwardResponsesFallbackError(c *gin.Context, statusCode int, errType, message string) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, statusCode, errType, message, true)
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardPassthroughErrorHeaders installs JSON/no-store headers and
// replaces Retry-After with a valid upstream value. Nil destination is
// ignored; absent source or invalid delay removes any stale retry header from
// the destination.
func WriteForwardPassthroughErrorHeaders(dst, src http.Header) {
	if dst == nil {
		return
	}
	dst.Set("Content-Type", "application/json; charset=utf-8")
	dst.Set("Cache-Control", "no-store")
	dst.Del("Retry-After")
	if src == nil {
		return
	}
	rawRetryAfter := strings.TrimSpace(src.Get("Retry-After"))
	if ValidForwardPassthroughRetryAfter(rawRetryAfter, time.Now()) {
		dst.Set("Retry-After", rawRetryAfter)
	}
}

// ValidForwardPassthroughRetryAfter accepts positive unsigned decimal seconds
// or an HTTP date later than now. Input is trimmed, zero and overflowing
// numbers fail validation, and no maximum future delay is imposed by this
// passthrough predicate.
func ValidForwardPassthroughRetryAfter(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	delaySeconds := true
	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			delaySeconds = false
			break
		}
	}
	if delaySeconds {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		return err == nil && seconds > 0
	}
	parsed, err := http.ParseTime(raw)
	return err == nil && parsed.After(now)
}

// WriteSanitizedForwardPassthroughError selects a fixed message from the
// upstream status and writes its envelope. Upstream 401 and 403 become client
// 502 because they describe provider authentication or access; other statuses
// retain their code, with a generic service message for 5xx.
func WriteSanitizedForwardPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header, compact func(*gin.Context, int, []byte) bool) {
	downstreamStatus := upstreamStatus
	message := "Upstream request failed"
	// Provider authentication and access failures should not look like failures
	// of the caller's TokenRouter key, so those statuses use a gateway error.
	switch upstreamStatus {
	case http.StatusUnauthorized:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream authentication failed"
	case http.StatusForbidden:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream access denied"
	default:
		if upstreamStatus >= http.StatusInternalServerError {
			message = "Upstream service temporarily unavailable"
		}
	}
	WriteForwardPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message, compact)
}

// WriteForwardPassthroughErrorEnvelope offers a rebuilt error body to the
// Compact callback before JSON output, and checks downstream SSE commitment so
// an already-started stream receives a terminal SSE error instead of raw JSON.
// Nil contexts are ignored; callers supply compact, and a true callback result
// ends output because it already handled the response format.
func WriteForwardPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string, compact func(*gin.Context, int, []byte) bool) {
	if c == nil {
		return
	}
	// Fixed string fields form the rebuilt client envelope, keeping the upstream
	// body outside this final presentation while Compact selects its output format.
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	// Offer the envelope before JSON commitment because the callback may need
	// to end an already-started Compact stream under its existing HTTP status.
	if compact(c, downstreamStatus, body) {
		return
	}
	// Downstream keepalives can commit an SSE stream outside Compact; emit a
	// terminal stream error instead of mutating headers and appending raw JSON.
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, downstreamStatus, "upstream_error", message, true)
		return
	}
	WriteForwardPassthroughErrorHeaders(c.Writer.Header(), upstreamHeaders)
	c.Data(downstreamStatus, "application/json; charset=utf-8", body)
}

// WriteForwardFastPolicyBlocked stops Compact heartbeat output before writing
// a fast-tier rejection, and checks downstream SSE commitment for non-Compact
// streams. Callers supply stop and failure callbacks; a committed Compact
// heartbeat uses failure, another committed SSE stream uses the client protocol
// terminal event, and an uncommitted response receives JSON 403.
func WriteForwardFastPolicyBlocked(c *gin.Context, message string, stop func(*gin.Context) bool, failure func(*gin.Context, int, string, string)) {
	// A stopped, committed Compact heartbeat requires a terminal event; an
	// uncommitted writer can still report the permission denial with HTTP 403 JSON.
	if stop(c) {
		failure(c, http.StatusForbidden, "permission_error", message)
		return
	}
	if CommittedSSE(c) {
		writeCommittedClientSSEError(c, http.StatusForbidden, "permission_error", "", message)
		return
	}
	c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": message}})
}

// WriteOpenAIForwardRejection writes an OpenAI JSON rejection with an optional
// parameter field, or emits a terminal SSE error when event-stream framing is
// already committed. Nil contexts are ignored, and an empty param omits that
// field; callers select the status and classification before writing.
func WriteOpenAIForwardRejection(c *gin.Context, status int, kind, message, param string) {
	if c == nil {
		return
	}
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, status, kind, message, true)
		return
	}
	payload := gin.H{"type": kind, "message": message}
	if param != "" {
		payload["param"] = param
	}
	c.JSON(status, gin.H{"error": payload})
}
