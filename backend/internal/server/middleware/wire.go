package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/gin-gonic/gin"
)

// JWTAuthMiddleware JWT 认证中间件类型
type JWTAuthMiddleware = identityhttp.JWTAuthMiddleware

// AdminAuthMiddleware 管理员认证中间件类型
type AdminAuthMiddleware = identityhttp.AdminAuthMiddleware

// StepUpAuthMiddleware 由身份 HTTP 契约唯一拥有。
type StepUpAuthMiddleware = identityhttp.StepUpAuthMiddleware

// APIKeyAuthMiddleware API Key 认证中间件类型
type APIKeyAuthMiddleware gin.HandlerFunc
