// 文本入口错误输出由 HTTP Adapter 拥有，compact 心跳停止后才接管响应写入。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func openAITextMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var limit *http.MaxBytesError
	ok := errors.As(err, &limit)
	return limit, ok
}
func (h *OpenAITextHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	writeOpenAIRequestError(c, status, kind, message, h.backend.StopCompact, h.backend.MarkStream, h.backend.ErrorMetadata)
}

// writeOpenAIRequestError 保留 compact 已提交状态下的终态事件与普通 JSON 错误。
func writeOpenAIRequestError(c *gin.Context, status int, kind, message string, stop func(*gin.Context) bool, mark func(*gin.Context, string, string, int), metadata func(*gin.Context) (string, string)) {
	if stop(c) {
		mark(c, kind, message, status)
		id, model := metadata(c)
		if WriteResponsesFailedSSE(c, kind, "", message, id, model) {
			return
		}
	}
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}
func (h *OpenAITextHandler) handleStreamingAwareError(c *gin.Context, status int, kind, message string, started bool) {
	h.WriteStreamingErrorWithCode(c, status, kind, "", message, started, false)
}
func (h *OpenAITextHandler) anthropicErrorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}
func (h *OpenAITextHandler) anthropicStreamingAwareError(c *gin.Context, status int, kind, message string, started bool) {
	if !started {
		h.anthropicErrorResponse(c, status, kind, message)
		return
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
		// 保留此兼容入口的写失败处置；不得附带通用 Messages 的额外观测或终态改写。
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		flusher.Flush()
	}
}
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
func (h *OpenAITextHandler) recoverResponsesPanic(c *gin.Context, started *bool) {
	value := recover()
	if value == nil {
		return
	}
	wrote := h.backend.EnsureFallback(c, started != nil && *started)
	RequestLogger(c, "handler.openai_gateway.responses").Error("openai.responses_panic_recovered", zap.Bool("fallback_error_response_written", wrote), zap.Any("panic", value), zap.ByteString("stack", debug.Stack()))
}
func (h *OpenAITextHandler) recoverAnthropicMessagesPanic(c *gin.Context, started *bool) {
	value := recover()
	if value == nil {
		return
	}
	stream := started != nil && *started
	RequestLogger(c, "handler.openai_gateway.messages").Error("openai.messages_panic_recovered", zap.Bool("stream_started", stream), zap.Any("panic", value), zap.ByteString("stack", debug.Stack()))
	if !stream {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "Internal server error")
	}
}

func (h *OpenAITextHandler) WriteStreamingErrorWithCode(
	c *gin.Context,
	status int,
	errType string,
	code string,
	message string,
	streamStarted bool,
	countTowardsSLA bool,
) {
	// body-signal compact 心跳可能已把响应头提交为 200：先停心跳（建立
	// happens-before，接管 ResponseWriter），并升级为流内错误处理。
	if h.backend.StopCompact(c) {
		streamStarted = true
	}
	if streamStarted {
		if countTowardsSLA {
			h.backend.MarkStreamFailure(c, errType, code, message, status)
		} else {
			h.backend.MarkStream(c, errType, message, status)
		}
		// /v1/responses 的严格 SDK（Codex CLI）要求终止事件必须属于
		// response.completed/failed/incomplete/cancelled 集合。
		// 通用 `event: error` 帧不被识别为终止事件，会导致
		// "stream closed before response.completed"。
		if InboundIsResponses(c) {
			if WriteResponsesFailedSSE(c, errType, code, message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		// Stream already started, send error as SSE event then close
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errorObject := gin.H{"type": errType, "message": message}
			if code != "" {
				errorObject["code"] = code
			}
			payload, err := json.Marshal(gin.H{"error": errorObject})
			if err != nil {
				payload = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
			}
			errorEvent := "event: error\ndata: " + string(payload) + "\n\n"
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	// Normal case: return JSON response with proper status code
	if code == "" {
		h.errorResponse(c, status, errType, message)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{
		"type": errType, "code": code, "message": message,
	}})
}

// WriteError 供已迁入口和暂存兼容调用共用相同 JSON/compact 输出。
func (h *OpenAITextHandler) WriteError(c *gin.Context, status int, kind, message string) {
	h.errorResponse(c, status, kind, message)
}

// WriteAnthropicError 保留 Anthropic 兼容 JSON。
func (h *OpenAITextHandler) WriteAnthropicError(c *gin.Context, status int, kind, message string) {
	h.anthropicErrorResponse(c, status, kind, message)
}

// WriteAnthropicStreamingError 保留该入口独立的 SSE 外形与写失败语义。
func (h *OpenAITextHandler) WriteAnthropicStreamingError(c *gin.Context, status int, kind, message string, started bool) {
	h.anthropicStreamingAwareError(c, status, kind, message, started)
}
