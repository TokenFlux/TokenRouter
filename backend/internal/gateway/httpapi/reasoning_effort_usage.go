package httpapi

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/gin-gonic/gin"
)

// BindRequestedReasoningEffort 在任何策略改写前保存客户端请求的档位。
func BindRequestedReasoningEffort(c *gin.Context, body []byte, model string) {
	if c == nil || c.Request == nil {
		return
	}
	effort := requeststate.CanonicalRequestedReasoningEffort(body, strings.TrimSpace(model))
	if effort == nil {
		return
	}
	c.Request = c.Request.WithContext(requeststate.WithRequestedReasoningEffort(c.Request.Context(), *effort))
}

// StampOpenAIRequestedReasoningEffort 将请求 context 中的档位写入转发结果。
func StampOpenAIRequestedReasoningEffort(result *forwardcore.OpenAIResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil || c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = requeststate.RequestedReasoningEffortFromContext(c.Request.Context())
}

// StampForwardRequestedReasoningEffort 将兼容桥的客户端档位写入转发结果。
func StampForwardRequestedReasoningEffort(result *forwardcore.MessagesResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil || c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = requeststate.RequestedReasoningEffortFromContext(c.Request.Context())
}
