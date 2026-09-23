package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	"github.com/gin-gonic/gin"
)

// WriteFastPolicyBlockedResponse 保留策略观察，具体 HTTP/SSE 输出委托 Adapter。
func WriteFastPolicyBlockedResponse(c *gin.Context, err *tierpolicy.BlockedError) {
	if c == nil || err == nil {
		return
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	WriteForwardFastPolicyBlocked(c, err.Message, StopOpenAICompactSSEKeepaliveCommitted, func(c *gin.Context, status int, kind, message string) {
		WriteOpenAICompactSSEFailureMessage(c, status, kind, message, MarkOpsStreamError)
	})
}
