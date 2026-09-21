package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// moderationHTTPEndpoints 只读取现有路由投影，不变更请求或决定资格。
type moderationHTTPEndpoints struct{}

func (moderationHTTPEndpoints) Inbound(c *gin.Context) string {
	return gatewayhttp.GetInboundEndpoint(c)
}
func (moderationHTTPEndpoints) Forced(c *gin.Context) (string, bool) {
	return middleware.GetForcePlatformFromContext(c)
}
func nativeModerationPort(s *moderationcore.ContentModerationService) gatewayhttp.ModerationPort {
	if s == nil {
		return nil
	}
	return s
}
func moderationAccountView(a *service.Account) *moderationflow.Account {
	if a == nil {
		return nil
	}
	return &moderationflow.Account{ID: a.ID, Name: a.Name, Platform: a.Platform}
}
