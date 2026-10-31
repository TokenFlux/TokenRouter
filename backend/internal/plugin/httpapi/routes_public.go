package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// routes_public.go registers contribution endpoints under the existing panel
// prefix with application-owned authentication, backend-mode policy, and limits.
// Manifest matching precedes dynamic authentication. Public requests receive
// the public-address limiter, and any authenticated caller also receives the
// existing user-scoped panel limiter without joining command idempotency handling.

// PublicMiddleware carries existing panel guards without introducing another credential parser.
type PublicMiddleware struct {
	JWT         gin.HandlerFunc // existing user JWT authentication
	Admin       gin.HandlerFunc // existing administrator credential authentication
	BackendUser gin.HandlerFunc // existing panel self-service mode guard
	Panel       gin.HandlerFunc // existing authenticated panel request limiter
	PublicIP    gin.HandlerFunc // existing public-address panel request limiter
}

// RegisterPublicRoutes mounts authenticated UI discovery, public assets, and declared API forwarding.
// All guards must be initialized before registration. The proxy's public path
// accepts missing credentials; supplied Authorization uses the normal JWT rejection rules.
func RegisterPublicRoutes(v1 *gin.RouterGroup, handler *PublicHandler, guards PublicMiddleware) {
	group := v1.Group("/plugins", handler.Ready)
	group.GET("/ui", guards.JWT, guards.BackendUser, guards.Panel, handler.UI)
	group.GET("/:id/assets/:version/*file", guards.PublicIP, handler.Asset)
	group.Any("/:id/api/*path", handler.Resolve, publicProxyLimit(guards.PublicIP), proxyAuthentication(guards), guards.BackendUser, guards.Panel, handler.Proxy)
}

// proxyAuthentication selects the shared authentication handler from the matched declaration.
// Public endpoints are anonymous when Authorization is absent. Each selected
// middleware owns its normal continuation and abort behavior within the Gin chain.
func proxyAuthentication(guards PublicMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		selected, ok := selectedFrom(c)
		if !ok {
			httpx.ErrorFrom(c, plugin.ErrRouteNotFound)
			c.Abort()
			return
		}
		switch selected.route.Auth {
		case "public":
			if c.GetHeader("Authorization") == "" {
				c.Next()
				return
			}
			guards.JWT(c)
		case "user":
			guards.JWT(c)
		case "admin":
			guards.Admin(c)
		default:
			httpx.ErrorFrom(c, plugin.ErrRouteNotFound)
			c.Abort()
		}
	}
}

// publicProxyLimit applies public-address counting only to declared anonymous-capable endpoints.
// Authenticated public callers still pass this address bucket and the subsequent
// user bucket. User and administrator endpoints retain their ordinary panel accounting.
func publicProxyLimit(limit gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		selected, ok := selectedFrom(c)
		if ok && selected.route.Auth == "public" {
			limit(c)
			return
		}
		c.Next()
	}
}
