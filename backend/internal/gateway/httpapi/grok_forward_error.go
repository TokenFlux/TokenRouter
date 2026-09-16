package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WriteGrokForwardInvalidRequest 保留端点/工具错误的 OpenAI envelope，不增加提交标记。
func WriteGrokForwardInvalidRequest(c *gin.Context, message, param string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message, "param": param}})
}
