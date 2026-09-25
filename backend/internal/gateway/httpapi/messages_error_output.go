// 本文件拥有三种文本协议的终止错误输出，不决定重试、健康或资金处理。
package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// MessagesErrorOutput 只引用唯一规则实例；不保存请求、缓存或可变输出状态。
type MessagesErrorOutput struct {
	Rules *errorpolicy.ErrorPassthroughService
}

// ConcurrencyError 统一处理并发槽位获取失败。
func (h MessagesErrorOutput) ConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := ConcurrencyErrorResponse(err, slotType)
	h.StreamErrorWithCode(c, status, errType, code, message, streamStarted)
}

// ExhaustedStatus 简化版本，用于没有响应体的情况
func (h MessagesErrorOutput) ExhaustedStatus(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := AnthropicUpstreamError(statusCode)
	SetOpsUpstreamError(c, statusCode, errMsg, "")
	h.StreamError(c, status, errType, errMsg, streamStarted)
}

// StreamError handles errors that may occur after streaming has started
func (h MessagesErrorOutput) StreamError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.StreamErrorWithCode(c, status, errType, "", message, streamStarted)
}

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func (h MessagesErrorOutput) StreamErrorWithCode(c *gin.Context, status int, errType, code, message string, streamStarted bool) {
	WriteAnthropicStreamError(c, status, errType, code, message, streamStarted, MarkOpsStreamError)
}

// EnsureResponse 在 Forward 返回错误但尚未写响应时补写统一错误响应。
// Writer 已被写过时（ping 已 flush）走 streamStarted 分支，
// 让 StreamError 通过 SSE 发协议合规的终止事件，
// 否则下游收到的就是 silent EOF。
func (h MessagesErrorOutput) EnsureResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Written() {
		streamStarted = true
	}
	h.StreamError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
	return true
}

// ForwardErrorAlreadyCommunicated 判断 Forward 实现返回错误前是否已经
// 向客户端写出了完整错误响应。
//
// 该判断有意比“writer size 变化”更窄：流式响应可能只发过保活 ping 或部分数据，
// 此时 handler 仍需要追加协议级终止错误。Forward 写出的非 SSE 响应不同：
// service 层辅助函数已经写出客户端可见的 JSON 响应体，再追加通用流式兜底会污染响应。
func ForwardErrorAlreadyCommunicated(c *gin.Context, writerSizeBeforeForward int, err error) bool {
	if err == nil || c == nil || c.Writer == nil {
		return false
	}
	if c.Writer.Size() == writerSizeBeforeForward {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(c.Writer.Header().Get("Content-Type")))
	if contentType == "" {
		return false
	}
	return !strings.Contains(contentType, "text/event-stream")
}

// Error 返回Claude API格式的错误响应
func (h MessagesErrorOutput) Error(c *gin.Context, status int, errType, message string) {
	h.ErrorWithCode(c, status, errType, "", message)
}

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func (h MessagesErrorOutput) ErrorWithCode(c *gin.Context, status int, errType, code, message string) {
	WriteAnthropicError(c, status, errType, code, message)
}

// Exhausted 只投影已分类失败，展示规则保持唯一实现。
func (h MessagesErrorOutput) Exhausted(c *gin.Context, failure *forwardcore.UpstreamFailoverError, platform string, started bool) {
	var rules ErrorRuleMatcher
	if h.Rules != nil {
		rules = h.Rules
	}
	WriteAnthropicFailover(c, failure, platform, started, rules, forwardcore.IsOpenAISilentRefusalErrorBody, forwardcore.OpenAISilentRefusalClientMessage())
}

// ResponsesError writes an error in OpenAI Responses API format.
func (h MessagesErrorOutput) ResponsesError(c *gin.Context, status int, code, message string) {
	WriteCompatibleResponsesError(c, status, code, message)
}

// ResponsesExhausted writes a failover-exhausted error in Responses format.
func (h MessagesErrorOutput) ResponsesExhausted(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if lastErr != nil {
		CopyFailoverRetryAfter(c, lastErr.ResponseHeaders)
	}
	statusCode := http.StatusBadGateway
	if lastErr != nil && lastErr.StatusCode > 0 {
		statusCode = lastErr.StatusCode
	}
	status, code, message := statusCode, "server_error", "All available accounts exhausted"
	if lastErr != nil && lastErr.IsCredentialFailure() {
		status, message = CredentialFailoverClientResponse(lastErr)
	} else if lastErr != nil && gatewayprovider.IsOpenAICapacityShed(lastErr) && strings.TrimSpace(lastErr.ClientMessage) != "" {
		status = lastErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		message = lastErr.ClientMessage
	} else if lastErr != nil && forwardcore.IsOpenAISilentRefusalErrorBody(lastErr.ResponseBody) {
		SetOpsUpstreamError(c, statusCode, forwardcore.OpenAISilentRefusalClientMessage(), "")
		status, code, message = http.StatusBadGateway, "upstream_error", forwardcore.OpenAISilentRefusalClientMessage()
	} else if lastErr != nil && statusCode == http.StatusTooManyRequests {
		status, code, message = http.StatusTooManyRequests, "rate_limit_error", "All available accounts are currently rate-limited. Please retry later."
	}
	if streamStarted {
		// A slot-wait heartbeat commits HTTP 200 before any upstream response.
		// In that case a terminal frame is still required; once any semantic or
		// official terminal bytes exist, preserve them without appending a second
		// generic response.failed.
		MarkOpsStreamError(c, code, message, status)
		if c != nil && c.Writer != nil && (c.Writer.Size() <= 0 || StreamHasOnlyHeartbeats(c)) {
			WriteResponsesFailedSSE(c, code, "", message, ErrorRequestID(c), ErrorRequestModel(c))
		}
		return
	}
	h.ResponsesError(c, status, code, message)
}

// ChatError writes an error in OpenAI Chat Completions format.
func (h MessagesErrorOutput) ChatError(c *gin.Context, status int, errType, message string) {
	WriteCompatibleChatError(c, status, errType, message)
}

// ChatExhausted writes a failover-exhausted error in CC format.
func (h MessagesErrorOutput) ChatExhausted(c *gin.Context, lastErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if streamStarted {
		return
	}
	if lastErr != nil {
		CopyFailoverRetryAfter(c, lastErr.ResponseHeaders)
	}
	if lastErr != nil && lastErr.IsCredentialFailure() {
		status, message := CredentialFailoverClientResponse(lastErr)
		h.ChatError(c, status, "server_error", message)
		return
	}
	if lastErr != nil && gatewayprovider.IsOpenAICapacityShed(lastErr) && strings.TrimSpace(lastErr.ClientMessage) != "" {
		status := lastErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		h.ChatError(c, status, "server_error", lastErr.ClientMessage)
		return
	}
	statusCode := http.StatusBadGateway
	if lastErr != nil && lastErr.StatusCode > 0 {
		statusCode = lastErr.StatusCode
	}
	if lastErr != nil && forwardcore.IsOpenAISilentRefusalErrorBody(lastErr.ResponseBody) {
		SetOpsUpstreamError(c, statusCode, forwardcore.OpenAISilentRefusalClientMessage(), "")
		h.ChatError(c, http.StatusBadGateway, "upstream_error", forwardcore.OpenAISilentRefusalClientMessage())
		return
	}
	h.ChatError(c, statusCode, "server_error", "All available accounts exhausted")
}

func (h MessagesErrorOutput) GeminiExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError) {
	if failoverErr == nil {
		WriteGoogleError(c, http.StatusBadGateway, "Upstream request failed")
		return
	}

	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody

	// 先检查透传规则
	if h.Rules != nil && len(responseBody) > 0 {
		if rule := h.Rules.MatchRule(capability.PlatformGemini, statusCode, responseBody); rule != nil {
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

			WriteGoogleError(c, respCode, msg)
			return
		}
	}

	// 记录原始上游状态码，以便 ops 错误日志捕获真实的上游错误
	upstreamMsg := upstream.ExtractErrorMessage(responseBody)
	SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	// 使用默认的错误映射
	status, message := GeminiUpstreamError(statusCode)
	WriteGoogleError(c, status, message)
}

func GeminiUpstreamError(statusCode int) (int, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "Upstream request failed"
	}
}
