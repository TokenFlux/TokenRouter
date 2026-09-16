package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// ResponsesWSFailure 是升级后错误展示的明确投影，不含内部凭据或原始上游报文。
type ResponsesWSFailure struct {
	Reason            string
	StatusCode        int
	AccountAuth       bool
	CredentialMessage string
}

func CloseResponsesWSFailure(c *gin.Context, conn *coderws.Conn, failoverErr *ResponsesWSFailure, mark func(*gin.Context, string, string, string, int)) {
	intendedStatus := http.StatusBadGateway
	errorType := "upstream_error"
	errorCode := "upstream_ws_failover_exhausted"
	message := "upstream websocket proxy failed"
	closeStatus := coderws.StatusInternalError

	if failoverErr != nil {
		if reason := strings.TrimSpace(failoverErr.Reason); reason != "" {
			errorCode = reason
		}
		if failoverErr.AccountAuth {
			intendedStatus = http.StatusServiceUnavailable
			errorType = "api_error"
			message = failoverErr.CredentialMessage
			closeStatus = coderws.StatusTryAgainLater
		} else {
			switch failoverErr.StatusCode {
			case http.StatusTooManyRequests:
				intendedStatus = http.StatusTooManyRequests
				errorType = "rate_limit_error"
				message = "upstream rate limit exceeded, please retry later"
				closeStatus = coderws.StatusTryAgainLater
			case 529, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
				intendedStatus = failoverErr.StatusCode
				message = "upstream service temporarily unavailable"
				closeStatus = coderws.StatusTryAgainLater
			case http.StatusUnauthorized, http.StatusForbidden:
				intendedStatus = failoverErr.StatusCode
				errorType = "authentication_error"
				message = "upstream websocket authentication failed"
				closeStatus = coderws.StatusPolicyViolation
			}
		}
	}

	mark(c, errorType, errorCode, message, intendedStatus)
	CloseResponsesWS(conn, closeStatus, message)
}
func WriteResponsesWSModeration(ctx context.Context, conn *coderws.Conn, decision *moderation.Decision) {
	if conn == nil || decision == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := strings.TrimSpace(decision.Message)
	if message == "" {
		message = "content moderation blocked this request"
	}
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_content_moderation_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    "content_policy_violation",
			"message": message,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_content_moderation_blocked","type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"content moderation blocked this request"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
}

func WriteResponsesWSCyberBlocked(ctx context.Context, conn *coderws.Conn, message string) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_cyber_session_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "permission_error",
			"code":    "session_blocked_by_cyber_policy",
			"message": message,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_cyber_session_blocked","type":"error","error":{"type":"permission_error","code":"session_blocked_by_cyber_policy","message":"This session is blocked by cyber-security policy, please start a new session"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
}
func WriteResponsesWSIsolation(ctx context.Context, conn *coderws.Conn, err error) bool {
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
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		message = session.SessionIsolationConflictMessage
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
		payload = []byte(`{"event_id":"evt_session_isolation_blocked","type":"error","error":{"type":"permission_error","code":"permission_error","message":"` + strings.ReplaceAll(session.SessionIsolationConflictMessage, `"`, `\"`) + `"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
	return true
}

func ResponsesWSIsolationCloseReason(err error) string {
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		return session.SessionIsolationConflictMessage
	}
	return "session isolation check failed"
}

// ResponsesWSEndedByClient 保留正常关闭和客户端取消的原账号归因边界。
func ResponsesWSEndedByClient(err error, info gatewayws.EntryClose) bool {
	if err == nil {
		return true
	}
	if info.Present && info.Status == int(coderws.StatusNormalClosure) {
		return true
	}
	if coderws.CloseStatus(err) == coderws.StatusNormalClosure {
		return true
	}
	return errors.Is(err, context.Canceled)
}
