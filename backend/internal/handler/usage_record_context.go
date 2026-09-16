package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/gin-gonic/gin"
)

// usageRecordContextFromGin 仅在同步入口读取请求；异步使用独立快照。
func usageRecordContextFromGin(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return completion.SnapshotContext(c.Request.Context())
}
func wrapUsageRecordTaskContext(c *gin.Context, task func(context.Context)) func(context.Context) {
	return completion.WrapTaskContext(usageRecordContextFromGin(c), task)
}
