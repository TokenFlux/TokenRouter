package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
)

// GeminiOutput 持有当前 HTTP 交换与静态输出配置，不承载账号、重试或资金状态。
type GeminiOutput struct {
	GoogleOutput
	Options googleforward.Options
}

// GeminiCustomCodeSkippedError 对自定义错误码未命中的请求隐藏上游细节并返回 500。
func (s *GeminiOutput) GeminiCustomCodeSkippedError(account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte, write func()) error {
	c := s.Context

	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	upstreamDetail := s.Options.ErrorDetail(body)
	SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

		Platform: account.Record.Platform,

		AccountID: account.Record.ID,

		AccountName: account.Record.Name,

		UpstreamStatusCode: upstreamStatus,

		UpstreamRequestID: upstreamRequestID,

		Kind: "http_error",

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})
	write()
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d (not in custom error codes)", upstreamStatus)
	}
	return fmt.Errorf("gemini upstream error: %d (not in custom error codes) message=%s", upstreamStatus, upstreamMsg)
}

// GeminiNativeUpstreamError 按原始状态码和响应体透传不可切换的 Gemini 错误。
func (s *GeminiOutput) GeminiNativeUpstreamError(account *gatewayprovider.ExecutionAccount, resp *http.Response, respBody []byte, requestID string, isOAuth bool) error {
	c := s.Context

	respBody = gemininative.UnwrapIfNeeded(isOAuth, respBody)
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	upstreamDetail := s.Options.ErrorDetail(respBody)
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

		Platform: account.Record.Platform,

		AccountID: account.Record.ID,

		AccountName: account.Record.Name,

		UpstreamStatusCode: resp.StatusCode,

		UpstreamRequestID: requestID,

		Kind: "http_error",

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	MarkResponseCommitted(c)
	c.Data(resp.StatusCode, contentType, respBody)
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d", resp.StatusCode)
	}
	return fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

func (s *GeminiOutput) GeminiMappedError(account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte) error {
	c := s.Context

	MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if s.Options.LogErrorBody {
		maxBytes := s.Options.LogErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

		Platform: account.Record.Platform,

		AccountID: account.Record.ID,

		AccountName: account.Record.Name,

		UpstreamStatusCode: upstreamStatus,

		UpstreamRequestID: upstreamRequestID,

		Kind: "http_error",

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})

	if s.Options.LogErrorBody {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini] upstream error %d: %s", upstreamStatus, logredact.TruncateLine(body, s.Options.LogErrorBodyMaxBytes))
	}

	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c,
		capability.PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"type":  "error",
			"error": gin.H{"type": errType, "message": errMsg},
		})
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d (passthrough rule matched)", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	if mapped := MapGeminiErrorBodyToClaudeError(body); mapped != nil {
		errType = mapped.Type
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}

	switch upstreamStatus {
	case 400:
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		if errType == "" {
			errType = "invalid_request_error"
		}
		if errMsg == "" {
			if upstreamMsg != "" {
				errMsg = upstreamMsg
			} else {
				errMsg = "Invalid request"
			}
		}
	case 401:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "authentication_error"
		}
		if errMsg == "" {
			errMsg = "Upstream authentication failed, please contact administrator"
		}
	case 403:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "permission_error"
		}
		if errMsg == "" {
			errMsg = "Upstream access forbidden, please contact administrator"
		}
	case 404:
		if statusCode == 0 {
			statusCode = http.StatusNotFound
		}
		if errType == "" {
			errType = "not_found_error"
		}
		if errMsg == "" {
			errMsg = "Resource not found"
		}
	case 429:
		if statusCode == 0 {
			statusCode = http.StatusTooManyRequests
		}
		if errType == "" {
			errType = "rate_limit_error"
		}
		if errMsg == "" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		if statusCode == 0 {
			statusCode = http.StatusServiceUnavailable
		}
		if errType == "" {
			errType = "overloaded_error"
		}
		if errMsg == "" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	case 500, 502, 503, 504:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			switch upstreamStatus {
			case 504:
				errType = "timeout_error"
			case 503:
				errType = "overloaded_error"
			default:
				errType = "api_error"
			}
		}
		if errMsg == "" {
			errMsg = "Upstream service temporarily unavailable"
		}
	default:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "upstream_error"
		}
		if errMsg == "" {
			errMsg = "Upstream request failed"
		}
	}

	c.JSON(statusCode, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": errMsg},
	})
	if upstreamMsg == "" {
		return fmt.Errorf("upstream error: %d", upstreamStatus)
	}
	return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
}

func (s *GeminiOutput) ClaudeError(status int, errType, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiOutput) GoogleError(status int, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  HTTPStatusToGoogleStatus(status),
		},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiOutput) GeminiOpenAICompatMappedError(

	account *gatewayprovider.ExecutionAccount,
	upstreamStatus int,
	upstreamRequestID string,
	body []byte,
	protocol gemininative.OpenAICompatProtocol,
) error {
	c := s.Context

	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, "")
	if account != nil {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

			Platform: account.Record.Platform,

			AccountID: account.Record.ID,

			AccountName: account.Record.Name,

			UpstreamStatusCode: upstreamStatus,

			UpstreamRequestID: upstreamRequestID,

			Kind: "http_error",

			Message: upstreamMsg,
		})
	}

	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c,
		capability.PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		return s.GeminiOpenAICompatError(protocol, status, errType, errMsg)
	}

	statusCode := http.StatusBadGateway
	errType := "upstream_error"
	errMsg := "Upstream request failed"
	if mapped := MapGeminiErrorBodyToClaudeError(body); mapped != nil {
		if mapped.Type != "" {
			errType = mapped.Type
		}
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}

	switch upstreamStatus {
	case http.StatusBadRequest:
		if statusCode == http.StatusBadGateway {
			statusCode = http.StatusBadRequest
		}
		if errType == "upstream_error" {
			errType = "invalid_request_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Invalid request"
		}
	case http.StatusNotFound:
		statusCode = http.StatusNotFound
		if errType == "upstream_error" {
			errType = "not_found_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Resource not found"
		}
	case http.StatusTooManyRequests:
		statusCode = http.StatusTooManyRequests
		if errType == "upstream_error" {
			errType = "rate_limit_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		statusCode = http.StatusServiceUnavailable
		if errType == "upstream_error" {
			errType = "overloaded_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	}

	if upstreamMsg != "" && errMsg == "Upstream request failed" {
		errMsg = upstreamMsg
	}
	// 池模式的 4xx 不会切换账号，客户端需要看到上游给出的具体校验原因；
	// 普通账号仍保留兼容层的通用错误文案。
	if account != nil && account.View().IsPoolMode() && upstreamStatus >= http.StatusBadRequest && upstreamMsg != "" {
		errMsg = upstreamMsg
	}
	return s.GeminiOpenAICompatError(protocol, statusCode, errType, errMsg)
}

// GeminiOpenAICompatError 按客户端入口输出对应的 OpenAI 错误格式。
func (s *GeminiOutput) GeminiOpenAICompatError(

	protocol gemininative.OpenAICompatProtocol,
	status int,
	errType string,
	message string,
) error {
	c := s.Context

	if protocol == gemininative.OpenAICompatResponses {
		(&ForwardConversionOutput{Context: c, Responses: true, Commit: func() { MarkResponseCommitted(c) }}).Error(status, errType, message)
		return fmt.Errorf("%s", message)
	}
	return s.ChatError(status, errType, message)
}

func (s *GeminiOutput) ChatError(status int, errType, message string) error {
	c := s.Context

	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
	return fmt.Errorf("%s", message)
}
