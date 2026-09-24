package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

const (
	grokMissingUsageErrorCode = "grok_missing_usage"
	grokMissingUsageMessage   = "xAI upstream returned a successful chat completion without billable usage"
)

// NewGrokMissingUsageFailure 构造稳定的缺失用量故障转移错误并写入 Grok Ops 诊断。
func NewGrokMissingUsageFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamRequestID string) *forwardcore.UpstreamFailoverError {
	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.Record.ID
		accountName = account.Record.Name
	}
	SetOpsUpstreamError(c, http.StatusBadGateway, grokMissingUsageMessage, "")
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           capability.PlatformGrok,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               "failover",
		Message:            grokMissingUsageMessage,
	})

	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"code":    grokMissingUsageErrorCode,
			"message": grokMissingUsageMessage,
		},
	})
	headers := http.Header{}
	if requestID := strings.TrimSpace(upstreamRequestID); requestID != "" {
		headers.Set("x-request-id", requestID)
	}
	return &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    body,
		ResponseHeaders: headers,
	}
}
