//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/gin-gonic/gin"
)

func recordGatewayStreamHeartbeat(c *gin.Context, n int) { gatewayhttp.RecordStreamHeartbeat(c, n) }
