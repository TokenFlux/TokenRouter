// 文本入口错误输出由 HTTP Adapter 拥有，compact 心跳停止后才接管响应写入。
package httpapi

import (
	"errors"
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
	h.errorOutput().WriteError(c, status, kind, message)
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

// errorOutput 保留测试/专用 HTTP backend 的观测端口，算法与生产默认输出器相同。
func (h *OpenAITextHandler) errorOutput() OpenAIErrorOutput {
	return OpenAIErrorOutput{stopCompact: h.backend.StopCompact, markStream: h.backend.MarkStream, markFailure: h.backend.MarkStreamFailure, metadata: h.backend.ErrorMetadata}
}
func (h *OpenAITextHandler) WriteStreamingErrorWithCode(c *gin.Context, status int, kind, code, message string, started, sla bool) {
	h.errorOutput().WriteStreamingErrorWithCode(c, status, kind, code, message, started, sla)
}
func (h *OpenAITextHandler) anthropicStreamingAwareError(c *gin.Context, status int, kind, message string, started bool) {
	(OpenAIErrorOutput{}).WriteAnthropicStreamingError(c, status, kind, message, started)
}
