// 旧日志入口共享同一 HTTP 关联投影与后端。
package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func requestLogger(c *gin.Context, component string, fields ...zap.Field) *zap.Logger {
	return gatewayhttp.RequestLogger(c, component, fields...)
}
