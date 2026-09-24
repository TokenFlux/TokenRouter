package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/gin-gonic/gin"
)

// AntigravityOutput 持有当前 HTTP 交换与静态输出配置，不承载账号、重试或资金状态。
type AntigravityOutput struct {
	GoogleOutput
	Options googleforward.Options
}

func (s *AntigravityOutput) ClaudeError(status int, errType, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *AntigravityOutput) MappedClaudeError(account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte) error {
	c := s.Context

	MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	logBody, maxBytes := s.Options.LogConfig()
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

	// 记录上游错误详情便于排障（可选：由配置控制；不回显到客户端）
	if logBody {
		logging.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] upstream_error status=%d body=%s", upstreamStatus, logredact.TruncateLine(body, maxBytes))
	}

	// 检查错误透传规则
	if ptStatus, ptErrType, ptErrMsg, matched := ApplyErrorPassthroughRule(
		c, account.Record.Platform, upstreamStatus, body,
		0, "", "",
	); matched {
		c.JSON(ptStatus, gin.H{
			"type":  "error",
			"error": gin.H{"type": ptErrType, "message": ptErrMsg},
		})
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	switch upstreamStatus {
	case 400:
		statusCode = http.StatusBadRequest
		errType = "invalid_request_error"
		errMsg = antigravity.GetPassthroughOrDefault(upstreamMsg, "Invalid request")
	case 401:
		statusCode = http.StatusBadGateway
		errType = "authentication_error"
		errMsg = "Upstream authentication failed"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "permission_error"
		errMsg = "Upstream access forbidden"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
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

func (s *AntigravityOutput) GoogleError(status int, message string) error {
	c := s.Context

	MarkResponseCommitted(c)
	statusStr := "UNKNOWN"
	switch status {
	case 400:
		statusStr = "INVALID_ARGUMENT"
	case 404:
		statusStr = "NOT_FOUND"
	case 429:
		statusStr = "RESOURCE_EXHAUSTED"
	case 500:
		statusStr = "INTERNAL"
	case 502, 503:
		statusStr = "UNAVAILABLE"
	}

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  statusStr,
		},
	})
	return fmt.Errorf("%s", message)
}

func (s *AntigravityOutput) AntigravityCompatError(

	status int,
	errType string,
	message string,
) error {
	c := s.Context

	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
	return errors.New(message)
}

func (s *AntigravityOutput) MappedAntigravityCompatError(

	account *gatewayprovider.ExecutionAccount,
	upstreamStatus int,
	upstreamRequestID string,
	body []byte,
) error {
	c := s.Context

	MarkResponseCommitted(c)
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(google.ExtractPlatformMessage(body)))
	SetOpsUpstreamError(c, upstreamStatus, message, s.Options.ErrorDetail(body))
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

		Platform: account.Record.Platform,

		AccountID: account.Record.ID,

		AccountName: account.Record.Name,

		UpstreamStatusCode: upstreamStatus,

		UpstreamRequestID: upstreamRequestID,

		Kind: "http_error",

		Message: message,
	})
	c.JSON(protocolforward.MapStatus(upstreamStatus), gin.H{
		"error": gin.H{

			"message": antigravity.GetPassthroughOrDefault(message, "Upstream request failed"),

			"type": "upstream_error",

			"param": nil,

			"code": nil,
		},
	})
	return fmt.Errorf("upstream error: %d %s", upstreamStatus, message)
}

func (s *AntigravityOutput) MapAntigravityCollectionError(err error) error {

	var failoverError *protocolforward.UpstreamFailoverError
	if errors.As(err, &failoverError) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if strings.Contains(err.Error(), "stream data interval timeout") {
		return s.AntigravityCompatError(http.StatusBadGateway, "upstream_timeout", "Upstream stream data interval timeout")
	}
	if errors.Is(err, bufio.ErrTooLong) {
		return s.AntigravityCompatError(http.StatusBadGateway, "response_too_large", "Upstream response line too long")
	}
	return s.AntigravityCompatError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
}
