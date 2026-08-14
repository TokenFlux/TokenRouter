package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	grokMissingUsageErrorCode = "grok_missing_usage"
	grokMissingUsageMessage   = "xAI upstream returned a successful chat completion without billable usage"
)

// hasBillableGrokChatUsage 只检查聊天结算实际使用的聚合 token 桶。
// 明细字段本身不能证明响应可安全结算，至少一个聚合桶必须为正数。
func hasBillableGrokChatUsage(usage OpenAIUsage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0
}

// requiresBillableGrokChatUsage 根据实际账号平台和最终模型身份识别 Grok 流量。
// Grok 可由通用 OpenAI 兼容账号承载，因此不能只检查 account.Platform；同时不使用
// 未映射的客户端模型，避免 Grok 命名别名映射到非 Grok 上游时被误判。
func requiresBillableGrokChatUsage(account *Account, models ...string) bool {
	if account != nil && account.Platform == PlatformGrok {
		return true
	}
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if separator := strings.LastIndex(normalized, "/"); separator >= 0 {
			normalized = strings.TrimSpace(normalized[separator+1:])
		}
		if normalized == "grok" || strings.HasPrefix(normalized, "grok-") {
			return true
		}
	}
	return false
}

// newGrokMissingUsageFailoverError 构造稳定的缺失用量故障转移错误并写入 Grok Ops 诊断。
func newGrokMissingUsageFailoverError(c *gin.Context, account *Account, upstreamRequestID string) *UpstreamFailoverError {
	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.ID
		accountName = account.Name
	}

	setOpsUpstreamError(c, http.StatusBadGateway, grokMissingUsageMessage, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           PlatformGrok,
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
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    body,
		ResponseHeaders: headers,
	}
}
