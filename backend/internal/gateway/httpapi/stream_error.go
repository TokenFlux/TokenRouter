package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// stream_error.go writes terminal SSE failure events, extracts safe error
// fields from captured upstream bodies, and derives Responses IDs and codes.
// After streaming or early header-wait heartbeats commit HTTP 200
// text/event-stream, clients cannot accept plain JSON error responses.
// Responses clients recognize failure through a terminal response.failed object
// carrying an ID, creation timestamp, and empty output array so they can finish
// response state even when generation never produced output.
//
// Local terminal error delivery sends frames through ResponseSink with upstream
// timing cleared and Terminal enabled. Clearing timing prevents gateway errors
// from establishing upstream TTFT, while the terminal marker serializes writes
// against DownstreamSSEKeepalive and stops further idle beats atomically once
// accepted. Captured upstream error bodies also pass through field extraction
// and query sanitization before entering a committed stream.

// ResponsesFailedError is the error object in an OpenAI Responses failure.
type ResponsesFailedError struct {
	Code    string `json:"code"`    // client machine-readable error code
	Message string `json:"message"` // client-safe failure description text
}

// ResponsesFailedBody is the response object carried by a failure event. The
// writer supplies an empty Output slice and CreatedAt for clients that require
// these fields.
type ResponsesFailedBody struct {
	// synthetic response correlation identifier
	ID        string `json:"id"`
	Object    string `json:"object"`     // response object type label
	CreatedAt int64  `json:"created_at"` // Unix seconds
	// optional trimmed request model
	Model  string `json:"model,omitempty"`
	Status string `json:"status"` // failed response status label
	// Output is an empty array in WriteResponsesFailedSSE, allowing clients
	// to read the ordinary response output shape even when generation
	// failed before content.
	Output []any                `json:"output"`
	Error  ResponsesFailedError `json:"error"` // terminal client error details
}

// ResponsesFailedEvent is the top-level JSON object on an SSE data line. It
// omits sequence_number because this writer does not know the last emitted
// sequence.
type ResponsesFailedEvent struct {
	Type     string              `json:"type"`     // terminal failure event type
	Response ResponsesFailedBody `json:"response"` // failed response metadata object
}

// WriteResponsesFailedSSE handles a terminal response.failed event for an
// already started stream. Callers provide a Gin writer and end the request
// after true, including marshal or write failure attached to Gin. False means
// Flush is unavailable, so the caller may choose another protocol error path;
// committed HTTP status remains fixed.
func WriteResponsesFailedSSE(c *gin.Context, errType, code, message, requestID, model string) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if _, ok := c.Writer.(http.Flusher); !ok {
		return false
	}

	payload, err := json.Marshal(ResponsesFailedEvent{
		Type: "response.failed",
		Response: ResponsesFailedBody{
			ID:        SynthesizeResponseID(requestID),
			Object:    "response",
			CreatedAt: time.Now().Unix(),
			Model:     strings.TrimSpace(model),
			Status:    "failed",
			Output:    []any{},
			Error: ResponsesFailedError{
				Code:    MapResponsesErrorCode(errType, code),
				Message: message,
			},
		},
	})
	// Attach serialization failure to the request and return handled,
	// preventing callers from appending a second incompatible envelope to
	// the started stream.
	if err != nil {
		_ = c.Error(err)
		return true
	}

	return emitLocalTerminalSSE(c, fmt.Appendf(nil, "event: response.failed\ndata: %s\n\n", payload))
}

// emitLocalTerminalSSE writes a gateway-generated terminal SSE frame through
// the full response writer chain without upstream timing attribution. Missing
// contexts or writers without http.Flusher return false so callers may choose
// an alternate fallback.
//
// Setting Terminal on ResponseSink serializes the write and flush against
// DownstreamSSEKeepalive and stops further idle beats atomically upon accepted
// delivery. Recording localTerminalSSEWrittenKey before emission also lets
// TerminalSSEDelivered suppress duplicate fallback errors when keepalive
// middleware is absent or the client write fails. Delivery errors are attached
// to Gin and still return true because a committed event stream cannot revert
// to a plain JSON status response.
func emitLocalTerminalSSE(c *gin.Context, data []byte) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if _, ok := c.Writer.(http.Flusher); !ok {
		return false
	}
	c.Set(localTerminalSSEWrittenKey, true)
	// Clear timing so local gateway errors never attribute upstream TTFT while
	// still passing through Ops capture and the terminal keepalive barrier.
	sink := NewResponseSink(c)
	sink.timing = nil
	if err := sink.Emit(upstream.OutputEvent{Data: data, Terminal: true, Flush: true}); err != nil {
		_ = c.Error(err)
	}
	return true
}

// extractSSEErrorFields extracts safe client error type, optional code, and
// sanitized message from captured upstream error bytes when a committed event
// stream cannot emit raw JSON. Missing or blank fields fall back to defaultType
// and defaultMessage, with HTTP 400 defaulting its type to invalid_request_error.
func extractSSEErrorFields(status int, body []byte, defaultType, defaultMessage string) (string, string, string) {
	errType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	if errType == "" {
		if status == http.StatusBadRequest {
			errType = "invalid_request_error"
		} else {
			errType = defaultType
		}
	}
	code := strings.TrimSpace(upstream.ExtractErrorCode(body))
	message := strings.TrimSpace(logredact.SanitizeUpstreamQueries(upstream.ExtractErrorMessage(body)))
	if message == "" {
		message = defaultMessage
	}
	return errType, code, message
}

// InboundIsResponses recognizes Responses roots and subpaths for terminal error
// formatting. It prefers the matched Gin path, then falls back to URL.Path when
// unmatched; nil contexts and empty paths do not identify a Responses entry.
func InboundIsResponses(c *gin.Context) bool {
	if c == nil {
		return false
	}
	p := strings.TrimRight(c.FullPath(), "/")
	// Unmatched test contexts and early error paths lack a Gin route
	// pattern; use their URL path to select the same terminal Responses
	// envelope.
	if p == "" && c.Request != nil && c.Request.URL != nil {
		p = strings.TrimRight(c.Request.URL.Path, "/")
	}
	if p == "" {
		return false
	}
	return strings.HasSuffix(p, "/responses") || strings.Contains(p, "/responses/")
}

// MapResponsesErrorCode uses the first supplied code when that value is
// nonempty. Later variadic entries are ignored; otherwise known error types are
// mapped, unknown types pass through, and server or empty types use
// server_error.
func MapResponsesErrorCode(errType string, code ...string) string {
	if len(code) > 0 && code[0] != "" {
		return code[0]
	}
	switch errType {
	case "rate_limit_error":
		return "rate_limit_exceeded"
	case "invalid_request_error":
		return "invalid_request"
	case "permission_error":
		return "permission_denied"
	case "authentication_error":
		return "authentication_failed"
	case "upstream_error":
		return "upstream_error"
	case "server_error", "api_error", "":
		return "server_error"
	default:
		return errType
	}
}

// SynthesizeResponseID prefixes a trimmed telemetry request ID after removing
// hyphens. Blank IDs use a newly generated UUID with its hyphens removed,
// allowing a failure event to carry an ID even without request correlation
// metadata.
func SynthesizeResponseID(requestID string) string {
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		return "resp_" + strings.ReplaceAll(requestID, "-", "")
	}
	// Generate a response identifier when correlation is absent, retaining
	// the resp_ shape expected by clients reading terminal Responses
	// events.
	return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
