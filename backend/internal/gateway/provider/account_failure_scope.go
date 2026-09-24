package provider

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/tidwall/gjson"
)

// IsContentPolicyRejection 识别只由当前请求内容触发的 OpenAI 安全拒绝。
// 这类错误即使通过流内事件被推断为 502，也不能命中账号级自定义错误策略。
func IsContentPolicyRejection(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	for _, path := range []string{
		"error.code",
		"error.type",
		"response.error.code",
		"response.error.type",
	} {
		marker := strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, path).String()))
		if strings.Contains(marker, "content_policy") ||
			strings.Contains(marker, "content_filter") ||
			strings.Contains(marker, "safety") ||
			strings.Contains(marker, "moderation") {
			return true
		}
	}
	for _, path := range []string{"error.message", "response.error.message", "message"} {
		message := strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, path).String()))
		if strings.Contains(message, "content policy") ||
			strings.Contains(message, "blocked by policy") ||
			strings.Contains(message, "safety system") ||
			strings.Contains(message, "violates our policies") {
			return true
		}
	}
	return false
}

// IsRequestScopedAccountFailure 识别只与当前请求有关、不能修改账号健康状态的错误。
// 413 请求体限制可能是账号上游代理的独有限制，仍允许切换账号，因此不在这里统一排除。
func IsRequestScopedAccountFailure(account *ExecutionAccount, statusCode int, responseBody []byte) bool {
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody))
	if hit, _, _ := openai.DetectOpenAICyberPolicy(responseBody); hit {
		return true
	}
	if IsOpenAICyberWarningPayload(responseBody, upstreamMsg) ||
		IsContentPolicyRejection(responseBody) ||
		openai.IsOpenAIClientInvalidRequestError(statusCode, upstreamMsg, responseBody) ||
		openai.IsOpenAIContextWindowError(upstreamMsg, responseBody) {
		return true
	}
	return account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(statusCode, responseBody)
}

// IsTransientAccountFailure 保留状态码与请求处理错误的短期恢复分类。
func IsTransientAccountFailure(statusCode int, responseBody []byte) bool {
	switch statusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 520, 521, 522, 523, 524:
		return true
	case http.StatusBadRequest:
		return openai.IsOpenAITransientProcessingError(statusCode, "", responseBody)
	default:
		return false
	}
}
