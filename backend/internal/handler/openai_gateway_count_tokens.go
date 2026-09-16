// 旧计数入口仅作兼容委托；原生 HTTP 与无槽编排不复制上游算法。
package handler

import "github.com/gin-gonic/gin"

func (h *OpenAIGatewayHandler) GrokCountTokens(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().GrokCountTokens(c)
}
func (h *OpenAIGatewayHandler) CountTokens(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().CountTokens(c)
}
