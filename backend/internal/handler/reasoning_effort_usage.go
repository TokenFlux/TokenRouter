package handler

import (
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// bindRequestedReasoningEffort 在任何策略改写前保存客户端请求的档位。
func bindRequestedReasoningEffort(c *gin.Context, body []byte, model string) {
	if c == nil || c.Request == nil {
		return
	}
	effort := service.CanonicalRequestedReasoningEffort(body, strings.TrimSpace(model))
	if effort == nil {
		return
	}
	c.Request = c.Request.WithContext(service.WithRequestedReasoningEffort(c.Request.Context(), *effort))
}

// stampOpenAIRequestedReasoningEffort 将请求 context 中的档位写入转发结果。
func stampOpenAIRequestedReasoningEffort(result *forwardcore.OpenAIResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil || c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = service.RequestedReasoningEffortFromContext(c.Request.Context())
}

// stampForwardRequestedReasoningEffort 将兼容桥的客户端档位写入转发结果。
func stampForwardRequestedReasoningEffort(result *forwardcore.MessagesResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil || c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = service.RequestedReasoningEffortFromContext(c.Request.Context())
}
