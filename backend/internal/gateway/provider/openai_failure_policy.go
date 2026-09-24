package provider

import (
	"net/http"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
)

// OpenAIFailoverPolicy 只计算当前账号的恢复资格与截止时间，不执行请求或切换循环。
type OpenAIFailoverPolicy struct {
	Health *accountprovider.OpenAIResponseHealth
}

const (
	openAIRequestBodyTooLargeReason              = forwardcore.GatewayFailureReason("openai_request_body_too_large")
	openAIUpstreamAccessUnavailableClientMessage = "Upstream access is temporarily unavailable, please retry later"
	openAIOAuth429RetryDelay                     = 500 * time.Millisecond
	openAIOAuth429MaxRetryDelay                  = 8 * time.Second
)

func OpenAI429RetryDelay(headers http.Header, deadline time.Time) time.Duration {
	delay := openAIOAuth429RetryDelay
	now := time.Now()
	if resetAt := openai.ParseRetryAfterResetTime(headers, now); resetAt != nil && resetAt.After(now) {
		delay = resetAt.Sub(now)
	}
	if delay > openAIOAuth429MaxRetryDelay {
		delay = openAIOAuth429MaxRetryDelay
	}
	if remaining := time.Until(deadline); !deadline.IsZero() && delay > remaining {
		delay = remaining
	}
	if delay < 0 {
		return 0
	}
	return delay
}

func ShouldFailoverUpstreamStatus(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

func ShouldFailoverOpenAIResponse(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	// cyber_policy 即使被中间层包成 5xx，仍属于请求拒绝，不触发账号切换。
	if IsOpenAICyberWarningPayload(upstreamBody, upstreamMsg) {
		return false
	}
	if openai.IsOpenAIContextWindowError(upstreamMsg, upstreamBody) {
		return false
	}
	if IsOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if IsOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if ShouldFailoverUpstreamStatus(statusCode) {
		return true
	}
	return openai.IsOpenAITransientProcessingError(statusCode, upstreamMsg, upstreamBody)
}

func IsOpenAIRequestBodyTooLargeError(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	return statusCode == http.StatusRequestEntityTooLarge && !openai.IsOpenAIContextWindowError(upstreamMsg, upstreamBody)
}

func NewOpenAIUpstreamFailure(
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	requestScopedCapacity := openai.IsOpenAIRequestScopedCapacityShed(upstreamMsg, responseBody)
	failoverErr := &forwardcore.UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           responseBody,
		ResponseHeaders:        responseHeaders.Clone(),
		RetryableOnSameAccount: retryableOnSameAccount || requestScopedCapacity,
		RequestScopedTransient: requestScopedCapacity,
	}
	if IsOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Scope = forwardcore.GatewayFailureScopeAccount
		failoverErr.Reason = openAIRequestBodyTooLargeReason
		failoverErr.NextAccountAction = forwardcore.NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusRequestEntityTooLarge
		failoverErr.ClientMessage = forwardcore.OpenAIRequestBodyTooLargeClientMessage
	}
	if IsOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Stage = forwardcore.GatewayFailureStageAccountAuth
		failoverErr.Scope = forwardcore.GatewayFailureScopeAccount
		failoverErr.Reason = forwardcore.OpenAIUpstreamAccessStateReason
		failoverErr.NextAccountAction = forwardcore.NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusBadGateway
		failoverErr.ClientMessage = openAIUpstreamAccessUnavailableClientMessage
	} else if requestScopedCapacity {
		// 重试耗尽后保留供应商的过载说明，并按可重试服务端错误返回。
		failoverErr.ClientStatusCode = http.StatusServiceUnavailable
		failoverErr.ClientMessage = OpenAICapacityShedClientMessage(upstreamMsg, responseBody)
	}
	return failoverErr
}

func (p OpenAIFailoverPolicy) NewAccountFailure(
	account *ExecutionAccount,
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	return p.NewAccountFailureWithClassificationHeaders(account, statusCode, responseHeaders, responseHeaders, responseBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
}

func (p OpenAIFailoverPolicy) NewAccountFailureWithClassificationHeaders(
	account *ExecutionAccount,
	statusCode int,
	responseHeaders http.Header,
	classificationHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	oauth429Retry := p.Health.RetryOAuth429(ExecutionRecord(account), statusCode, shouldDisable, classificationHeaders, responseBody)
	failoverErr := NewOpenAIUpstreamFailure(
		statusCode,
		responseHeaders,
		responseBody,
		upstreamMsg,
		retryableOnSameAccount || oauth429Retry,
	)
	if oauth429Retry {
		failoverErr.SameAccountRetryDeadline = p.Health.RetryDeadline(ExecutionRecord(account))
		failoverErr.SameAccountRetryDelay = OpenAI429RetryDelay(responseHeaders, failoverErr.SameAccountRetryDeadline)
	}
	return failoverErr
}

func IsOpenAIUpstreamAccessStateError(_ string, body []byte) bool {
	return openai.IsOpenAIUpstreamAccessStateError("", body)
}

func IsOpenAIHTTPUpstreamAccessStateError(_ int, _ string, body []byte) bool {
	return openai.IsOpenAIHTTPUpstreamAccessStateError(0, "", body)
}

func OpenAICapacityShedClientMessage(upstreamMsg string, body []byte) string {
	for _, candidate := range []string{
		upstreamMsg,
		gjson.GetBytes(body, "error.message").String(),
		gjson.GetBytes(body, "response.error.message").String(),
		gjson.GetBytes(body, "message").String(),
	} {
		candidate = logredact.SanitizeUpstreamQueries(strings.TrimSpace(candidate))
		if candidate != "" && openai.IsOpenAICapacityShedMessage(candidate) {
			return candidate
		}
	}
	return "Upstream service is temporarily overloaded, please retry later"
}

// OpenAISemantic429Headers 仅把明确的 Spark 窗口头用于流内语义限流。
func OpenAISemantic429Headers(target *ExecutionAccount, model string, headers http.Header) http.Header {
	if IsCodexSparkModel(model) && target != nil && target.View().IsOpenAIOAuthLike() {
		return headers
	}
	return nil
}

// OpenAIStreamFailureRetryable 保留容量降载与账号池模式的同账号重试资格。
func OpenAIStreamFailureRetryable(account *ExecutionAccount, payload []byte, message string) bool {
	if account == nil {
		return false
	}
	// 容量降载由客户端身份或模型容量触发，与当前账号健康无关；非池账号也应先
	// 做有界同账号重试，避免无意义地轮换并冷却整组账号。
	if openai.IsOpenAIUpstreamCapacityShedEvent(payload) {
		return true
	}
	if !account.View().IsPoolMode() {
		return false
	}
	semanticStatus := openai.OpenAIStreamFailedEventSemanticStatus(payload, message)
	return account.View().IsPoolModeRetryableStatus(semanticStatus) ||
		openai.IsOpenAITransientProcessingError(http.StatusBadRequest, message, payload)

}
