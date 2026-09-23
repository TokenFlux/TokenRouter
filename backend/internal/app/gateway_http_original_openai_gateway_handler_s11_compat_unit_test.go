//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package app

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/wsentry"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func closeOpenAIWSFailoverExhausted(c *gin.Context, conn *coderws.Conn, err *forwardcore.UpstreamFailoverError) {
	gatewayhttp.CloseResponsesWSFailure(c, conn, wsentry.FailoverPresentation(err), gatewayhttp.MarkOpsStreamFailure)
}
