package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

func isSessionIsolationConflict(err error) bool {
	return errors.Is(err, service.ErrSessionIsolationConflict)
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
