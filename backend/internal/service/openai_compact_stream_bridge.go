package service

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

// MarkOpenAICompactClientStream 保留原标记键，状态由 HTTP 层唯一持有。
func MarkOpenAICompactClientStream(c *gin.Context) { gatewayhttp.MarkOpenAICompactClientStream(c) }

// OpenAICompactClientStreamKeyForTest 保留跨包契约测试入口。
func OpenAICompactClientStreamKeyForTest() string {
	return gatewayhttp.OpenAICompactClientStreamKeyForTest()
}
func openAICompactClientWantsStream(c *gin.Context) bool {
	return gatewayhttp.OpenAICompactClientWantsStream(c)
}
func writeOpenAICompactSSEBridge(c *gin.Context, status int, body []byte) bool {
	return gatewayhttp.WriteOpenAICompactSSEBridge(c, status, body, MarkOpsStreamError)
}

func writeOpenAICompactSSEFailureMessage(c *gin.Context, status int, kind, message string) {
	gatewayhttp.WriteOpenAICompactSSEFailureMessage(c, status, kind, message, MarkOpsStreamError)
}
func buildOpenAICompactSSEPayload(body []byte) ([]byte, bool) {
	return gatewayhttp.BuildOpenAICompactSSEPayload(body)
}
