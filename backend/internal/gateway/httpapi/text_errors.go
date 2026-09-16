// 文本错误的 HTTP 状态和 envelope 唯一由本适配器实现。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func WriteAnthropicStreamError(c *gin.Context, status int, errType, code, message string, streamStarted bool, observe func(*gin.Context, string, string, int)) {
	if streamStarted {
		// 响应状态码已固化为 200（ping/部分数据已 flush），错误只能就地以 SSE 帧回传。
		// 标记本次流内错误，供 ops_error_logger 补记——否则该中间件按 status>=400 采集，
		// 这类挂在 200 流上的失败（如并发限流回退）不会进错误看板。
		if observe != nil {
			observe(c, errType, message, status)
		}

		// /v1/responses 的严格 SDK（Codex CLI）要求终止事件必须属于
		// response.completed/failed/incomplete/cancelled 集合。
		// Anthropic-backed Responses 路径同样会因为通用 error 帧被拒。
		if InboundIsResponses(c) {
			if WriteResponsesFailedSSE(c, errType, code, message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		// Stream already started, send error as SSE event then close
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			// SSE 错误事件固定 schema，使用 Quote 直拼可避免额外 Marshal 分配。
			errorEvent := `data: {"type":"error","error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n"
			if code != "" {
				errorObject := gin.H{"type": errType, "code": code, "message": message}
				payload, err := json.Marshal(gin.H{"type": "error", "error": errorObject})
				if err == nil {
					errorEvent = "data: " + string(payload) + "\n\n"
				}
			}
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	// Normal case: return JSON response with proper status code
	if code == "" {
		WriteAnthropicError(c, status, errType, "", message)
	} else {
		WriteAnthropicError(c, status, errType, code, message)
	}
}

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

// ExtractQuotaResetSeconds 从 quota 错误的 metadata 中提取 window_resets_at 并计算
// 距重置剩余秒数。fallback 路径必须返回 ≥1 秒，避免客户端立即重试无限循环。
func ExtractQuotaResetSeconds(err error) int {
	const fallback = 60
	appErr := apperror.FromError(err)
	if appErr == nil {
		return fallback
	}
	raw, ok := appErr.Metadata["window_resets_at"]
	if !ok || raw == "" {
		return fallback
	}
	resetAt, parseErr := time.Parse(time.RFC3339, raw)
	if parseErr != nil {
		logging.L().With(
			zap.String("component", "handler.gateway.billing"),
			zap.String("raw", raw),
			zap.Error(parseErr),
		).Warn("quota.invalid_window_resets_at_format")
		return fallback
	}
	secs := time.Until(resetAt).Seconds()
	if secs <= 0 {
		// reset 时间已过：cache 与 DB 应该正在自愈，返回 fallback 让客户端按常规节奏退避，
		// 避免返回 1 秒导致客户端立即重试仍触发限额的退避循环。
		return fallback
	}
	return int(math.Ceil(secs))
}

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
	// 用户/分组 RPM 超限统一映射为 HTTP 429；保留与其它 rate_limit 一致的错误码便于客户端分类。
	// 返回 Retry-After 秒数（当前分钟剩余秒数），让 SDK 自动退避。
	if errors.Is(err, scheduler.ErrGroupRPMExceeded) || errors.Is(err, scheduler.ErrUserRPMExceeded) {
		msg := apperror.Message(err)
		retrySeconds := 60 - int(time.Now().Unix()%60)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, retrySeconds
	}
	if errors.Is(err, billing.ErrUserPlatformDailyQuotaExhausted) ||
		errors.Is(err, billing.ErrUserPlatformWeeklyQuotaExhausted) ||
		errors.Is(err, billing.ErrUserPlatformMonthlyQuotaExhausted) {
		// 与 RPM 超限一致映射 429 + Retry-After，让 SDK 自动退避（而非 403 直接失败）。
		// 错误码用 rate_limit_exceeded 与 OpenAI 兼容客户端一致；细分类型由 ErrCode + window_resets_at metadata 区分。
		msg := apperror.Message(err)
		return http.StatusTooManyRequests, "rate_limit_exceeded", msg, ExtractQuotaResetSeconds(err)
	}
	msg := apperror.Message(err)
	if msg == "" {
		logging.L().With(
			zap.String("component", "handler.gateway.billing"),
			zap.Error(err),
		).Warn("gateway.billing_error_missing_message")
		msg = "Billing error"
	}
	return http.StatusForbidden, "billing_error", msg, 0
}

// ErrorRequestID、ErrorRequestModel 读取原观测字段，不推断或恢复业务状态。
func ErrorRequestID(c *gin.Context) string {
	if c != nil && c.Request != nil {
		value, _ := c.Request.Context().Value(telemetry.RequestID).(string)
		return value
	}
	return ""
}
func ErrorRequestModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetString("ops_model"))
}
