package routes

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	gatewayhttpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// extendedRouteProtocol 为非文本入口及别名统一命名；已有任务操作不受新建开关影响。
func extendedRouteProtocol(method, path string) domain.ProtocolID {
	protocol := routeProtocol(method, path)
	// Compact 由 Responses 通配路由完成路径校验后检查，保留无效路径的错误语义。
	if protocol == domain.ProtocolResponsesCompact {
		return ""
	}
	return protocol
}

// routeProtocol 仅移除完整的别名前缀，避免相似路径被误归为已知入口。
func routeProtocol(method, path string) domain.ProtocolID {
	for _, prefix := range []string{"/backend-api/codex", "/v1"} {
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
		}
	}
	protocol, _ := gatewayhttpapi.ProtocolForRoute(method, path)
	return protocol
}

func requireExtendedProtocol(c *gin.Context) {
	protocol := extendedRouteProtocol(c.Request.Method, c.Request.URL.Path)
	if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil && !slices.Contains(domain.SupportedGroupClientProtocols(key.Group.Platform), protocol) {
		c.Next()
		return
	}
	if protocol == "" || enforceGroupClientProtocol(c, protocol, groupClientProtocolErrorOpenAI) {
		c.Next()
	}
}
