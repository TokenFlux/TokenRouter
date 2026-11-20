package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// text_errors.go presents gateway streaming errors and translates billing
// failures into HTTP status, message and retry details.
// Gateway errors may occur after a wait heartbeat has committed a streaming
// response. Final output then uses the inbound protocol's error event, while
// funding and rate failures retain a safe status, code and message and can
// supply the remaining RPM-window seconds.

// WriteAnthropicStreamError writes a protocol error using streamStarted and
// live SSE commitment to choose SSE or JSON. Started Responses paths receive a
// terminal response.failed event; other started or heartbeat-committed streams
// emit Anthropic error data through the terminal SSE barrier when Flush is
// available. The optional observer records intended error status before output,
// and callers end processing after the helper returns.
func WriteAnthropicStreamError(c *gin.Context, status int, errType, code, message string, streamStarted bool, observe func(*gin.Context, string, string, int)) {
	if streamStarted || CommittedSSE(c) {
		// A flushed keepalive or data frame commits the status, so subsequent errors use SSE.
		// Record stream errors so Ops can account for failures whose HTTP status is below 400.
		if observe != nil {
			observe(c, errType, message, status)
		}

		// Responses clients recognize stream completion through terminal response
		// events, so select that envelope even when this caller uses Anthropic output.
		if InboundIsResponses(c) {
			if WriteResponsesFailedSSE(c, errType, code, message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		// Quote variable strings when constructing the fixed SSE error schema.
		errorEvent := `data: {"type":"error","error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n"
		if code != "" {
			errorObject := gin.H{"type": errType, "code": code, "message": message}
			payload, err := json.Marshal(gin.H{"type": "error", "error": errorObject})
			// Keep the quoted fixed error event if structured encoding fails, so
			// variable text still has escaped JSON in the started SSE response.
			if err == nil {
				errorEvent = "data: " + string(payload) + "\n\n"
			}
		}
		// Anthropic error data also requires http.Flusher inside emitLocalTerminalSSE,
		// so a Responses failure rejected for missing Flush emits nothing here.
		emitLocalTerminalSSE(c, []byte(errorEvent))
		return
	}

	// Before streaming starts, return JSON with the requested error status.
	if code == "" {
		WriteAnthropicError(c, status, errType, "", message)
	} else {
		WriteAnthropicError(c, status, errType, code, message)
	}
}

// WriteAnthropicError writes an Anthropic JSON error with the supplied status.
// A nonempty code adds its machine-readable field; callers choose the status
// and retain responsibility for any separate Ops or stream observation.
func WriteAnthropicError(c *gin.Context, status int, errType, code, message string) {
	errorObject := gin.H{"type": errType, "message": message}
	if code != "" {
		errorObject["code"] = code
	}
	c.JSON(status, gin.H{
		"type":  "error",
		"error": errorObject,
	})
}

// BillingErrorDetails maps funding and rate failures to client status, code,
// message and retry seconds. RPM limits count seconds until the next
// Unix-minute boundary; rolling Key limits omit Retry-After. An unavailable
// service with no message uses fixed unavailable text. Remaining failures use
// 403, logging an absent application message before returning generic billing
// text.
func BillingErrorDetails(err error) (status int, code, message string, retryAfter int) {
	if errors.Is(err, billing.ErrBillingServiceUnavailable) {
		msg := apperror.Message(err)
		if msg == "" {
			msg = "Billing service temporarily unavailable. Please retry later."
		}
		return http.StatusServiceUnavailable, "billing_service_error", msg, 0
	}
	if errors.Is(err, billing.ErrAPIKeyRateLimit5hExceeded) {
		msg := apperror.Message(err)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, 0
	}
	if errors.Is(err, billing.ErrAPIKeyRateLimit1dExceeded) {
		msg := apperror.Message(err)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, 0
	}
	if errors.Is(err, billing.ErrAPIKeyRateLimit7dExceeded) {
		msg := apperror.Message(err)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, 0
	}
	// User and group RPM limits return 429 with the shared rate_limit_exceeded code.
	// Retry-After is the number of seconds remaining in the current minute.
	if errors.Is(err, scheduler.ErrGroupRPMExceeded) || errors.Is(err, scheduler.ErrUserRPMExceeded) {
		msg := apperror.Message(err)
		retrySeconds := 60 - int(time.Now().Unix()%60)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, retrySeconds
	}

	msg := apperror.Message(err)
	// A failure with no safe application message is logged for diagnosis and
	// receives generic billing text rather than exposing its raw error.
	if msg == "" {
		logging.L().With(
			zap.String("component", "handler.gateway.billing"),
			zap.Error(err),
		).Warn("gateway.billing_error_missing_message")
		msg = "Billing error"
	}
	return http.StatusForbidden, "billing_error", msg, 0
}

// ErrorRequestID returns the telemetry request correlation value as a string. A
// missing Gin request or a context value of another type yields empty, allowing
// protocol error writers to generate their own response identifier.
func ErrorRequestID(c *gin.Context) string {
	if c != nil && c.Request != nil {
		value, _ := c.Request.Context().Value(telemetry.RequestID).(string)
		return value
	}
	return ""
}

// ErrorRequestModel returns the trimmed model retained for Ops error
// presentation. Nil context or an absent string yields empty, which lets
// terminal Responses failures omit the model field when request metadata is
// unavailable.
func ErrorRequestModel(c *gin.Context) string {
	return strings.TrimSpace(GetOpsModel(c))
}
