package httpapi

import (
	"net/http"
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"
)

const (
	openAISilentRefusalUpstreamMessage   = "OpenAI upstream returned an empty completion stream with finish_reason=stop and no usage"
	openAIResponsesEmptyCompletedMessage = "OpenAI upstream returned an empty response.completed stream with no output and no usage"
)

func NewOpenAISilentRefusalFailoverError(c *gin.Context, account *UpstreamErrorAccount, upstreamRequestID string) *forwardcore.UpstreamFailoverError {
	accountID := int64(0)
	accountName := ""
	platform := capability.PlatformOpenAI
	if account != nil {
		accountID = account.ID
		accountName = account.Name
		platform = account.Platform
	}
	SetOpsUpstreamError(c, http.StatusBadGateway, openAISilentRefusalUpstreamMessage, "")
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "failover",
		Message:            openAISilentRefusalUpstreamMessage,
	})

	headers := http.Header{}
	if strings.TrimSpace(upstreamRequestID) != "" {
		headers.Set("x-request-id", strings.TrimSpace(upstreamRequestID))
	}
	return &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    forwardcore.OpenAISilentRefusalErrorBody(),
		ResponseHeaders: headers,
	}
}

// NewOpenAIResponsesEmptyCompletedFailoverError 将空 Responses 终态标记为可重试的上游异常。
// 这类响应没有任何可见输出、用量或错误，不应作为成功请求结算。
func NewOpenAIResponsesEmptyCompletedFailoverError(c *gin.Context, account *UpstreamErrorAccount, upstreamRequestID string) *forwardcore.UpstreamFailoverError {
	accountID := int64(0)
	accountName := ""
	platform := capability.PlatformOpenAI
	if account != nil {
		accountID = account.ID
		accountName = account.Name
		platform = account.Platform
	}
	SetOpsUpstreamError(c, http.StatusBadGateway, openAIResponsesEmptyCompletedMessage, "")
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "failover",
		Message:            openAIResponsesEmptyCompletedMessage,
	})

	headers := http.Header{}
	if strings.TrimSpace(upstreamRequestID) != "" {
		headers.Set("x-request-id", strings.TrimSpace(upstreamRequestID))
	}
	return &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    forwardcore.OpenAISilentRefusalErrorBody(),
		ResponseHeaders: headers,
	}
}

// UpstreamErrorAccount 只传递已选账号的安全观测字段。
type UpstreamErrorAccount struct {
	ID             int64
	Name, Platform string
}
