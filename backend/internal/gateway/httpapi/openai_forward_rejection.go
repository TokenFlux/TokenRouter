// 转发准备阶段只向 HTTP Adapter 提交明确错误投影，不持有 ResponseWriter。
package httpapi

import "github.com/gin-gonic/gin"

// WriteOpenAIForwardRejection 保留可选 param 和原 JSON envelope。
func WriteOpenAIForwardRejection(c *gin.Context, status int, kind, message, param string) {
	if c == nil {
		return
	}
	payload := gin.H{"type": kind, "message": message}
	if param != "" {
		payload["param"] = param
	}
	c.JSON(status, gin.H{"error": payload})
}
