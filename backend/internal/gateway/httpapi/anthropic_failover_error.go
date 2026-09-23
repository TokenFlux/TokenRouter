package httpapi

import (
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// WriteAnthropicFailover 只解释客户端展示，保持静默拒绝、规则和默认映射的原顺序。
func WriteAnthropicFailover(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, platform string, streamStarted bool, rules ErrorRuleMatcher, silent func([]byte) bool, silentMessage string) {
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody
	if silent(responseBody) {
		SetOpsUpstreamError(c, statusCode, silentMessage, "")
		writeAnthropicFailure(c, http.StatusBadGateway, "upstream_error", silentMessage, streamStarted)
		return
	}

	// 先检查透传规则
	if rules != nil && len(responseBody) > 0 {
		if rule := rules.MatchRule(platform, statusCode, responseBody); rule != nil {
			// 确定响应状态码
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			// 确定响应消息
			msg := upstream.ExtractErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				c.Set(OpsSkipPassthroughKey, true)
			}

			writeAnthropicFailure(c, respCode, "upstream_error", msg, streamStarted)
			return
		}
	}

	// 记录原始上游状态码，以便 ops 错误日志捕获真实的上游错误
	upstreamMsg := upstream.ExtractErrorMessage(responseBody)
	SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	// 使用默认的错误映射
	status, errType, errMsg := AnthropicUpstreamError(statusCode)
	writeAnthropicFailure(c, status, errType, errMsg, streamStarted)
}

func AnthropicUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// writeAnthropicFailure 保留已提交输出的终态事件和 Ops 观察。
func writeAnthropicFailure(c *gin.Context, status int, kind, message string, started bool) {
	WriteAnthropicStreamError(c, status, kind, "", message, started, MarkOpsStreamError)
}
