// 各 OpenAI 执行入口错误输出迁入 HTTP Adapter；compact 与提交状态由原拥有者提供。
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// WriteForwardAnthropicError 保留该转发入口既有的错误信封和提交语义。
func WriteForwardAnthropicError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardAnthropicErrorBody 保留该转发入口既有的错误信封和提交语义。
func WriteForwardAnthropicErrorBody(c *gin.Context, statusCode int, body []byte) {
	errorObject := gjson.GetBytes(body, "error")
	if !errorObject.Exists() || !gjson.Valid(errorObject.Raw) {
		c.Data(statusCode, "application/json; charset=utf-8", body)
		return
	}
	wrapped := []byte(`{"type":"error","error":` + errorObject.Raw + `}`)
	c.Data(statusCode, "application/json; charset=utf-8", wrapped)
}

// BuildForwardAnthropicStreamError 保留该转发入口既有的错误信封和提交语义。
func BuildForwardAnthropicStreamError(errType, message string) string {
	payload, err := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
	if err != nil {
		return `event: error` + "\n" + `data: {"type":"error","error":{"type":"invalid_request_error","message":"Request blocked by upstream cyber-security policy"}}` + "\n\n"
	}
	return "event: error\ndata: " + string(payload) + "\n\n"
}

// WriteForwardChatError 保留该转发入口既有的错误信封和提交语义。
func WriteForwardChatError(c *gin.Context, statusCode int, errType, message string, mark func(*gin.Context)) {
	mark(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardChatErrorBody 保留该转发入口既有的错误信封和提交语义。
func WriteForwardChatErrorBody(c *gin.Context, statusCode int, body []byte, mark func(*gin.Context)) {
	mark(c)
	c.Data(statusCode, "application/json; charset=utf-8", body)
}

// BuildForwardChatStreamError 保留该转发入口既有的错误信封和提交语义。
func BuildForwardChatStreamError(code, message string) string {
	payload, err := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
	if err != nil {
		return `data: {"error":{"type":"invalid_request_error","code":"cyber_policy","message":"Request blocked by upstream cyber-security policy"}}` + "\n\n"
	}
	return "data: " + string(payload) + "\n\n"
}

// WriteForwardResponsesFallbackError 保留该转发入口既有的错误信封和提交语义。
func WriteForwardResponsesFallbackError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// WriteForwardPassthroughErrorHeaders 保留该转发入口既有的错误信封和提交语义。
func WriteForwardPassthroughErrorHeaders(dst, src http.Header) {
	if dst == nil {
		return
	}
	dst.Set("Content-Type", "application/json; charset=utf-8")
	dst.Set("Cache-Control", "no-store")
	dst.Del("Retry-After")
	if src == nil {
		return
	}
	rawRetryAfter := strings.TrimSpace(src.Get("Retry-After"))
	if ValidForwardPassthroughRetryAfter(rawRetryAfter, time.Now()) {
		dst.Set("Retry-After", rawRetryAfter)
	}
}

// ValidForwardPassthroughRetryAfter 保留该转发入口既有的错误信封和提交语义。
func ValidForwardPassthroughRetryAfter(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	delaySeconds := true
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			delaySeconds = false
			break
		}
	}
	if delaySeconds {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		return err == nil && seconds > 0
	}
	parsed, err := http.ParseTime(raw)
	return err == nil && parsed.After(now)
}

// WriteSanitizedForwardPassthroughError 保留该转发入口既有的错误信封和提交语义。
func WriteSanitizedForwardPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header, compact func(*gin.Context, int, []byte) bool) {
	downstreamStatus := upstreamStatus
	message := "Upstream request failed"
	switch upstreamStatus {
	case http.StatusUnauthorized:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream authentication failed"
	case http.StatusForbidden:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream access denied"
	default:
		if upstreamStatus >= http.StatusInternalServerError {
			message = "Upstream service temporarily unavailable"
		}
	}
	WriteForwardPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message, compact)
}

// WriteForwardPassthroughErrorEnvelope 保留该转发入口既有的错误信封和提交语义。
func WriteForwardPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string, compact func(*gin.Context, int, []byte) bool) {
	if c == nil {
		return
	}
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	if compact(c, downstreamStatus, body) {
		return
	}
	WriteForwardPassthroughErrorHeaders(c.Writer.Header(), upstreamHeaders)
	c.Data(downstreamStatus, "application/json; charset=utf-8", body)
}

// WriteForwardFastPolicyBlocked 保留 compact 心跳已提交时的终态 SSE，否则返回原 403 信封。
func WriteForwardFastPolicyBlocked(c *gin.Context, message string, stop func(*gin.Context) bool, failure func(*gin.Context, int, string, string)) {
	if stop(c) {
		failure(c, http.StatusForbidden, "permission_error", message)
		return
	}
	c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": message}})
}
