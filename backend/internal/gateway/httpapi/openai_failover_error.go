// 错误展示只读取平台已确认的分类和值，不改变重试、健康或资金决策。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/gin-gonic/gin"
)

// OpenAIFailoverError 固化展示输入；原始响应只用于规则匹配，不返回给公开 JSON。
type OpenAIFailoverError struct {
	Status, ClientStatus, CredentialStatus                                                          int
	ClientMessage, CredentialMessage, TooLargeMessage, SilentMessage, CyberMessage, UpstreamMessage string
	Headers                                                                                         http.Header
	Body                                                                                            []byte
	TooLarge, ContinuationUnsupported, Credential, CapacityShed, SilentRefusal, CyberWarning        bool
}
type ErrorRuleMatcher interface {
	MatchRule(string, int, []byte) *errorpolicy.ErrorPassthroughRule
}
type FailoverErrorHooks struct {
	Upstream       func(*gin.Context, int, string)
	SkipMonitoring func(*gin.Context)
}

// WriteFailoverExhausted 按原顺序解释已分类错误，再匹配展示规则与默认映射。
func WriteOpenAIFailoverExhausted(c *gin.Context, failure *OpenAIFailoverError, started bool, rules ErrorRuleMatcher, hooks FailoverErrorHooks, write func(*gin.Context, int, string, string, bool)) {
	if failure == nil {
		status, kind, message := MapOpenAIUpstreamError(http.StatusBadGateway)
		write(c, status, kind, message, started)
		return
	}
	if failure.TooLarge {
		hooks.Upstream(c, http.StatusRequestEntityTooLarge, failure.TooLargeMessage)
		write(c, http.StatusRequestEntityTooLarge, "invalid_request_error", failure.TooLargeMessage, started)
		return
	}
	if failure.ContinuationUnsupported {
		message := strings.TrimSpace(failure.ClientMessage)
		if message == "" {
			message = "previous_response_id requires an OpenAI API-key account for HTTP requests"
		}
		write(c, http.StatusBadRequest, "invalid_request_error", message, started)
		return
	}
	CopyFailoverRetryAfter(c, failure.Headers)
	if failure.Credential {
		write(c, failure.CredentialStatus, "upstream_error", failure.CredentialMessage, started)
		return
	}
	if failure.CapacityShed && strings.TrimSpace(failure.ClientMessage) != "" {
		status := failure.ClientStatus
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		write(c, status, "server_error", failure.ClientMessage, started)
		return
	}
	if failure.SilentRefusal {
		hooks.Upstream(c, failure.Status, failure.SilentMessage)
		write(c, http.StatusBadGateway, "upstream_error", failure.SilentMessage, started)
		return
	}
	if failure.CyberWarning {
		hooks.Upstream(c, failure.Status, failure.CyberMessage)
		status := failure.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		write(c, status, "invalid_request_error", failure.CyberMessage, started)
		return
	}
	if rules != nil && len(failure.Body) > 0 {
		if rule := rules.MatchRule("openai", failure.Status, failure.Body); rule != nil {
			status := failure.Status
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				status = *rule.ResponseCode
			}
			message := failure.UpstreamMessage
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				message = *rule.CustomMessage
			}
			if rule.SkipMonitoring {
				hooks.SkipMonitoring(c)
			}
			write(c, status, "upstream_error", message, started)
			return
		}
	}
	hooks.Upstream(c, failure.Status, failure.UpstreamMessage)
	status, kind, message := MapOpenAIUpstreamError(failure.Status)
	write(c, status, kind, message, started)
}

func CopyFailoverRetryAfter(c *gin.Context, headers http.Header) {
	if c == nil || headers == nil {
		return
	}
	retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
	if retryAfter == "" || len(retryAfter) > 128 || strings.ContainsAny(retryAfter, "\r\n") || !IsSafeRetryAfter(retryAfter) {
		return
	}
	c.Header("Retry-After", retryAfter)
}
func IsSafeRetryAfter(value string) bool {
	digitsOnly := true
	for _, char := range value {
		if char < '0' || char > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		seconds, err := strconv.ParseUint(value, 10, 32)
		return err == nil && seconds <= uint64((7*24*time.Hour)/time.Second)
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return false
	}
	return !retryAt.After(time.Now().Add(7 * 24 * time.Hour))
}
func MapOpenAIUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "upstream_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// WriteFailoverExhausted 使用当前文本入口的输出状态，裁决仅保留一份。
func (h *OpenAITextHandler) WriteFailoverExhausted(c *gin.Context, failure *OpenAIFailoverError, started bool, rules ErrorRuleMatcher, hooks FailoverErrorHooks) {
	WriteOpenAIFailoverExhausted(c, failure, started, rules, hooks, h.handleStreamingAwareError)
}
