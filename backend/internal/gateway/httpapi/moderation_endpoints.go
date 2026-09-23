package httpapi

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/gin-gonic/gin"
)

// GatewayModerationEndpoints 只读取路由和强制平台，不决定审核资格。
type GatewayModerationEndpoints struct{}

func (GatewayModerationEndpoints) Inbound(c *gin.Context) string { return GetInboundEndpoint(c) }
func (GatewayModerationEndpoints) Forced(c *gin.Context) (string, bool) {
	return keyhttp.GetForcePlatformFromContext(c)
}
