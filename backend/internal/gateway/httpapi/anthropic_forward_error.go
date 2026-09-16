package httpapi

import (
	"github.com/gin-gonic/gin"
)

// AnthropicForwardErrorOutput 仅写通用 Messages 执行的既有格式；提交/流边界由调用方保持。
type AnthropicForwardErrorOutput struct{ Context *gin.Context }

func (o AnthropicForwardErrorOutput) Message(status int, kind, message string) {
	o.Context.JSON(status, gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
}
func (o AnthropicForwardErrorOutput) Raw(status int, body []byte) {
	o.Context.Data(status, "application/json", body)
}
