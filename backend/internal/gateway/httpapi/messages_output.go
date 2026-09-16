package httpapi

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MessagesOutput 是同步 HTTP 输出适配，兼容执行适配只用 HTTP 字段读写响应与观测。
// 身份、路由、报文和资金状态从 execution.Request 取得，不从这里恢复业务实体。
type MessagesOutput struct {
	Concurrency *ConcurrencyHelper
	ResponseSink
	HTTP          *gin.Context
	Log           *zap.Logger
	StreamStarted *bool
}
