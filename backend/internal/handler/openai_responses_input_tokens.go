// 旧入口只委托原生 HTTP 预检。
package handler

import (
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) ResponsesInputTokens(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().ResponsesInputTokens(c)
}
