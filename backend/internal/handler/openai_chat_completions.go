package handler

import (
	"github.com/gin-gonic/gin"
)

// ChatCompletions 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) ChatCompletions(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().ChatCompletions(c)
}
