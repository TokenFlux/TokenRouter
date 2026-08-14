package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func isSessionIsolationConflict(err error) bool {
	return errors.Is(err, service.ErrSessionIsolationConflict)
}

// metadataSessionIsolationID 只返回客户端显式 metadata.user_id 中解析出的会话 ID。
func metadataSessionIsolationID(raw string) string {
	parsed := service.ParseMetadataUserID(raw)
	if parsed == nil {
		return ""
	}
	return strings.TrimSpace(parsed.SessionID)
}

func (h *GatewayHandler) ensureGatewaySessionIsolation(ctx context.Context, apiKey *service.APIKey, userID int64, source, sessionHash string) error {
	if h == nil || h.gatewayService == nil {
		return nil
	}
	return h.gatewayService.EnsureSessionIsolation(ctx, apiKey, userID, source, sessionHash)
}

func (h *OpenAIGatewayHandler) ensureOpenAISessionIsolation(ctx context.Context, apiKey *service.APIKey, userID int64, source, sessionHash string) error {
	if h == nil || h.gatewayService == nil {
		return nil
	}
	return h.gatewayService.EnsureSessionIsolation(ctx, apiKey, userID, source, sessionHash)
}

func (h *GatewayHandler) handleSessionIsolationError(c *gin.Context, err error, streamStarted bool) bool {
	if err == nil {
		return false
	}
	if isSessionIsolationConflict(err) {
		h.handleStreamingAwareError(c, http.StatusForbidden, "permission_error", service.SessionIsolationConflictMessage, streamStarted)
		return true
	}
	h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
	return true
}

func (h *OpenAIGatewayHandler) handleOpenAISessionIsolationError(c *gin.Context, err error, streamStarted bool) bool {
	if err == nil {
		return false
	}
	if isSessionIsolationConflict(err) {
		h.handleStreamingAwareError(c, http.StatusForbidden, "permission_error", service.SessionIsolationConflictMessage, streamStarted)
		return true
	}
	h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
	return true
}

func (h *OpenAIGatewayHandler) handleAnthropicSessionIsolationError(c *gin.Context, err error, streamStarted bool) bool {
	if err == nil {
		return false
	}
	if isSessionIsolationConflict(err) {
		h.anthropicStreamingAwareError(c, http.StatusForbidden, "permission_error", service.SessionIsolationConflictMessage, streamStarted)
		return true
	}
	h.anthropicStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
	return true
}

func handleGeminiSessionIsolationError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if isSessionIsolationConflict(err) {
		googleError(c, http.StatusForbidden, service.SessionIsolationConflictMessage)
		return true
	}
	googleError(c, http.StatusServiceUnavailable, "Service temporarily unavailable")
	return true
}

func writeSessionIsolationWSError(ctx context.Context, conn *coderws.Conn, err error) bool {
	if err == nil {
		return false
	}
	if conn == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := "Service temporarily unavailable"
	code := "service_unavailable"
	if isSessionIsolationConflict(err) {
		message = service.SessionIsolationConflictMessage
		code = "permission_error"
	} else {
		closeMessage := message
		payload, marshalErr := json.Marshal(gin.H{
			"event_id": "evt_session_isolation_cache_failed",
			"type":     "error",
			"error": gin.H{
				"type":    "api_error",
				"code":    code,
				"message": closeMessage,
			},
		})
		if marshalErr == nil {
			writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_ = conn.Write(writeCtx, coderws.MessageText, payload)
			return true
		}
	}
	payload, marshalErr := json.Marshal(gin.H{
		"event_id": "evt_session_isolation_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "permission_error",
			"code":    code,
			"message": message,
		},
	})
	if marshalErr != nil {
		payload = []byte(`{"event_id":"evt_session_isolation_blocked","type":"error","error":{"type":"permission_error","code":"permission_error","message":"` + strings.ReplaceAll(service.SessionIsolationConflictMessage, `"`, `\"`) + `"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
	return true
}

func openAIWSSessionIsolationCloseReason(err error) string {
	if isSessionIsolationConflict(err) {
		return service.SessionIsolationConflictMessage
	}
	return "session isolation check failed"
}
