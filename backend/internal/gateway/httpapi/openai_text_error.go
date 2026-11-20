package httpapi

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// openai_text_error.go writes text-admission failures using the active
// protocol and output state.
// Text admission can fail before JSON output or after a wait/Compact stream
// has already committed a format. These handler bindings use the configured
// heartbeat-aware output for OpenAI routes and Anthropic-specific output for
// Messages, while panic recovery logs the captured state used to decide its
// final response.
//
// When a Compact or downstream idle heartbeat has committed an event stream,
// request-error writing stops Compact output, records the intended failure, and
// emits response.failed on Compact or Responses routes or event: error on other
// streams. JSON remains the fallback when a terminal frame cannot be written.
// Messages panic recovery writes a JSON 500 only before its shared started flag
// or downstream SSE commitment indicates streaming.

// openAITextMaxBytesError extracts a body-limit error through the supplied
// error chain. Callers use the boolean to select HTTP 413 and retain the
// reported limit; nonmatching errors continue through ordinary request-read
// failure handling.
func openAITextMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var limit *http.MaxBytesError
	ok := errors.As(err, &limit)
	return limit, ok
}

// errorResponse delegates heartbeat-aware OpenAI rejection using this
// handler's backend callbacks.
func (h *OpenAITextHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	h.errorOutput().WriteError(c, status, kind, message)
}

// writeOpenAIRequestError stops Compact heartbeat output before writing a
// chosen OpenAI rejection. Callers supply stop, observation and metadata
// callbacks. Either a committed Compact heartbeat or an already committed
// downstream event stream records the intended error and emits response.failed
// on Compact/Responses routes or generic event: error on other streams; an
// unsuccessful terminal write falls through to JSON on the current writer.
func writeOpenAIRequestError(c *gin.Context, status int, kind, message string, stop func(*gin.Context) bool, mark func(*gin.Context, string, string, int), metadata func(*gin.Context) (string, string)) {
	// Stop waits for an active Compact heartbeat write before checking committed
	// SSE state so no background beat can race terminal framing.
	compactCommitted := stop != nil && stop(c)
	sseCommitted := CommittedSSE(c)
	if compactCommitted || sseCommitted {
		if mark != nil {
			mark(c, kind, message, status)
		}
		if compactCommitted || InboundIsResponses(c) {
			id, model := "", ""
			if metadata != nil {
				id, model = metadata(c)
			}
			if WriteResponsesFailedSSE(c, kind, "", message, id, model) {
				return
			}
		}
		if sseCommitted && writeOpenAIGenericErrorSSE(c, kind, "", message) {
			return
		}
	}
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}

// handleStreamingAwareError writes the selected rejection using ordinary
// stream observation. It passes no separate code and disables SLA counting,
// while the output helper combines started state with any committed Compact
// heartbeat.
func (h *OpenAITextHandler) handleStreamingAwareError(c *gin.Context, status int, kind, message string, started bool) {
	h.WriteStreamingErrorWithCode(c, status, kind, "", message, started, false)
}

// anthropicErrorResponse writes this handler's rejection using the Anthropic
// JSON envelope.
func (h *OpenAITextHandler) anthropicErrorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}

// handleOpenAISessionIsolationError writes a stream-aware rejection when
// isolation failed and reports true. Nil errors return false; session
// conflicts become permission 403, while other failures become unavailable 503
// with fixed client text.
func (h *OpenAITextHandler) handleOpenAISessionIsolationError(c *gin.Context, err error, started bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		h.handleStreamingAwareError(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage, started)
	} else {
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", started)
	}
	return true
}

// handleAnthropicSessionIsolationError writes the Messages-format rejection
// when isolation failed and reports true. Nil errors return false; session
// conflict selects permission 403, and other failures use unavailable 503
// under the current started state.
func (h *OpenAITextHandler) handleAnthropicSessionIsolationError(c *gin.Context, err error, started bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		h.anthropicStreamingAwareError(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage, started)
	} else {
		h.anthropicStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", started)
	}
	return true
}

// recoverResponsesPanic recovers a panic when deferred directly by a text
// handler. It uses the optional started pointer for heartbeat-aware fallback,
// then logs the panic, stack and whether output was invoked; normal return
// leaves the writer untouched.
func (h *OpenAITextHandler) recoverResponsesPanic(c *gin.Context, started *bool) {
	value := recover()
	if value == nil {
		return
	}
	// Panic recovery can occur after wait output. Let the backend inspect its
	// actual heartbeat/commit state before recording whether fallback output was invoked.
	wrote := h.backend.EnsureFallback(c, started != nil && *started)
	RequestLogger(c, "handler.openai_gateway.responses").Error("openai.responses_panic_recovered", zap.Bool("fallback_error_response_written", wrote), zap.Any("panic", value), zap.ByteString("stack", debug.Stack()))
}

// recoverAnthropicMessagesPanic recovers a directly deferred Messages handler
// panic and logs started state and stack. Before output starts it writes JSON
// 500; after started output or an early SSE heartbeat it logs the failure and
// returns without appending raw JSON to the stream.
func (h *OpenAITextHandler) recoverAnthropicMessagesPanic(c *gin.Context, started *bool) {
	value := recover()
	if value == nil {
		return
	}
	stream := (started != nil && *started) || CommittedSSE(c)
	RequestLogger(c, "handler.openai_gateway.messages").Error("openai.messages_panic_recovered", zap.Bool("stream_started", stream), zap.Any("panic", value), zap.ByteString("stack", debug.Stack()))
	// Messages recovery writes JSON only before stream framing starts; committed
	// SSE output leaves the stream to close after logging rather than appending JSON.
	if !stream {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "Internal server error")
	}
}

// errorOutput binds this backend's Compact stopper, stream observations and
// metadata to shared error output.
func (h *OpenAITextHandler) errorOutput() OpenAIErrorOutput {
	return OpenAIErrorOutput{stopCompact: h.backend.StopCompact, markStream: h.backend.MarkStream, markFailure: h.backend.MarkStreamFailure, metadata: h.backend.ErrorMetadata}
}

// WriteStreamingErrorWithCode delegates classified error output using the
// handler's configured backend callbacks. started and sla independently
// describe the current output and observation choice; the shared output helper
// handles Compact commitment and protocol terminal framing.
func (h *OpenAITextHandler) WriteStreamingErrorWithCode(c *gin.Context, status int, kind, code, message string, started, sla bool) {
	h.errorOutput().WriteStreamingErrorWithCode(c, status, kind, code, message, started, sla)
}

// anthropicStreamingAwareError delegates Anthropic error presentation under
// the supplied started state with this handler's stream observation callback.
// The shared compatibility writer appends an SSE error event after stream or
// heartbeat commitment and records its intended status through MarkStream.
func (h *OpenAITextHandler) anthropicStreamingAwareError(c *gin.Context, status int, kind, message string, started bool) {
	h.errorOutput().WriteAnthropicStreamingError(c, status, kind, message, started)
}
