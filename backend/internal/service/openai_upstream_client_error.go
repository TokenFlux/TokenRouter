package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAIUpstreamClientErrorFallbackType 是上游未返回 error.type 时的兜底类型。
const openAIUpstreamClientErrorFallbackType = "invalid_request_error"

// openAIUpstreamClientErrorFallbackMessage 是上游未返回可用消息时的兜底文案。
const openAIUpstreamClientErrorFallbackMessage = "Upstream rejected the request"

// isOpenAIDeterministicClientError 判断错误是否为不可通过换号或重试恢复的客户端请求错误。
// fork 的普通账号与池模式使用不同故障转移规则，因此必须同时检查既有分类结果。
func isOpenAIDeterministicClientError(statusCode int, shouldFailover bool) bool {
	return statusCode == http.StatusBadRequest && !shouldFailover
}

// writeOpenAIUpstreamClientError 以 OpenAI 错误信封回写确定性客户端错误。
// message 已由调用方完成身份脱敏和敏感查询参数清洗；这里只保留诊断必需字段，
// 不直接透传上游完整正文，避免把扩展字段或内部调试信息暴露给客户端。
func writeOpenAIUpstreamClientError(c *gin.Context, statusCode int, body []byte, upstreamMsg string) {
	errorPayload := gin.H{"type": openAIUpstreamClientErrorFallbackType}
	if errType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String()); errType != "" {
		errorPayload["type"] = errType
	}
	if code := strings.TrimSpace(extractUpstreamErrorCode(body)); code != "" {
		errorPayload["code"] = code
	}
	if param := strings.TrimSpace(gjson.GetBytes(body, "error.param").String()); param != "" {
		errorPayload["param"] = param
	}
	message := strings.TrimSpace(upstreamMsg)
	if message == "" {
		message = openAIUpstreamClientErrorFallbackMessage
	}
	errorPayload["message"] = message

	c.JSON(statusCode, gin.H{"error": errorPayload})
}
