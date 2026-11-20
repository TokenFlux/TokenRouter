package httpapi

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// openai_error_output.go writes OpenAI and Anthropic compatibility protocol
// errors and records their intended status after stream framing begins.
// A Compact or downstream idle heartbeat can commit HTTP 200 text/event-stream
// before an error is ready. Error output first checks those commitment signals
// and emits a failed Responses terminal on Responses routes or an event: error
// frame on other started streams, stopping further heartbeats atomically.
//
// Stream observations distinguish ordinary error reporting from failures
// counted toward SLA, even though the committed HTTP status remains 200. The
// callbacks carry that intended status independently of wire status, while an
// uncommitted response still receives JSON with the requested HTTP status.

// OpenAIErrorOutput binds synchronous protocol error writes to request
// observation callbacks. DefaultOpenAIErrorOutput supplies the callbacks used
// by HTTP, WebSocket and media execution adapters.
type OpenAIErrorOutput struct {
	// stopCompact prevents later Compact beats and waits for an active write.
	// Its result includes ordinary reader heartbeats and failed controller writes.
	stopCompact func(*gin.Context) bool
	// markStream and markFailure receive the intended error status after streaming
	// begins. WriteStreamingErrorWithCode selects them with countTowardsSLA,
	// keeping observation independent of the HTTP status already committed.
	// ordinary stream error observation
	markStream func(*gin.Context, string, string, int)
	// SLA stream failure observation
	markFailure func(*gin.Context, string, string, string, int)
	// metadata supplies request ID and model when a stopped Compact heartbeat
	// needs a Responses terminal. Configure it before invoking WriteError.
	metadata func(*gin.Context) (string, string)
}

// WriteAnthropicStreamingError writes Anthropic JSON before stream start or an
// SSE error event afterward. Model-attribute overrides run first, and an
// already committed event stream promotes started so early heartbeats never
// receive trailing raw JSON.
//
// On started output it records the intended error status when markStream is
// configured, then emits event: error through the terminal SSE barrier so idle
// keepalives stop immediately after delivery. Writers without http.Flusher
// skip emission after observation.
func (h OpenAIErrorOutput) WriteAnthropicStreamingError(c *gin.Context, status int, kind, message string, started bool) {
	status, message = modelAttributeError(c, status, message)
	if !started && !CommittedSSE(c) {
		WriteAnthropicError(c, status, kind, "", message)
		return
	}
	if h.markStream != nil {
		h.markStream(c, kind, message, status)
	}
	payload, err := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
	// Retain a valid Anthropic error payload if marshaling fails so the active
	// event stream still terminates with a parseable protocol error frame.
	if err != nil {
		payload = []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	}
	emitLocalTerminalSSE(c, fmt.Appendf(nil, "event: error\ndata: %s\n\n", payload))
}

// WriteStreamingErrorWithCode writes a client error in the format permitted by
// current output. Callers configure the callbacks and provide a live writer.
// It stops Compact heartbeats before taking over output, and either Compact or
// downstream SSE commitment selects an in-stream error even when streamStarted
// was false.
//
// Started output records either an ordinary stream error or an SLA failure
// according to countTowardsSLA. Responses routes first attempt
// response.failed, and other routes or an unsuccessful Responses write use
// generic SSE error framing through the terminal keepalive barrier. Unstarted
// output uses JSON; a nonempty code is included directly in that envelope.
func (h OpenAIErrorOutput) WriteStreamingErrorWithCode(
	c *gin.Context,
	status int,
	errType string,
	code string,
	message string,
	streamStarted bool,
	countTowardsSLA bool,
) {
	status, message = modelAttributeError(c, status, message)
	// Stop Compact heartbeats before checking downstream SSE commitment so an
	// active Compact worker finishes and cannot interleave with terminal output.
	if (h.stopCompact != nil && h.stopCompact(c)) || CommittedSSE(c) {
		streamStarted = true
	}
	if streamStarted {
		if countTowardsSLA {
			if h.markFailure != nil {
				h.markFailure(c, errType, code, message, status)
			}
		} else if h.markStream != nil {
			h.markStream(c, errType, message, status)
		}
		// Responses clients consume terminal lifecycle events. Emit response.failed
		// first so the already-started response carries a recognizable failure terminal.
		if InboundIsResponses(c) {
			if WriteResponsesFailedSSE(c, errType, code, message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		// Use generic SSE error framing for other routes or when the Responses
		// terminal could not be written, leaving the committed HTTP status in place.
		writeOpenAIGenericErrorSSE(c, errType, code, message)
		return
	}

	if code == "" {
		h.WriteError(c, status, errType, message)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{
		"type": errType, "code": code, "message": message,
	}})
}

// writeOpenAIGenericErrorSSE encodes an OpenAI-shaped SSE error frame and sends
// it through the terminal local SSE helper. Empty codes are omitted from the
// error object, marshaling failure falls back to a fixed upstream_error frame,
// and false reports that the writer lacks http.Flusher.
func writeOpenAIGenericErrorSSE(c *gin.Context, errType, code, message string) bool {
	errorObject := gin.H{"type": errType, "message": message}
	if code != "" {
		errorObject["code"] = code
	}
	payload, err := json.Marshal(gin.H{"error": errorObject})
	// Retain a valid generic error event if envelope serialization fails, so
	// the existing stream can still receive a failure payload through this writer.
	if err != nil {
		payload = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	}
	return emitLocalTerminalSSE(c, fmt.Appendf(nil, "event: error\ndata: %s\n\n", payload))
}

// writeCommittedClientSSEError emits a terminal SSE error for the inbound
// client protocol after an event stream is already committed. Anthropic
// Messages clients receive an Anthropic error event, while OpenAI clients use
// WriteStreamingErrorWithCode to select Responses or generic SSE framing.
func writeCommittedClientSSEError(c *gin.Context, status int, errType, code, message string) {
	if c != nil && c.Request != nil {
		if source, _ := requeststate.ClientProtocolFromContext(c.Request.Context()); source == protocol.ProtocolAnthropicMessages {
			DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, errType, message, true)
			return
		}
	}
	DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, errType, code, message, true, false)
}

// DefaultOpenAIErrorOutput binds heartbeat stopping, request metadata and Ops
// error observations.
func DefaultOpenAIErrorOutput() OpenAIErrorOutput {
	return OpenAIErrorOutput{stopCompact: StopOpenAICompactSSEKeepaliveCommitted, markStream: MarkOpsStreamError, markFailure: MarkOpsStreamFailure, metadata: func(c *gin.Context) (string, string) { return ErrorRequestID(c), ErrorRequestModel(c) }}
}

// WriteError applies any model-attribute presentation override and writes the
// OpenAI error through the configured heartbeat-aware helper. Callers supply a
// live context and configured stop, observation and metadata callbacks for
// Compact terminal output.
func (h OpenAIErrorOutput) WriteError(c *gin.Context, status int, kind, message string) {
	status, message = modelAttributeError(c, status, message)
	writeOpenAIRequestError(c, status, kind, message, h.stopCompact, h.markStream, h.metadata)
}

// StreamError writes a streaming-aware error without a separate classification
// code. It uses ordinary stream observation rather than SLA failure counting;
// callers configure the output callbacks and provide the current started
// state.
func (h OpenAIErrorOutput) StreamError(c *gin.Context, status int, kind, message string, started bool) {
	h.WriteStreamingErrorWithCode(c, status, kind, "", message, started, false)
}

// EnsureFallback attempts an error response when no upstream failure object is
// available. Its result reports whether EnsureResponse invoked output;
// already-communicated content can suppress the fallback, and true does not
// confirm successful network delivery.
func (h OpenAIErrorOutput) EnsureFallback(c *gin.Context, started bool) bool {
	return h.EnsureResponse(c, started, nil)
}

// WriteFailoverExhausted presents the final classified failure through this
// output's stream-aware writer. The supplied rules and hooks govern final
// observation and presentation; callers provide the started state after the
// provider loop ends.
func (h OpenAIErrorOutput) WriteFailoverExhausted(c *gin.Context, failure *OpenAIFailoverError, started bool, rules ErrorRuleMatcher, hooks FailoverErrorHooks) {
	WriteOpenAIFailoverExhausted(c, failure, started, rules, hooks, h.StreamError)
}
