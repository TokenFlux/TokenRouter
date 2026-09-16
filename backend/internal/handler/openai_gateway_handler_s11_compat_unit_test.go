//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func closeOpenAIWSFailoverExhausted(c *gin.Context, conn *coderws.Conn, err *service.UpstreamFailoverError) {
	gatewayhttp.CloseResponsesWSFailure(c, conn, wsFailoverPresentation(err), service.MarkOpsStreamFailure)
}
